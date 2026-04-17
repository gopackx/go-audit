package dialect

import "fmt"

// PostgresDialect generates PostgreSQL-specific DDL and queries.
type PostgresDialect struct{}

func (PostgresDialect) Name() Type               { return PostgreSQL }
func (PostgresDialect) Placeholder(n int) string { return fmt.Sprintf("$%d", n) }

func (PostgresDialect) CreateDataTableSQL(table string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    id              BIGSERIAL PRIMARY KEY,
    entity_type     VARCHAR(100) NOT NULL,
    entity_id       VARCHAR(100) NOT NULL,
    action          VARCHAR(20) NOT NULL,
    old_values      JSONB,
    new_values      JSONB,
    user_id         VARCHAR(100) NOT NULL,
    user_type       VARCHAR(50),
    metadata        JSONB,
    transaction_id  VARCHAR(100),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`, table)
}

func (PostgresDialect) CreateAPITableSQL(table string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    id                BIGSERIAL PRIMARY KEY,
    service           VARCHAR(100) NOT NULL,
    endpoint          VARCHAR(500) NOT NULL,
    method            VARCHAR(10) NOT NULL,
    status_code       INT,
    request_headers   JSONB,
    request_body      JSONB,
    response_body     JSONB,
    duration_ms       INT,
    error_message     TEXT,
    user_id           VARCHAR(100),
    metadata          JSONB,
    transaction_id    VARCHAR(100),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`, table)
}

func (PostgresDialect) CreateIndexesSQL(table string) []string {
	return []string{
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_entity ON %s (entity_type, entity_id)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_user ON %s (user_id, created_at)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_action ON %s (action)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_created ON %s (created_at)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_transaction ON %s (transaction_id)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_old_values ON %s USING GIN (old_values)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_new_values ON %s USING GIN (new_values)", table, table),
	}
}

func (PostgresDialect) CreateAPIIndexesSQL(table string) []string {
	return []string{
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_service ON %s (service, created_at)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_status ON %s (status_code)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_user ON %s (user_id)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_created ON %s (created_at)", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_transaction ON %s (transaction_id)", table, table),
	}
}
