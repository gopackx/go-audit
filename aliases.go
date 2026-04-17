package audit

import (
	"database/sql"

	"github.com/gopackx/go-audit/dialect"
	"github.com/gopackx/go-audit/entry"
	"github.com/gopackx/go-audit/store"
)

// DialectType identifies the SQL dialect used by the underlying database.
// Type alias for dialect.Type so callers can use audit.PostgreSQL etc.
// without importing the dialect subpackage directly.
type DialectType = dialect.Type

const (
	PostgreSQL = dialect.PostgreSQL
	MySQL      = dialect.MySQL
	SQLite     = dialect.SQLite
)

// Dialect generates database-specific SQL. Type alias for dialect.Dialect.
type Dialect = dialect.Dialect

// Store persists and retrieves audit records. Type alias for store.Store.
type Store = store.Store

// Persistent data types re-exported from the entry subpackage.
type (
	AuditLog       = entry.AuditLog
	AuditAPILog    = entry.AuditAPILog
	DataFilter     = entry.DataFilter
	APIFilter      = entry.APIFilter
	TransactionLog = entry.TransactionLog
)

// RegisterDialect adds or replaces a Dialect implementation.
func RegisterDialect(t DialectType, d Dialect) { dialect.Register(t, d) }

// DialectFor resolves a DialectType to its registered implementation.
func DialectFor(t DialectType) Dialect { return dialect.For(t) }

// NewSQLStore returns a Store backed by database/sql using the supplied
// dialect for parameter placeholders.
func NewSQLStore(db *sql.DB, d Dialect) Store {
	return store.NewSQLStore(db, d)
}

// NewMemoryStore returns an in-memory Store, intended for tests.
func NewMemoryStore() Store {
	return store.NewMemoryStore()
}
