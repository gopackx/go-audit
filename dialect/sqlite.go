package dialect

import "fmt"

// SQLiteDialect generates SQLite-specific DDL and queries.
type SQLiteDialect struct{}

func (SQLiteDialect) Name() Type               { return SQLite }
func (SQLiteDialect) Placeholder(n int) string { return "?" }

func (SQLiteDialect) CreateDataTableSQL(table string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type     TEXT NOT NULL,
    entity_id       TEXT NOT NULL,
    action          TEXT NOT NULL,
    old_values      TEXT,
    new_values      TEXT,
    user_id         TEXT NOT NULL,
    user_type       TEXT,
    metadata        TEXT,
    transaction_id  TEXT,
    created_at      DATETIME NOT NULL DEFAULT (datetime('now'))
)`, table)
}

func (SQLiteDialect) CreateAPITableSQL(table string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    service           TEXT NOT NULL,
    endpoint          TEXT NOT NULL,
    method            TEXT NOT NULL,
    status_code       INTEGER,
    request_headers   TEXT,
    response_headers  TEXT,
    request_body      TEXT,
    response_body     TEXT,
    duration_ms       INTEGER,
    error_message     TEXT,
    user_id           TEXT,
    metadata          TEXT,
    transaction_id    TEXT,
    created_at        DATETIME NOT NULL DEFAULT (datetime('now'))
)`, table)
}

func (SQLiteDialect) CreateIndexesSQL(table string) []string {
	return []string{
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_entity ON %s (entity_type, entity_id)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_user ON %s (user_id, created_at)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_action ON %s (action)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_created ON %s (created_at)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_transaction ON %s (transaction_id)", table, table),
	}
}

func (SQLiteDialect) CreateAPIIndexesSQL(table string) []string {
	return []string{
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_service ON %s (service, created_at)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_status ON %s (status_code)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_user ON %s (user_id)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_created ON %s (created_at)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_transaction ON %s (transaction_id)", table, table),
	}
}
