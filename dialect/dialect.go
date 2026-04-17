// Package dialect provides database-specific SQL generation for go-audit.
// Built-in dialects cover PostgreSQL, MySQL, and SQLite. Custom dialects can
// be registered via Register.
package dialect

import "sync"

// Type identifies the SQL dialect used by the underlying database.
type Type string

const (
	PostgreSQL Type = "postgres"
	MySQL      Type = "mysql"
	SQLite     Type = "sqlite"
)

// Dialect generates database-specific SQL.
type Dialect interface {
	Name() Type
	CreateDataTableSQL(table string) string
	CreateAPITableSQL(table string) string
	CreateIndexesSQL(table string) []string
	CreateAPIIndexesSQL(table string) []string
	Placeholder(n int) string
}

// dialectRegistry holds the built-in and user-registered Dialect
// implementations. A sync.RWMutex keeps Register safe if users call it
// during init across goroutines.
var (
	dialectMu sync.RWMutex
	dialects  = map[Type]Dialect{
		PostgreSQL: PostgresDialect{},
		MySQL:      MySQLDialect{},
		SQLite:     SQLiteDialect{},
	}
)

// Register adds or replaces a Dialect implementation for the given Type.
// Intended for extending go-audit to databases beyond the built-in
// PostgreSQL / MySQL / SQLite support without forking.
func Register(t Type, d Dialect) {
	if d == nil {
		return
	}
	dialectMu.Lock()
	dialects[t] = d
	dialectMu.Unlock()
}

// For resolves a Type to its registered implementation. Returns nil if no
// dialect is registered under t.
func For(t Type) Dialect {
	dialectMu.RLock()
	defer dialectMu.RUnlock()
	return dialects[t]
}
