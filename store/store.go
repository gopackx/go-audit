// Package store provides storage implementations for go-audit. The Store
// interface is defined here. Two built-in implementations are provided: a
// database/sql-backed store for production use and an in-memory store for
// testing. Persistent data types live in the entry subpackage.
package store

import (
	"context"
	"time"

	"github.com/gopackx/go-audit/entry"
)

// Store persists and retrieves audit records.
type Store interface {
	SaveDataLog(ctx context.Context, table string, log entry.AuditLog) error
	SaveAPILog(ctx context.Context, table string, log entry.AuditAPILog) error
	QueryDataLogs(ctx context.Context, table string, filter entry.DataFilter) ([]entry.AuditLog, error)
	QueryAPILogs(ctx context.Context, table string, filter entry.APIFilter) ([]entry.AuditAPILog, error)
	// Purge deletes rows from table whose created_at is strictly older than
	// before, returning the count removed. Used by retention tooling.
	Purge(ctx context.Context, table string, before time.Time) (int64, error)
	Exec(ctx context.Context, stmt string) error
}
