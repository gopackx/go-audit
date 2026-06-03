package dialect

import "fmt"

// MySQLDialect generates MySQL-specific DDL and queries.
type MySQLDialect struct{}

func (MySQLDialect) Name() Type               { return MySQL }
func (MySQLDialect) Placeholder(n int) string { return "?" }

func (MySQLDialect) CreateDataTableSQL(table string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    id              BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    entity_type     VARCHAR(100) NOT NULL,
    entity_id       VARCHAR(100) NOT NULL,
    action          VARCHAR(20) NOT NULL,
    old_values      JSON,
    new_values      JSON,
    user_id         VARCHAR(100) NOT NULL,
    user_type       VARCHAR(50),
    metadata        JSON,
    transaction_id  VARCHAR(100),
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_%s_entity (entity_type, entity_id),
    INDEX idx_%s_user (user_id, created_at),
    INDEX idx_%s_action (action),
    INDEX idx_%s_created (created_at),
    INDEX idx_%s_transaction (transaction_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`, table, table, table, table, table, table)
}

func (MySQLDialect) CreateAPITableSQL(table string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    id                BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    service           VARCHAR(100) NOT NULL,
    endpoint          VARCHAR(500) NOT NULL,
    method            VARCHAR(10) NOT NULL,
    status_code       INT,
    request_headers   JSON,
    response_headers  JSON,
    request_body      JSON,
    response_body     JSON,
    duration_ms       INT,
    error_message     TEXT,
    user_id           VARCHAR(100),
    metadata          JSON,
    transaction_id    VARCHAR(100),
    created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_%s_service (service, created_at),
    INDEX idx_%s_status (status_code),
    INDEX idx_%s_user (user_id),
    INDEX idx_%s_created (created_at),
    INDEX idx_%s_transaction (transaction_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`, table, table, table, table, table, table)
}

// MySQL declares its indexes inline in the CREATE TABLE statement.
func (MySQLDialect) CreateIndexesSQL(table string) []string    { return nil }
func (MySQLDialect) CreateAPIIndexesSQL(table string) []string { return nil }
