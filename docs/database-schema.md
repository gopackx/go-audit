# Database Schema Reference

go-audit creates two tables: one for data change logs and one for API call logs. Both are auto-created by `auditor.AutoMigrate(ctx)`. Table names are customizable.

## audit_logs (Data Changes)

### PostgreSQL

```sql
CREATE TABLE audit_logs (
    id              BIGSERIAL PRIMARY KEY,
    entity_type     VARCHAR(100) NOT NULL,
    entity_id       VARCHAR(100) NOT NULL,
    action          VARCHAR(20) NOT NULL,       -- create, update, delete, soft_delete, restore
    old_values      JSONB,
    new_values      JSONB,
    user_id         VARCHAR(100) NOT NULL,
    user_type       VARCHAR(50),
    metadata        JSONB,
    transaction_id  VARCHAR(100),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_entity ON audit_logs (entity_type, entity_id);
CREATE INDEX idx_audit_user ON audit_logs (user_id, created_at);
CREATE INDEX idx_audit_action ON audit_logs (action);
CREATE INDEX idx_audit_created ON audit_logs (created_at);
CREATE INDEX idx_audit_transaction ON audit_logs (transaction_id);
CREATE INDEX idx_audit_old_values ON audit_logs USING GIN (old_values);
CREATE INDEX idx_audit_new_values ON audit_logs USING GIN (new_values);
```

### MySQL

```sql
CREATE TABLE audit_logs (
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

    INDEX idx_audit_entity (entity_type, entity_id),
    INDEX idx_audit_user (user_id, created_at),
    INDEX idx_audit_action (action),
    INDEX idx_audit_created (created_at),
    INDEX idx_audit_transaction (transaction_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### SQLite

```sql
CREATE TABLE audit_logs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type     TEXT NOT NULL,
    entity_id       TEXT NOT NULL,
    action          TEXT NOT NULL,
    old_values      TEXT,           -- JSON stored as TEXT
    new_values      TEXT,
    user_id         TEXT NOT NULL,
    user_type       TEXT,
    metadata        TEXT,
    transaction_id  TEXT,
    created_at      DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_audit_entity ON audit_logs (entity_type, entity_id);
CREATE INDEX idx_audit_user ON audit_logs (user_id, created_at);
CREATE INDEX idx_audit_action ON audit_logs (action);
CREATE INDEX idx_audit_created ON audit_logs (created_at);
CREATE INDEX idx_audit_transaction ON audit_logs (transaction_id);
```

## audit_api_logs (API Calls)

### PostgreSQL

```sql
CREATE TABLE audit_api_logs (
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
);

CREATE INDEX idx_api_audit_service ON audit_api_logs (service, created_at);
CREATE INDEX idx_api_audit_status ON audit_api_logs (status_code);
CREATE INDEX idx_api_audit_user ON audit_api_logs (user_id);
CREATE INDEX idx_api_audit_created ON audit_api_logs (created_at);
CREATE INDEX idx_api_audit_transaction ON audit_api_logs (transaction_id);
```

### MySQL

```sql
CREATE TABLE audit_api_logs (
    id                BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    service           VARCHAR(100) NOT NULL,
    endpoint          VARCHAR(500) NOT NULL,
    method            VARCHAR(10) NOT NULL,
    status_code       INT,
    request_headers   JSON,
    request_body      JSON,
    response_body     JSON,
    duration_ms       INT,
    error_message     TEXT,
    user_id           VARCHAR(100),
    metadata          JSON,
    transaction_id    VARCHAR(100),
    created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    INDEX idx_api_audit_service (service, created_at),
    INDEX idx_api_audit_status (status_code),
    INDEX idx_api_audit_user (user_id),
    INDEX idx_api_audit_created (created_at),
    INDEX idx_api_audit_transaction (transaction_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### SQLite

```sql
CREATE TABLE audit_api_logs (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    service           TEXT NOT NULL,
    endpoint          TEXT NOT NULL,
    method            TEXT NOT NULL,
    status_code       INTEGER,
    request_headers   TEXT,
    request_body      TEXT,
    response_body     TEXT,
    duration_ms       INTEGER,
    error_message     TEXT,
    user_id           TEXT,
    metadata          TEXT,
    transaction_id    TEXT,
    created_at        DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_api_audit_service ON audit_api_logs (service, created_at);
CREATE INDEX idx_api_audit_status ON audit_api_logs (status_code);
CREATE INDEX idx_api_audit_user ON audit_api_logs (user_id);
CREATE INDEX idx_api_audit_created ON audit_api_logs (created_at);
CREATE INDEX idx_api_audit_transaction ON audit_api_logs (transaction_id);
```

## Index Strategy

### PostgreSQL
- **GIN indexes** on `old_values` and `new_values` JSONB columns enable fast queries like "find all changes where field X was modified"
- All other indexes are standard B-tree

### MySQL
- Indexes are declared inline in the `CREATE TABLE` statement
- No JSON-specific indexes (MySQL's JSON indexing requires generated columns)

### SQLite
- Standard B-tree indexes created separately via `CREATE INDEX`
- JSON stored as TEXT; use `json_extract()` for querying JSON fields

## Custom Table Names

```go
audit.Config{
    DataAudit: audit.DataAuditConfig{
        Table: "my_audit_logs",      // default: "audit_logs"
    },
    APIAudit: audit.APIAuditConfig{
        Table: "my_api_audit_logs",  // default: "audit_api_logs"
    },
}
```

Table names must match `^[A-Za-z_][A-Za-z0-9_]{0,62}$` (valid SQL identifiers).

## Column Details

### entity_id

Stored as `VARCHAR(100)` / `TEXT`. For compound primary keys (e.g., GORM composite PKs), the adapter serializes them as JSON: `["us-east-1","user-42"]`.

### old_values / new_values

For `update` actions, these contain **only the changed fields**, not the full row. This keeps storage compact.

| Action | old_values | new_values |
|--------|------------|------------|
| `create` | `NULL` | All fields |
| `update` | Changed fields (old) | Changed fields (new) |
| `delete` | All fields | `NULL` |
| `soft_delete` | All fields | `{"deleted_at": "..."}` |
| `restore` | Current state | Target state |

### transaction_id

Format: `YYYYMMDDTHHmmss-<128-bit-random-hex>` (e.g., `20260417T103045-a1b2c3d4e5f6a1b2...`).

The timestamp prefix makes transaction IDs naturally sortable. The random suffix ensures uniqueness.
