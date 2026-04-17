// Package bun is the Bun adapter for go-audit. It attaches a QueryHook that
// captures INSERT / UPDATE / DELETE statements and records them via the
// Auditor.
//
// On UPDATE / DELETE the hook performs a pre-query SELECT to snapshot the
// affected rows, so audit logs carry both old_values and new_values (the
// snapshot is diffed against the post-state for UPDATE, matching the GORM
// adapter). The snapshot strategy is, in order:
//
//  1. If the caller's Model has primary-key values set (e.g.
//     NewUpdate().Model(&x).WherePK() or Model(&slice).Bulk()), SELECT by PK.
//  2. Otherwise, parse the rendered WHERE clause out of the statement and
//     run SELECT * FROM <table> WHERE <same clause>.
//
// Set DataAuditConfig.SkipOldValues = true to skip the pre-query SELECT.
//
// Known limitation: the snapshot SELECT runs against the base *bun.DB, not
// the current *bun.Tx, so uncommitted rows written earlier in the same
// transaction are not visible. For strict tx-local snapshots, load rows
// explicitly before the write, or use the GORM adapter.
package bun

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/gopackx/go-audit"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// Register installs the audit QueryHook on db. All subsequent writes through
// db are audited automatically.
func Register(db *bun.DB, auditor audit.Auditor) {
	db.AddQueryHook(&hook{auditor: auditor, db: db})
}

type hook struct {
	auditor audit.Auditor
	db      *bun.DB
}

// snapshotKey stashes pre-change row values keyed by primary-key string.
type snapshotKey struct{}

// internalKey marks queries issued by the hook itself so BeforeQuery /
// AfterQuery skip them (prevents recursive snapshotting).
type internalKey struct{}

func (h *hook) BeforeQuery(ctx context.Context, event *bun.QueryEvent) context.Context {
	if ctx.Value(internalKey{}) != nil {
		return ctx
	}
	if h.auditor.Config().DataAudit.SkipOldValues {
		return ctx
	}
	op := event.Operation()
	if op != "UPDATE" && op != "DELETE" {
		return ctx
	}

	tm, ok := event.Model.(bun.TableModel)
	if !ok || tm == nil {
		return ctx
	}
	table := tm.Table()
	if table == nil {
		return ctx
	}
	rv := reflect.Indirect(reflect.ValueOf(tm.Value()))

	// Attempt 1: snapshot by PK values carried on the caller's model. Skip
	// when Model((*T)(nil)) was used — rv is invalid and no PKs are set.
	if rv.IsValid() {
		if snap := h.snapshotByPKs(ctx, table, rv); snap != nil {
			return context.WithValue(ctx, snapshotKey{}, snap)
		}
	}
	// Attempt 2: extract WHERE from the rendered SQL and run an equivalent
	// SELECT. This covers bulk updates / deletes with custom predicates
	// (e.g. NewUpdate().Model((*T)(nil)).Where("price < ?", 100).Set(...)).
	if snap := h.snapshotByRenderedWhere(ctx, table, event.Query); snap != nil {
		return context.WithValue(ctx, snapshotKey{}, snap)
	}
	return ctx
}

func (h *hook) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	if ctx.Value(internalKey{}) != nil {
		return
	}
	if event.Err != nil {
		return
	}

	var action string
	switch event.Operation() {
	case "INSERT":
		action = audit.ActionCreate
	case "UPDATE":
		action = audit.ActionUpdate
	case "DELETE":
		action = audit.ActionDelete
	default:
		return
	}

	tm, ok := event.Model.(bun.TableModel)
	if !ok || tm == nil {
		return
	}
	table := tm.Table()
	if table == nil {
		return
	}
	rv := reflect.Indirect(reflect.ValueOf(tm.Value()))

	snap, _ := ctx.Value(snapshotKey{}).(map[string]map[string]any)

	txID := audit.TransactionIDFromContext(ctx)
	if txID == "" {
		txID = audit.NewTransactionID()
	}

	// When the caller's Model has no rows (e.g. NewUpdate().Model((*T)(nil))
	// with custom WHERE) rv is invalid — fall back to emitting one entry per
	// snapshotted row below.
	rowsEmitted := 0
	if rv.IsValid() {
		iterateRows(rv, func(row reflect.Value) {
			entityID := primaryKeyString(table, row)
			values := fieldValues(table, row)
			emitEntry(ctx, h.auditor, table, action, entityID, values, snap, txID)
			rowsEmitted++
		})
	}
	if rowsEmitted == 0 && snap != nil {
		// The update/delete targeted rows via custom WHERE; emit one entry
		// per snapshotted row using snapshot values.
		for id, old := range snap {
			entry := audit.DataEntry{
				EntityType:    string(table.Name),
				EntityID:      id,
				Action:        action,
				TransactionID: txID,
			}
			switch action {
			case audit.ActionUpdate:
				// Without post-state per row, log old only. Callers can
				// still reconstruct diffs by querying the table.
				entry.OldValues = old
			case audit.ActionDelete:
				entry.OldValues = old
			}
			_ = h.auditor.RecordDataChange(ctx, entry)
		}
	}
}

func emitEntry(
	ctx context.Context,
	auditor audit.Auditor,
	table *schema.Table,
	action, entityID string,
	values map[string]any,
	snap map[string]map[string]any,
	txID string,
) {
	var oldVals map[string]any
	if snap != nil {
		oldVals = snap[entityID]
	}
	entry := audit.DataEntry{
		EntityType:    string(table.Name),
		EntityID:      entityID,
		Action:        action,
		TransactionID: txID,
	}
	switch action {
	case audit.ActionCreate:
		entry.NewValues = values
	case audit.ActionUpdate:
		entry.OldValues = oldVals
		entry.NewValues = values
	case audit.ActionDelete:
		if oldVals != nil {
			entry.OldValues = oldVals
		} else {
			entry.OldValues = values
		}
	}
	_ = auditor.RecordDataChange(ctx, entry)
}

// snapshotByPKs runs SELECT * WHERE (pk1=? AND pk2=?) OR (...) using the PK
// tuples carried by the caller's Model. Returns nil if no row has a populated
// PK (e.g. Model((*T)(nil))).
func (h *hook) snapshotByPKs(ctx context.Context, table *schema.Table, rv reflect.Value) map[string]map[string]any {
	if len(table.PKs) == 0 {
		return nil
	}

	var tuples [][]any
	iterateRows(rv, func(row reflect.Value) {
		tuple := make([]any, 0, len(table.PKs))
		allZero := true
		for _, pk := range table.PKs {
			v := pk.Value(row)
			if !v.IsValid() {
				return
			}
			if !v.IsZero() {
				allZero = false
			}
			tuple = append(tuple, v.Interface())
		}
		if allZero {
			return
		}
		tuples = append(tuples, tuple)
	})
	if len(tuples) == 0 {
		return nil
	}

	var parts []string
	var args []any
	for _, tuple := range tuples {
		cols := make([]string, 0, len(table.PKs))
		for i, pk := range table.PKs {
			cols = append(cols, "? = ?")
			args = append(args, bun.Ident(pk.Name), tuple[i])
		}
		parts = append(parts, "("+strings.Join(cols, " AND ")+")")
	}
	whereClause := strings.Join(parts, " OR ")

	slicePtr := reflect.New(reflect.SliceOf(table.Type))
	internalCtx := context.WithValue(ctx, internalKey{}, true)
	if err := h.db.NewSelect().
		Model(slicePtr.Interface()).
		Where(whereClause, args...).
		Scan(internalCtx); err != nil {
		return nil
	}
	return buildSnapshot(table, slicePtr.Elem())
}

// snapshotByRenderedWhere extracts the WHERE clause from the rendered SQL
// and runs the equivalent SELECT. Used when PKs cannot be read from the
// caller's Model (bulk update/delete with custom predicate).
func (h *hook) snapshotByRenderedWhere(ctx context.Context, table *schema.Table, rendered string) map[string]map[string]any {
	where := extractWhere(rendered)
	if where == "" {
		return nil
	}
	tableSQL := string(table.SQLName)
	if tableSQL == "" {
		tableSQL = string(table.Name)
	}

	sql := "SELECT * FROM " + tableSQL + " WHERE " + where
	slicePtr := reflect.New(reflect.SliceOf(table.Type))
	internalCtx := context.WithValue(ctx, internalKey{}, true)
	if err := h.db.NewRaw(sql).Scan(internalCtx, slicePtr.Interface()); err != nil {
		return nil
	}
	return buildSnapshot(table, slicePtr.Elem())
}

func buildSnapshot(table *schema.Table, slice reflect.Value) map[string]map[string]any {
	out := map[string]map[string]any{}
	for i := 0; i < slice.Len(); i++ {
		row := reflect.Indirect(slice.Index(i))
		id := primaryKeyString(table, row)
		if id == "" {
			continue
		}
		out[id] = fieldValues(table, row)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func iterateRows(rv reflect.Value, fn func(reflect.Value)) {
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			fn(reflect.Indirect(rv.Index(i)))
		}
	case reflect.Struct:
		fn(rv)
	case reflect.Ptr, reflect.Interface:
		if !rv.IsNil() {
			fn(reflect.Indirect(rv.Elem()))
		}
	}
}

func primaryKeyString(t *schema.Table, rv reflect.Value) string {
	rv = reflect.Indirect(rv)
	if len(t.PKs) == 0 {
		return ""
	}
	if len(t.PKs) == 1 {
		v := t.PKs[0].Value(rv)
		return fmt.Sprintf("%v", v.Interface())
	}
	parts := make([]any, 0, len(t.PKs))
	for _, f := range t.PKs {
		parts = append(parts, f.Value(rv).Interface())
	}
	return fmt.Sprintf("%v", parts)
}

func fieldValues(t *schema.Table, rv reflect.Value) map[string]any {
	rv = reflect.Indirect(rv)
	out := make(map[string]any, len(t.Fields))
	for _, f := range t.Fields {
		if f.Name == "" {
			continue
		}
		fv := f.Value(rv)
		if !fv.IsValid() {
			continue
		}
		out[f.Name] = fv.Interface()
	}
	return out
}

// extractWhere finds the top-level WHERE clause in a rendered UPDATE or DELETE
// statement and returns its text stripped of trailing RETURNING / ORDER BY /
// LIMIT / OFFSET / FOR UPDATE clauses. Returns "" if the statement has no
// WHERE.
//
// The scanner tracks single-quoted strings, double-quoted identifiers, and
// paren depth so subqueries / literals don't produce false matches.
func extractWhere(sql string) string {
	idx := findTopLevelToken(sql, " WHERE ")
	if idx < 0 {
		return ""
	}
	clause := sql[idx+len(" WHERE "):]
	clause = stripTrailingClauses(clause)
	return strings.TrimSpace(clause)
}

func stripTrailingClauses(sql string) string {
	for _, tok := range []string{" RETURNING ", " ORDER BY ", " LIMIT ", " OFFSET ", " FOR UPDATE"} {
		if idx := findTopLevelToken(sql, tok); idx >= 0 {
			sql = sql[:idx]
		}
	}
	return sql
}

// findTopLevelToken returns the index of the first case-insensitive
// occurrence of token in sql that sits at paren-depth 0 and outside of
// single-/double-quoted literals. Returns -1 when absent.
func findTopLevelToken(sql, token string) int {
	n, tn := len(sql), len(token)
	if tn == 0 || tn > n {
		return -1
	}
	depth := 0
	inSingle, inDouble := false, false
	for i := 0; i < n; i++ {
		c := sql[i]
		switch {
		case c == '\'' && !inDouble:
			// Handle '' escape inside single-quoted strings.
			if inSingle && i+1 < n && sql[i+1] == '\'' {
				i++
				continue
			}
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case inSingle || inDouble:
			// Skip contents of string / identifier literals.
		case c == '(':
			depth++
		case c == ')':
			if depth > 0 {
				depth--
			}
		case depth == 0 && i+tn <= n && strings.EqualFold(sql[i:i+tn], token):
			return i
		}
	}
	return -1
}
