package gorm

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"github.com/gopackx/go-audit"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// oldValuesKey is the typed key used to stash pre-change row snapshots on a
// GORM statement's Settings map. Using a dedicated struct type avoids string
// collisions with other plugins.
type oldValuesKey struct{}

// seenRowsKey marks (entityType, entityID) pairs that have already been
// recorded for the current statement so nested association inserts don't get
// double-audited when GORM walks parent + child slices. Used as the
// per-statement dedup fallback when an operation is not inside a transaction.
type seenRowsKey struct{}

// existingPKKey holds, per CREATE statement, the set of primary-key strings
// whose rows already had a non-zero PK *before* the INSERT ran. Such rows are
// pre-existing records being re-upserted (e.g. GORM re-saving loaded HasMany
// associations during a parent Update), not genuine inserts — so they must not
// be audited a second time as `create`. Captured in beforeCreate, read in
// recordEach. Stored on the statement's Settings because before/after of one
// statement share it.
type existingPKKey struct{}

// dedupState is the set of (entityType, entityID) pairs already audited within
// a single transaction, plus a depth counter so the set is torn down only once
// the outermost operation in that transaction completes. rootAction records the
// kind of the outermost operation (create/update/delete) that opened the scope,
// used to tell a genuine nested create (root = create) from an association
// re-save triggered by a parent update/delete (root != create).
type dedupState struct {
	depth      int
	rootAction string
	seen       map[string]struct{}
}

type callbacks struct {
	auditor audit.Auditor

	// mu guards txDedup. Entries are keyed by the transaction's ConnPool (a
	// *sql.Tx), so each in-flight transaction gets its own dedup set and the
	// set survives across the multiple GORM statements (initial INSERT +
	// FK-backfill upsert, parent + nested associations) that make up one
	// logical write. A single *sql.Tx is never used concurrently, so the
	// returned set is safe to read/write without holding mu.
	mu      sync.Mutex
	txDedup map[any]*dedupState
}

func newCallbacks(a audit.Auditor) *callbacks {
	return &callbacks{auditor: a, txDedup: map[any]*dedupState{}}
}

func (c *callbacks) afterCreate(db *gorm.DB) {
	c.recordEach(db, audit.ActionCreate, nil)
}

// enter/leave bracket every create/update/delete callback chain. They maintain
// a per-transaction depth counter so the shared dedup set lives for the whole
// operation tree (outer write + every nested association / backfill statement)
// and is freed exactly when the outermost operation unwinds — no leak. The
// first enter in a transaction records the root operation kind.
func (c *callbacks) enter(db *gorm.DB, action string) {
	pool := txKey(db)
	if pool == nil {
		return // not in a transaction: per-statement fallback handles dedup
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.txDedup[pool]
	if st == nil {
		st = &dedupState{seen: map[string]struct{}{}, rootAction: action}
		c.txDedup[pool] = st
	}
	st.depth++
}

// captureExistingPKs runs before a CREATE statement and records, on the
// statement's Settings, the primary keys of rows that already have a non-zero
// PK. Those rows are not first-time inserts (GORM is re-upserting pre-existing
// associations), so recordEach can skip auditing them as `create`.
func (c *callbacks) captureExistingPKs(db *gorm.DB) {
	if db.Statement == nil || db.Statement.Schema == nil {
		return
	}
	existing := map[string]struct{}{}
	iterateRows(db, func(rv reflect.Value) {
		if pkIsZero(db.Statement.Schema, rv) {
			return
		}
		existing[primaryKeyString(db.Statement.Schema, rv)] = struct{}{}
	})
	if len(existing) > 0 {
		db.Statement.Settings.Store(existingPKKey{}, existing)
	}
}

// rootAction returns the kind of the outermost operation for the current
// transaction, or "" when not in a transaction (or none recorded yet).
func (c *callbacks) rootAction(db *gorm.DB) string {
	pool := txKey(db)
	if pool == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.txDedup[pool]; st != nil {
		return st.rootAction
	}
	return ""
}

func (c *callbacks) leave(db *gorm.DB) {
	pool := txKey(db)
	if pool == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.txDedup[pool]; st != nil {
		st.depth--
		if st.depth <= 0 {
			delete(c.txDedup, pool)
		}
	}
}

func (c *callbacks) beforeUpdate(db *gorm.DB) {
	if c.auditor.Config().DataAudit.SkipOldValues {
		return
	}
	// Snapshot old values for each affected row so afterUpdate can diff.
	old, err := snapshotRows(db)
	if err != nil {
		_ = db.AddError(fmt.Errorf("go-audit: snapshot before update: %w", err))
		return
	}
	db.Statement.Settings.Store(oldValuesKey{}, old)
}

func (c *callbacks) afterUpdate(db *gorm.DB) {
	var old map[string]map[string]any
	if v, ok := db.Statement.Settings.Load(oldValuesKey{}); ok {
		old, _ = v.(map[string]map[string]any)
	}
	c.recordEach(db, audit.ActionUpdate, old)
}

func (c *callbacks) beforeDelete(db *gorm.DB) {
	if c.auditor.Config().DataAudit.SkipOldValues {
		return
	}
	old, err := snapshotRows(db)
	if err != nil {
		_ = db.AddError(fmt.Errorf("go-audit: snapshot before delete: %w", err))
		return
	}
	db.Statement.Settings.Store(oldValuesKey{}, old)
}

func (c *callbacks) afterDelete(db *gorm.DB) {
	var old map[string]map[string]any
	if v, ok := db.Statement.Settings.Load(oldValuesKey{}); ok {
		old, _ = v.(map[string]map[string]any)
	}
	action := audit.ActionDelete
	if isSoftDelete(db) {
		action = audit.ActionSoftDelete
	}
	c.recordEach(db, action, old)
}

// recordEach emits one audit entry per affected row.
func (c *callbacks) recordEach(db *gorm.DB, action string, old map[string]map[string]any) {
	if db.Error != nil || db.Statement.Schema == nil {
		return
	}
	entityType := db.Statement.Table

	txID := audit.TransactionIDFromContext(db.Statement.Context)
	if txID == "" {
		// Group rows in this statement under one transaction_id for batches.
		txID = audit.NewTransactionID()
	}

	// Dedup: GORM can audit the same row more than once per logical write —
	// nested associations walk parent + child slices, and on Postgres ≥ v1.31
	// a nested Create runs the create-callback chain twice (initial INSERT then
	// an FK-backfill upsert) across two separate statements. The seen-set is
	// scoped to the whole transaction so cross-statement repeats are caught,
	// falling back to per-statement scope when not in a transaction.
	seen := c.seenSet(db)

	// A row whose PK already existed before a CREATE statement, fired while the
	// transaction's root operation is an update/delete, is GORM re-saving a
	// loaded association (e.g. Model(&parent).Update(...) with parent.Items
	// still attached re-upserts the children). That's a re-link, not a genuine
	// insert, so it must not be re-audited as `create`. A genuine nested create
	// (root = create) and new children added during an update (PK still zero)
	// are unaffected.
	var existingPK map[string]struct{}
	if action == audit.ActionCreate && c.rootAction(db) != "" && c.rootAction(db) != audit.ActionCreate {
		if v, ok := db.Statement.Settings.Load(existingPKKey{}); ok {
			existingPK, _ = v.(map[string]struct{})
		}
	}

	iterateRows(db, func(rv reflect.Value) {
		entityID := primaryKeyString(db.Statement.Schema, rv)
		if existingPK != nil {
			if _, reSaved := existingPK[entityID]; reSaved {
				return
			}
		}
		seenKey := entityType + "\x00" + entityID
		if _, dup := seen[seenKey]; dup {
			return
		}
		seen[seenKey] = struct{}{}
		values := fieldValues(db.Statement.Schema, rv)

		var oldVals map[string]any
		if old != nil {
			oldVals = old[entityID]
		}

		entry := audit.DataEntry{
			EntityType:    entityType,
			EntityID:      entityID,
			Action:        action,
			OldValues:     oldVals,
			NewValues:     values,
			TransactionID: txID,
		}
		switch action {
		case audit.ActionCreate:
			entry.OldValues = nil
		case audit.ActionDelete:
			// Hard delete: new state is empty; old state is what we captured.
			if oldVals == nil {
				entry.OldValues = values
			}
			entry.NewValues = nil
		case audit.ActionSoftDelete:
			// Soft delete: capture deleted_at in new_values to show what changed.
			if oldVals == nil {
				entry.OldValues = values
			}
			entry.NewValues = softDeleteNewValues(db.Statement.Schema, rv)
		}
		if err := c.auditor.RecordDataChange(db.Statement.Context, entry); err != nil {
			_ = db.AddError(fmt.Errorf("go-audit: record %s: %w", action, err))
		}
	})
}

// snapshotRows issues a SELECT with the statement's current WHERE clause to
// capture the pre-change state of each affected row, keyed by primary key.
func snapshotRows(db *gorm.DB) (map[string]map[string]any, error) {
	if db.Statement.Schema == nil {
		return nil, nil
	}
	sess := db.Session(&gorm.Session{NewDB: true, Context: db.Statement.Context})
	// Use Table+Clauses to mirror the caller's filter.
	rowsModel := reflect.New(reflect.SliceOf(db.Statement.Schema.ModelType)).Interface()
	q := sess.Table(db.Statement.Table)
	// Re-apply the caller's WHERE conditions on the fresh session. Passing
	// the original *clause.Clause back into q.Clauses(...) makes GORM emit
	// "WHERE WHERE ..." (Postgres 42601) because the Clause carries its own
	// "WHERE" name and the builder also prepends one. Extract the
	// underlying clause.Where expression and wrap it in a fresh value so the
	// builder writes a single WHERE.
	if src, ok := db.Statement.Clauses["WHERE"]; ok {
		if w, ok := src.Expression.(clause.Where); ok && len(w.Exprs) > 0 {
			q = q.Clauses(clause.Where{Exprs: w.Exprs})
		}
	}
	if err := q.Find(rowsModel).Error; err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	rv := reflect.ValueOf(rowsModel).Elem()
	for i := 0; i < rv.Len(); i++ {
		row := rv.Index(i)
		id := primaryKeyString(db.Statement.Schema, row)
		out[id] = fieldValues(db.Statement.Schema, row)
	}
	return out, nil
}

// seenSet returns the dedup set in scope for the current operation. Inside a
// transaction it returns the transaction-shared set (populated by enter), so
// repeats across separate statements of one logical write are caught. Outside
// a transaction it falls back to a per-statement set stashed on Settings,
// which is sufficient because a single statement can't span associations.
func (c *callbacks) seenSet(db *gorm.DB) map[string]struct{} {
	if pool := txKey(db); pool != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		if st := c.txDedup[pool]; st != nil {
			return st.seen
		}
		// enter should have created it; be defensive if callback order ever
		// changes so we never nil-panic.
		st := &dedupState{depth: 1, seen: map[string]struct{}{}}
		c.txDedup[pool] = st
		return st.seen
	}
	return loadOrInitSeen(db)
}

// loadOrInitSeen returns the per-statement dedup set, creating and stashing
// it on the statement's Settings on first call. Used when not in a transaction.
func loadOrInitSeen(db *gorm.DB) map[string]struct{} {
	if v, ok := db.Statement.Settings.Load(seenRowsKey{}); ok {
		if m, ok := v.(map[string]struct{}); ok {
			return m
		}
	}
	m := map[string]struct{}{}
	db.Statement.Settings.Store(seenRowsKey{}, m)
	return m
}

// txKey returns a comparable key identifying the current transaction, or nil
// when the operation is not running inside one. GORM sets Statement.ConnPool to
// the *sql.Tx (which implements gorm.TxCommitter) while in a transaction and to
// the *sql.DB (which does not) otherwise; all nested association statements of
// one write inherit the same ConnPool, making it a stable per-transaction key.
func txKey(db *gorm.DB) any {
	if db.Statement == nil || db.Statement.ConnPool == nil {
		return nil
	}
	if _, ok := db.Statement.ConnPool.(gorm.TxCommitter); ok {
		return db.Statement.ConnPool
	}
	return nil
}

// iterateRows invokes fn for each struct in the statement's ReflectValue
// (which is either a single struct or a slice of structs).
func iterateRows(db *gorm.DB, fn func(reflect.Value)) {
	rv := db.Statement.ReflectValue
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			fn(rv.Index(i))
		}
	case reflect.Struct:
		fn(rv)
	case reflect.Ptr:
		if !rv.IsNil() {
			fn(rv.Elem())
		}
	}
}

// primaryKeyString renders the primary key of a row as a unambiguous string.
// Single-column PKs use their fmt.Sprint value directly; compound PKs are
// JSON-encoded as an array so that values containing the separator don't
// collide.
func primaryKeyString(s *schema.Schema, rv reflect.Value) string {
	if len(s.PrimaryFields) == 0 {
		return ""
	}
	rv = reflect.Indirect(rv)
	if len(s.PrimaryFields) == 1 {
		v, _ := s.PrimaryFields[0].ValueOf(nil, rv)
		return fmt.Sprintf("%v", v)
	}
	values := make([]any, 0, len(s.PrimaryFields))
	for _, f := range s.PrimaryFields {
		v, _ := f.ValueOf(nil, rv)
		values = append(values, v)
	}
	b, err := json.Marshal(values)
	if err != nil {
		// Fallback to separator join with escaped colons; should never hit
		// in practice since json.Marshal of basic types can't fail.
		return fmt.Sprintf("%v", values)
	}
	return string(b)
}

// pkIsZero reports whether every primary-key field of the row is the zero value
// (i.e. an auto-increment PK not yet assigned). A row with any non-zero PK is
// treated as already-persisted.
func pkIsZero(s *schema.Schema, rv reflect.Value) bool {
	if len(s.PrimaryFields) == 0 {
		return true
	}
	rv = reflect.Indirect(rv)
	for _, f := range s.PrimaryFields {
		if _, zero := f.ValueOf(nil, rv); !zero {
			return false
		}
	}
	return true
}

func fieldValues(s *schema.Schema, rv reflect.Value) map[string]any {
	rv = reflect.Indirect(rv)
	out := make(map[string]any, len(s.Fields))
	for _, f := range s.Fields {
		if f.DBName == "" {
			continue
		}
		v, zero := f.ValueOf(nil, rv)
		if zero {
			continue
		}
		out[f.DBName] = v
	}
	return out
}

// isSoftDelete reports whether the current delete operation is a GORM soft
// delete (UPDATE SET deleted_at) rather than a physical DELETE.
func isSoftDelete(db *gorm.DB) bool {
	if db.Statement.Schema == nil {
		return false
	}
	for _, f := range db.Statement.Schema.Fields {
		if f.DBName == "deleted_at" {
			// Check that the model's deleted_at field has been set (non-zero)
			// which indicates GORM performed a soft delete.
			rv := reflect.Indirect(db.Statement.ReflectValue)
			if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
				if rv.Len() > 0 {
					_, zero := f.ValueOf(nil, reflect.Indirect(rv.Index(0)))
					return !zero
				}
				return false
			}
			_, zero := f.ValueOf(nil, rv)
			return !zero
		}
	}
	return false
}

// softDeleteNewValues returns a map containing only the deleted_at field
// value, used as NewValues for soft-delete audit entries.
func softDeleteNewValues(s *schema.Schema, rv reflect.Value) map[string]any {
	rv = reflect.Indirect(rv)
	for _, f := range s.Fields {
		if f.DBName == "deleted_at" {
			v, zero := f.ValueOf(nil, rv)
			if !zero {
				return map[string]any{f.DBName: v}
			}
		}
	}
	return nil
}
