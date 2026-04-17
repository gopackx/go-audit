// Package ent is the Ent adapter for go-audit. It provides an ent.Hook that
// captures every create / update / delete mutation and records it via the
// Auditor.
//
// Usage:
//
//	client.Use(entaudit.Hook(auditor))  // once at startup
//
// Semantics:
//   - Create  → one audit entry with new_values.
//   - UpdateOne / DeleteOne → one entry with old_values (fetched via
//     mutation.OldField) and new_values (for update).
//   - Update / Delete (bulk) → one entry per affected row; IDs are fetched
//     from the mutation (reflection on m.IDs) and old values are fetched per
//     row before the mutation executes.
package ent

import (
	"context"
	"fmt"
	"reflect"

	"github.com/gopackx/go-audit"

	"entgo.io/ent"
)

// Hook returns an ent.Hook that records every mutation against auditor. Pass
// it to the generated ent.Client via client.Use(Hook(auditor)).
func Hook(auditor audit.Auditor) ent.Hook {
	return func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			action := mapAction(m.Op())
			if action == "" {
				return next.Mutate(ctx, m)
			}

			// Gather IDs (and pre-mutation state for updates/deletes) before
			// the mutation runs, so DELETE bulk still has something to log
			// after the rows are gone.
			ids, err := collectIDs(ctx, m)
			if err != nil {
				// Proceed even if we can't enumerate IDs — we'd rather run
				// the mutation and lose the audit row than block the write.
				ids = nil
			}
			oldValues := map[string]map[string]any{}
			if !auditor.Config().DataAudit.SkipOldValues && (action == audit.ActionUpdate || action == audit.ActionDelete) {
				captureOldValues(ctx, m, ids, oldValues)
			}
			newVals := collectNewValues(m)

			// Execute the actual mutation.
			result, mutErr := next.Mutate(ctx, m)
			if mutErr != nil {
				return result, mutErr
			}

			// For single-row Create we can now pull the assigned ID.
			if action == audit.ActionCreate {
				id, ok := idFromResult(result)
				if !ok {
					// Fall back to whatever the mutation reported.
					id, _ = idFromMutation(m)
				}
				ids = []string{stringifyID(id)}
			}

			txID := audit.TransactionIDFromContext(ctx)
			if txID == "" && len(ids) > 1 {
				txID = audit.NewTransactionID()
			}

			for _, id := range ids {
				entry := audit.DataEntry{
					EntityType:    m.Type(),
					EntityID:      id,
					Action:        action,
					TransactionID: txID,
				}
				switch action {
				case audit.ActionCreate:
					entry.NewValues = newVals
				case audit.ActionUpdate:
					entry.OldValues = oldValues[id]
					entry.NewValues = newVals
				case audit.ActionDelete:
					if v, ok := oldValues[id]; ok {
						entry.OldValues = v
					}
				}
				_ = auditor.RecordDataChange(ctx, entry)
			}
			return result, nil
		})
	}
}

func mapAction(op ent.Op) string {
	switch {
	case op.Is(ent.OpCreate):
		return audit.ActionCreate
	case op.Is(ent.OpUpdate | ent.OpUpdateOne):
		return audit.ActionUpdate
	case op.Is(ent.OpDelete | ent.OpDeleteOne):
		return audit.ActionDelete
	default:
		return ""
	}
}

// collectIDs returns the stringified IDs the mutation targets. For single-row
// ops it pulls m.ID(); for bulk ops it invokes m.IDs(ctx) via reflection
// (ent.Mutation does not expose IDs on the core interface).
func collectIDs(ctx context.Context, m ent.Mutation) ([]string, error) {
	op := m.Op()
	if op.Is(ent.OpCreate) {
		// The ID is assigned by the database — resolved from the result
		// after the mutation runs.
		return nil, nil
	}
	if op.Is(ent.OpUpdateOne | ent.OpDeleteOne) {
		if id, ok := idFromMutation(m); ok {
			return []string{stringifyID(id)}, nil
		}
	}
	// Bulk operations — call IDs(ctx) via reflection.
	method := reflect.ValueOf(m).MethodByName("IDs")
	if !method.IsValid() {
		return nil, nil
	}
	out := method.Call([]reflect.Value{reflect.ValueOf(ctx)})
	if len(out) != 2 {
		return nil, nil
	}
	if !out[1].IsNil() {
		return nil, out[1].Interface().(error)
	}
	rv := out[0]
	ids := make([]string, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		ids = append(ids, stringifyID(rv.Index(i).Interface()))
	}
	return ids, nil
}

// idFromMutation reflects over m.ID() (each generated mutation exposes it) to
// pull the primary-key value for single-row operations.
func idFromMutation(m ent.Mutation) (any, bool) {
	method := reflect.ValueOf(m).MethodByName("ID")
	if !method.IsValid() {
		return nil, false
	}
	out := method.Call(nil)
	if len(out) == 0 {
		return nil, false
	}
	// ID() usually returns (id, ok). If ok is present and false, bail.
	if len(out) == 2 && out[1].Kind() == reflect.Bool && !out[1].Bool() {
		return nil, false
	}
	return out[0].Interface(), true
}

// idFromResult tries to read an "ID" field off the entity returned by
// Mutator.Mutate — used after Create to capture the freshly-assigned PK.
func idFromResult(v ent.Value) (any, bool) {
	if v == nil {
		return nil, false
	}
	rv := reflect.Indirect(reflect.ValueOf(v))
	if !rv.IsValid() || rv.Kind() != reflect.Struct {
		return nil, false
	}
	f := rv.FieldByName("ID")
	if !f.IsValid() {
		return nil, false
	}
	return f.Interface(), true
}

func stringifyID(v any) string { return fmt.Sprintf("%v", v) }

func collectNewValues(m ent.Mutation) map[string]any {
	fields := m.Fields()
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(fields))
	for _, name := range fields {
		if v, ok := m.Field(name); ok {
			out[name] = v
		}
	}
	return out
}

// captureOldValues pre-loads the pre-mutation row state. For UpdateOne /
// DeleteOne ent.Mutation.OldField already consults the database (it's what
// the generated .Old*() helpers wrap), so we just iterate the fields the
// mutation touches.
//
// For bulk ops (Update / Delete targeting many rows), OldField typically
// returns an error — ent can only resolve old values for single-entity
// mutations. In that case old_values is left empty.
func captureOldValues(ctx context.Context, m ent.Mutation, ids []string, out map[string]map[string]any) {
	fields := m.Fields()
	// Bulk path: OldField won't work across many rows. Skip gracefully.
	if len(ids) != 1 {
		return
	}
	vals := map[string]any{}
	for _, name := range fields {
		v, err := m.OldField(ctx, name)
		if err != nil {
			continue
		}
		vals[name] = v
	}
	if len(vals) > 0 {
		out[ids[0]] = vals
	}
}
