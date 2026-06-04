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

> **Adapter versions.** Adapter sub-modules are not (yet) released under
> their own semver tags, so `go get .../adapters/gorm@v1.0.0` will fail.
> Pin to the latest commit on `master` via a pseudo-version instead:
>
> ```bash
> go get github.com/gopackx/go-audit/adapters/gorm@master
> # go.mod will rewrite this to e.g. v0.0.0-20260101120000-abcdef012345
> ```

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
        Enabled:         true,
        ExcludeFields:   []string{"password", "remember_token"},
        ExcludeEntities: []string{"sessions", "audit_logs"}, // skip whole tables
        SkipOldValues:   false,                              // see below
        OnError:         audit.ErrorFailLoud,                // per-table, see below
    },
    APIAudit: audit.APIAuditConfig{
        Enabled:          true,
        RedactHeaders:    []string{"Authorization", "X-API-Key"},
        RedactBodyFields: []string{"password", "secret", "token"},
        MaxBodySize:      4096,
        OnError:          audit.ErrorFailLoud, // independent from DataAudit
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
    Service:         "bca",
    Endpoint:        "/api/v1/transfer",
    Method:          "POST",
    StatusCode:      resp.StatusCode,
    RequestHeaders:  reqHeaders,            // map[string]string
    ResponseHeaders: respHeaders,           // map[string]string, redacted alongside RequestHeaders
    RequestBody:     req,
    ResponseBody:    resp.Body,
    DurationMs:      int(time.Since(start).Milliseconds()),
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

### Data audit knobs

| Field | Purpose |
| --- | --- |
| `ExcludeFields` | Column names dropped from both `old_values` and `new_values` (e.g. `password`, `remember_token`). |
| `ExcludeEntities` | Entire tables to skip — useful for high-volume housekeeping tables (`sessions`, `jobs`) or to avoid recursively auditing the audit table itself. |
| `SkipOldValues` | When `true` the adapter skips the pre-write `SELECT` snapshot used to populate `old_values` on UPDATE/DELETE. Trades audit completeness for one fewer round-trip per write. Same flag works for GORM, Bun and Ent. |
| `OnError` | Per-table error policy — `ErrorFailLoud` (default) surfaces store failures back to the caller; `ErrorFailSilent` logs and swallows them. The `APIAudit` block has its own independent `OnError`, so you can fail-loud on data changes but fail-silent on API logging (or vice versa). |

### API audit knobs

`APIAuditConfig.MaxBodySize` truncates request / response bodies above the
limit (default 4 KiB) into a `{ "_truncated": true, "original_size": N,
"preview": "..." }` envelope so the column stays valid JSON.

`RedactBodyFields` works on JSON-shaped payloads. If you hand the auditor a
struct or pointer, go-audit round-trips it through `encoding/json` before
walking the keys, so JSON tag names — not Go field names — are what the
redaction list must match. Anything that can't be JSON-marshalled is left
as-is.

### Skipping the GORM plugin for a single call

Sometimes you need to write through the same `*gorm.DB` without producing an
audit row (bulk back-fills, migrations, or to avoid recursively auditing the
audit table itself). Use GORM's standard `SkipHooks` session option — the
adapter's callbacks are registered as ordinary GORM callbacks, so they
honour it automatically:

```go
db.Session(&gorm.Session{SkipHooks: true}).Create(&bulkRows)
```

Pair this with `DataAudit.ExcludeEntities` when you want the bypass to be
permanent for a table.

### Production tip: dedicated `*sql.DB` for the audit store

`audit.New(db, ...)` never opens or closes the connection — it borrows the
`*sql.DB` you supply. In production we recommend giving the auditor its
**own** `*sql.DB` (separate pool, often a separate user / database) rather
than reusing the application pool. That way:

* Audit writes don't compete with hot application queries for connections.
* You can tighten `SetMaxOpenConns` / `SetConnMaxLifetime` to the audit
  workload independently.
* Granting the audit user `INSERT`-only on the audit tables limits blast
  radius if the app is compromised.
* Pointing the audit DB at a separate database or replica makes the
  retention / purge job operationally independent from the main schema.

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

## AI integration

go-audit ships with first-class support for AI coding assistants:

- **Claude Skill** — copy [`skills/go-audit/SKILL.md`](skills/go-audit/SKILL.md)
  into your project's `.claude/skills/go-audit/` (or
  `~/.claude/skills/go-audit/`) and Claude Code will know how to
  integrate go-audit correctly from a cold prompt ("set up an audit
  trail for this GORM project").
- **MCP server** — [`mcp/`](mcp/) is a Model Context Protocol server
  that exposes the query API (`Query`, `QueryByTransaction`,
  `Snapshot`, …) as tools. Wire it into Claude Code / Claude.ai /
  Cursor to investigate audit history conversationally:
  > "Show me every change to order #42 in the last 24 hours."
  > "What did that user look like at 09:00 yesterday?"

See [`mcp/README.md`](mcp/README.md) for install + configuration.

## License

MIT
