// Package audit provides automated audit trail and API call logging for Go
// applications with adapter-based support for multiple ORMs and databases.
//
// The package is split across several subpackages:
//
//   - dialect/  generates database-specific SQL (Postgres, MySQL, SQLite).
//   - store/    persists and queries audit records.
//   - entry/    holds the persistent data types (AuditLog, AuditAPILog).
//   - migrate/  creates audit tables and indexes.
//   - adapters/ ORM-specific integrations (GORM, Bun, Ent).
//
// Most callers only need to import this root package; the relevant types
// from the subpackages are re-exported here via type aliases.
package audit

import (
	"context"
	"time"
)

// Action constants for data-change audit logs.
const (
	ActionCreate     = "create"
	ActionUpdate     = "update"
	ActionDelete     = "delete"
	ActionSoftDelete = "soft_delete"
	ActionRestore    = "restore"
)

// Auditor is the main interface used by adapters and callers.
type Auditor interface {
	RecordDataChange(ctx context.Context, entry DataEntry) error
	API() APIAuditor
	Query(ctx context.Context, filter DataFilter) ([]AuditLog, error)
	QueryByTransaction(ctx context.Context, txID string) (*TransactionLog, error)
	AutoMigrate(ctx context.Context) error
	// Purge deletes data and API audit rows older than before. Returns per-
	// table counts; either table may be skipped when its config is disabled.
	Purge(ctx context.Context, before time.Time) (PurgeResult, error)
	// Snapshot reconstructs the state of an entity at a point in time by
	// replaying all audit logs up to `at`. Returns nil if the entity did not
	// exist (or was deleted) at that time.
	Snapshot(ctx context.Context, entityType, entityID string, at time.Time) (map[string]any, error)
	// Restore reconstructs entity state at time `at` via Snapshot, records a
	// "restore" audit entry (old=current state, new=target state), and returns
	// the target values. The caller is responsible for applying the returned
	// values via their ORM; the ORM adapter will not double-record this change
	// because the restore entry is already written here.
	Restore(ctx context.Context, entityType, entityID string, at time.Time) (*RestoreResult, error)
	Config() Config
}

// APIAuditor handles third-party API call logging.
type APIAuditor interface {
	Record(ctx context.Context, entry APIEntry) error
	Query(ctx context.Context, filter APIFilter) ([]AuditAPILog, error)
}

// Adapter is implemented by ORM-specific integration packages.
type Adapter interface {
	Register(auditor Auditor) error
}
