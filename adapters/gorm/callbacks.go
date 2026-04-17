package gorm

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/gopackx/go-audit"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// oldValuesKey is the typed key used to stash pre-change row snapshots on a
// GORM statement's Settings map. Using a dedicated struct type avoids string
// collisions with other plugins.
type oldValuesKey struct{}

type callbacks struct {
	auditor audit.Auditor
}

func newCallbacks(a audit.Auditor) *callbacks { return &callbacks{auditor: a} }

func (c *callbacks) afterCreate(db *gorm.DB) {
	c.recordEach(db, audit.ActionCreate, nil)
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

	iterateRows(db, func(rv reflect.Value) {
		entityID := primaryKeyString(db.Statement.Schema, rv)
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
	if len(db.Statement.Clauses) > 0 {
		for _, clause := range []string{"WHERE"} {
			if c, ok := db.Statement.Clauses[clause]; ok {
				q = q.Clauses(c)
			}
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
