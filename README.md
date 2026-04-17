# go-audit

[![Go Reference](https://pkg.go.dev/badge/github.com/gopackx/go-audit.svg)](https://pkg.go.dev/github.com/gopackx/go-audit)
[![CI](https://github.com/gopackx/go-audit/actions/workflows/ci.yml/badge.svg)](https://github.com/gopackx/go-audit/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/gopackx/go-audit)](https://goreportcard.com/report/github.com/gopackx/go-audit)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Automated audit trail and API call logging for Go applications.

- **Zero external dependencies** in the core package — only the Go standard library.
- **Automatic data-change capture** via ORM adapters (GORM, Bun, Ent).
- **API call logging** with header / body redaction and size truncation.
- **Cross-concern correlation** — data changes and API calls share a `transaction_id`.
- **Multi-database** — PostgreSQL, MySQL, SQLite (dialect-aware DDL & placeholders).

## Install

```bash
go get github.com/gopackx/go-audit
go get github.com/gopackx/go-audit/adapters/gorm  # optional, for GORM users
go get github.com/gopackx/go-audit/adapters/bun   # optional, for Bun users
go get github.com/gopackx/go-audit/adapters/ent   # optional, for Ent users
```

## Quick start (GORM + SQLite)

```go
import (
    "github.com/gopackx/go-audit"
    auditgorm "github.com/gopackx/go-audit/adapters/gorm"
)

sqlDB, _ := gormDB.DB()

auditor, _ := audit.New(sqlDB, audit.Config{
    Dialect:  audit.SQLite,
    UserFunc: func(ctx context.Context) (string, string) {
        return ctx.Value("user_id").(string), "user"
    },
    DataAudit: audit.DataAuditConfig{
        Enabled:       true,
        ExcludeFields: []string{"password", "remember_token"},
    },
    APIAudit: audit.APIAuditConfig{
        Enabled:          true,
        RedactHeaders:    []string{"Authorization", "X-API-Key"},
        RedactBodyFields: []string{"password", "secret", "token"},
        MaxBodySize:      4096,
    },
})

_ = auditor.AutoMigrate(ctx)
_ = gormDB.Use(auditgorm.Plugin(auditor))
```

After `db.Use(auditgorm.Plugin(auditor))` every create / update / delete through
that `*gorm.DB` is audited automatically — no manual `Record()` calls.

## API call logging

```go
start := time.Now()
resp, err := client.Transfer(ctx, req)

_ = auditor.API().Record(ctx, audit.APIEntry{
    Service:      "bca",
    Endpoint:     "/api/v1/transfer",
    Method:       "POST",
    StatusCode:   resp.StatusCode,
    RequestBody:  req,
    ResponseBody: resp.Body,
    DurationMs:   int(time.Since(start).Milliseconds()),
})
```

## Transaction correlation

```go
txID := audit.NewTransactionID()
ctx = audit.WithTransactionID(ctx, txID)

// API call + data change share txID
_, _ = client.Transfer(ctx, req)
_ = auditor.API().Record(ctx, audit.APIEntry{ /* ... */ })
db.WithContext(ctx).Save(&tx)

logs, _ := auditor.QueryByTransaction(ctx, txID)
// logs.DataLogs, logs.APILogs
```

## Querying

```go
logs, _ := auditor.Query(ctx, audit.DataFilter{
    EntityType: "products",
    EntityID:   "42",
    Action:     audit.ActionUpdate,
    DateFrom:   time.Now().AddDate(0, -1, 0),
    Limit:      50,
})

apiLogs, _ := auditor.API().Query(ctx, audit.APIFilter{
    Service:    "bca",
    StatusCode: 500,
    Limit:      100,
})
```

## Bun adapter

```go
import (
    "github.com/gopackx/go-audit"
    auditbun "github.com/gopackx/go-audit/adapters/bun"
)

auditbun.Register(bunDB, auditor)

// Every insert / update / delete through bunDB is audited automatically.
_, _ = bunDB.NewInsert().Model(&product).Exec(ctx)
_, _ = bunDB.NewUpdate().Model(&product).WherePK().Exec(ctx)
_, _ = bunDB.NewDelete().Model(&product).WherePK().Exec(ctx)
```

Old values are captured via a pre-query SELECT, so UPDATE and DELETE logs
carry both `old_values` and `new_values` (diffed automatically, matching the
GORM adapter). The snapshot strategy is: (1) SELECT by primary key when the
caller's Model has PKs set (`WherePK()` / `Bulk()`, compound PKs supported),
otherwise (2) extract the WHERE clause from the rendered SQL and run the
equivalent `SELECT * FROM <table> WHERE <same>` — so custom-predicate bulk
updates / deletes still get old_values. Set `DataAuditConfig.SkipOldValues =
true` to skip the snapshot SELECT entirely.

The snapshot SELECT runs against the base `*bun.DB`, so uncommitted writes
inside an active `*bun.Tx` are not visible. For strict tx-local snapshots,
load rows explicitly before the write.

## Ent adapter

```go
import (
    "github.com/gopackx/go-audit"
    entaudit "github.com/gopackx/go-audit/adapters/ent"
)

client.Use(entaudit.Hook(auditor)) // once at startup
```

Every create / update / delete mutation — single-row and bulk — is audited
automatically. Single-row updates / deletes capture `old_values` via
`ent.Mutation.OldField`; bulk updates / deletes log affected IDs (fetched
from the mutation) with new_values. As with the other adapters,
`DataAuditConfig.SkipOldValues = true` skips the pre-mutation fetch.

## Soft deletes (GORM)

Models with `gorm.DeletedAt` are detected automatically. Soft deletes are
recorded as `action: "soft_delete"` with `new_values` containing the
`deleted_at` timestamp, while hard deletes remain `action: "delete"` with
`new_values: null`.

```go
type Product struct {
    ID        uint           `gorm:"primaryKey"`
    Name      string
    DeletedAt gorm.DeletedAt `gorm:"index"`  // enables soft delete
}

db.Delete(&product)             // → action: "soft_delete"
db.Unscoped().Delete(&product)  // → action: "delete"
```

## Snapshot & Restore

Reconstruct entity state at any point in time, or revert to a previous state.

```go
// Snapshot: what did Product#42 look like yesterday?
state, err := auditor.Snapshot(ctx, "products", "42", yesterday)
// state = map[string]any{"name": "Widget", "price": 100, ...}
// Returns nil if the entity didn't exist or was deleted at that time.

// Restore: revert Product#42 to yesterday's state.
// Records a "restore" audit entry and returns the target values.
result, err := auditor.Restore(ctx, "products", "42", yesterday)
// result.Values = map[string]any{"name": "Widget", "price": 100}
// result.WasDeleted = false

// Apply the restored values via your ORM:
db.Model(&product).Where("id = ?", 42).Updates(result.Values)
```

`Restore` records an audit entry with `action: "restore"`, capturing both the
current state (old_values) and the target state (new_values) for full
traceability.

## Retention

```go
res, err := auditor.Purge(ctx, time.Now().AddDate(0, 0, -90))
// res.DataLogs and res.APILogs — rows deleted per table
```

## Configuration

```go
type Config struct {
    Dialect   audit.DialectType  // PostgreSQL | MySQL | SQLite
    UserFunc  audit.UserFunc     // extracts (userID, userType) from ctx
    DataAudit audit.DataAuditConfig
    APIAudit  audit.APIAuditConfig
}
```

`DataAuditConfig.ExcludeFields` drops sensitive columns from both `old_values`
and `new_values` before they are persisted. `APIAuditConfig.MaxBodySize`
truncates request / response bodies above the limit (default 4 KiB).

## Schema

Two tables, both auto-created by `AutoMigrate()`:

- **`audit_logs`** — one row per data change (entity_type, entity_id, action,
  old_values, new_values, user_id, transaction_id, …).
- **`audit_api_logs`** — one row per third-party API call (service, endpoint,
  method, status_code, request/response headers + body, duration_ms, …).

Table names are configurable via `DataAudit.Table` / `APIAudit.Table`.

## Layout

```
github.com/gopackx/go-audit                     core package, zero external deps
github.com/gopackx/go-audit/adapters/gorm       GORM plugin (requires gorm.io/gorm)
github.com/gopackx/go-audit/adapters/bun        Bun QueryHook (requires uptrace/bun)
github.com/gopackx/go-audit/adapters/ent        Ent Hook (requires entgo.io/ent)
github.com/gopackx/go-audit/integration         integration tests (requires a driver)
github.com/gopackx/go-audit/_examples/...       runnable examples
```

## Examples

- [`_examples/gorm-basic`](_examples/gorm-basic) — GORM + SQLite end-to-end run.
- [`_examples/bun-basic`](_examples/bun-basic) — Bun + SQLite end-to-end run.
- [`_examples/api-logging`](_examples/api-logging) — third-party API call
  auditing with redaction and transaction correlation against data changes.
- [`_examples/full-example`](_examples/full-example) — complete demo: GORM CRUD,
  API logging, soft deletes, transaction correlation, querying, and retention.

## Status

Shipped:

- Core package (zero external deps): interfaces, config, diff engine,
  transaction helpers.
- Dialects: PostgreSQL, MySQL, SQLite (DDL, indexes, placeholders).
- Stores: `database/sql` (default) + in-memory (for tests).
- Migration (`AutoMigrate()`).
- API audit with header / body redaction and size truncation.
- GORM adapter (`adapters/gorm/`) with automatic create / update / delete
  capture, diff, and batch grouping under one `transaction_id`.
- Bun adapter (`adapters/bun/`) with automatic create / update / delete
  capture via QueryHook, including pre-query snapshot for both PK-based and
  custom-predicate writes.
- Ent adapter (`adapters/ent/`) with automatic create / update / delete
  capture via ent.Hook, single-row + bulk.
- Retention: `auditor.Purge(ctx, before)` deletes rows older than a cutoff
  from both audit tables.
- Snapshot & Restore: reconstruct entity state at any point in time, revert
  with full audit trail.
- Soft delete support (GORM): detected automatically, recorded as
  `action: "soft_delete"`.
- Integration tests against SQLite; opt-in Postgres / MySQL via DSN env vars.
- CI (GitHub Actions) for core + adapter tests across SQLite / Postgres / MySQL.
- Benchmarks for core hot paths (diff, record, API audit, transaction ID).

## License

MIT
