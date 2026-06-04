---
name: go-audit
description: Use this skill when integrating gopackx/go-audit (a Go library for automated audit-trail and outbound API call logging) into a Go project. Triggers include any of these in a Go context — adding an "audit log" / "audit trail" / "change tracking" / "who changed X" / "compliance log"; setting up automatic CREATE/UPDATE/DELETE tracking with GORM, Bun, or Ent; configuring redaction of sensitive fields (password, card_number, tokens) in audit records or outbound API logs; building point-in-time entity reconstruction (Snapshot) or revert (Restore); correlating data changes with third-party API calls via transaction_id; querying historical changes. Use also for upgrading an existing go-audit deployment between versions. Do NOT use for: general application logging (use log/slog), HTTP access logs (use gin/chi middleware), non-Go projects, or audit features in frameworks like Laravel/Django.
---

# go-audit integration skill

`github.com/gopackx/go-audit` is a Go library that auto-captures data
changes through ORM adapters and records outbound API calls with
redaction. The core has **zero external dependencies** (only Go
stdlib); ORM support is opt-in via three separate adapter modules.

This skill walks you through integrating it correctly the first time
and lists the pitfalls that cause most early issues.

---

## Step 0 — Does the user actually want go-audit?

Confirm fit before suggesting it:

✅ **Good fit**
- Writing a Go service (any framework) that needs an audit trail for compliance, forensics, or "who changed X" answers.
- Already using GORM, Bun, or Ent. (Adapter does the work.)
- Wants outbound API call logging (payment gateways, SMS providers, etc.) with header/body redaction.
- Needs cross-concern correlation: "show me every data change and API call inside one business transaction."

❌ **Not the right tool**
- They want general application logging → recommend `log/slog`.
- They want HTTP request access logs → recommend gin/chi middleware.
- They don't use GORM/Bun/Ent and don't want to call `RecordDataChange` manually → walk away or volunteer to write the manual recording path.
- Non-Go project → walk away.

---

## Step 1 — Install

Recommend pinning to `v1.1.0` or later (earlier releases have a
GORM `WHERE WHERE` bug that cancels writes — see "Common pitfalls"
below).

```bash
go get github.com/gopackx/go-audit@v1.1.0
```

Then **one** of the adapters depending on the user's ORM:

```bash
# pick exactly one
go get github.com/gopackx/go-audit/adapters/gorm@v1.1.0
go get github.com/gopackx/go-audit/adapters/bun@v1.1.0
go get github.com/gopackx/go-audit/adapters/ent@v1.1.0
```

If an adapter tag doesn't resolve yet (release timing), fall back to
`@master` — Go rewrites it to a pseudo-version.

The core depends only on stdlib. The user keeps their existing
`*sql.DB` and ORM client; go-audit never opens its own connection.

---

## Step 2 — Build the `Config`

Always set these four:

```go
import (
    "context"
    "database/sql"

    "github.com/gopackx/go-audit"
)

auditor, err := audit.New(sqlDB, audit.Config{
    Dialect:  audit.PostgreSQL, // or audit.MySQL / audit.SQLite — never auto-detected
    UserFunc: func(ctx context.Context) (userID, userType string) {
        // Pull whatever identifier the app uses for the actor.
        // Returning ("", "") is fine for system writes.
        if v := ctx.Value(userIDKey{}); v != nil {
            return v.(string), "user"
        }
        return "", ""
    },
    DataAudit: audit.DataAuditConfig{
        Enabled:       true,
        ExcludeFields: []string{"password", "remember_token"},
    },
    APIAudit: audit.APIAuditConfig{
        Enabled:          true,
        RedactHeaders:    []string{"Authorization", "X-API-Key", "Cookie"},
        RedactBodyFields: []string{"password", "card_number", "cvv", "secret", "token"},
        MaxBodySize:      4096, // bytes; bodies above this get truncated into a JSON envelope
    },
})
if err != nil {
    return err
}
```

Common mistakes here:

- **Forgetting `UserFunc` when `DataAudit.Enabled = true`** — `audit.New` returns a validation error. If the user only wants API logging, set `DataAudit.Enabled = false` and skip `UserFunc`.
- **Setting `Dialect` to a string** — use the constants `audit.PostgreSQL` / `audit.MySQL` / `audit.SQLite`.
- **Reusing the app's hot `*sql.DB`** — works, but in production recommend a dedicated `*sql.DB` for the audit pool (separate pool sizing, optionally a separate DB user with `INSERT`-only on the audit tables).

### Optional fields to mention only if relevant

- `DataAudit.ExcludeEntities []string` — skip whole tables (e.g. `sessions`, the audit table itself if the user stores it in the same DB).
- `DataAudit.SkipOldValues bool` — disables the pre-write `SELECT` snapshot. Faster, but `old_values` is empty on UPDATE/DELETE. Trade only if performance is measured.
- `DataAudit.OnError` and `APIAudit.OnError` — independent per-table policy: `audit.ErrorFailLoud` (default; bubbles store errors back to the caller) vs `audit.ErrorFailSilent` (logs via `Config.Logger` and swallows). Use `ErrorFailSilent` for the API audit table when the app must keep running during audit-DB outages.

---

## Step 3 — Create the schema

```go
if err := auditor.AutoMigrate(ctx); err != nil {
    return err
}
```

Creates `audit_logs` (data changes) and/or `audit_api_logs` (API
calls) depending on which is enabled. Uses `CREATE TABLE IF NOT
EXISTS`, so it's safe to call on every boot.

**Custom table names**: set `DataAudit.Table` / `APIAudit.Table`. Names
must match `^[A-Za-z_][A-Za-z0-9_]{0,62}$` (validated to prevent SQL
injection through identifier interpolation).

---

## Step 4 — Register the adapter

### GORM

```go
import auditgorm "github.com/gopackx/go-audit/adapters/gorm"

if err := gormDB.Use(auditgorm.Plugin(auditor)); err != nil {
    return err
}
```

After this, every `Create / Updates / Save / Delete` through `gormDB`
produces an audit row automatically. No manual `Record` calls.

### Bun

```go
import auditbun "github.com/gopackx/go-audit/adapters/bun"

auditbun.Register(bunDB, auditor)
```

### Ent

```go
import entaudit "github.com/gopackx/go-audit/adapters/ent"

entClient.Use(entaudit.Hook(auditor))
```

---

## Step 5 — Outbound API logging (manual)

The library doesn't make HTTP calls itself. Wrap the user's existing
call:

```go
start := time.Now()
resp, err := httpClient.Do(req)
duration := time.Since(start)

_ = auditor.API().Record(ctx, audit.APIEntry{
    Service:         "stripe",
    Endpoint:        "/v1/charges",
    Method:          http.MethodPost,
    StatusCode:      resp.StatusCode,
    RequestHeaders:  flatten(req.Header),  // map[string]string
    ResponseHeaders: flatten(resp.Header),
    RequestBody:     reqStruct,            // any JSON-encodable; structs work
    ResponseBody:    respStruct,
    DurationMs:      int(duration.Milliseconds()),
})
```

Redaction happens automatically per `RedactHeaders` /
`RedactBodyFields`. Bodies above `MaxBodySize` get wrapped in a
`{"_truncated": true, ...}` envelope, so the column stays valid JSON.

---

## Step 6 — Transaction correlation (optional but useful)

Tie a data change and an API call to one business operation:

```go
txID := audit.NewTransactionID()
ctx = audit.WithTransactionID(ctx, txID)

gormDB.WithContext(ctx).Save(&order) // data change uses txID
_ = auditor.API().Record(ctx, /* ... */) // API log uses same txID

// later:
combined, _ := auditor.QueryByTransaction(ctx, txID)
// combined.DataLogs + combined.APILogs — full story
```

---

## Step 7 — Querying

```go
logs, _ := auditor.Query(ctx, audit.DataFilter{
    EntityType: "products",
    EntityID:   "42",
    Action:     audit.ActionUpdate,
    DateFrom:   time.Now().AddDate(0, -1, 0),
    Limit:      50,
})
```

`AuditLog.OldValues` and `AuditLog.NewValues` are `json.RawMessage` —
unmarshal into whatever shape suits the read site.

---

## Snapshot & Restore (power feature)

Reconstruct an entity's state at any past time, or revert it:

```go
state, _ := auditor.Snapshot(ctx, "products", "42", yesterday)
// state == nil → entity didn't exist or was deleted at that point

result, _ := auditor.Restore(ctx, "products", "42", yesterday)
// Records a "restore" audit entry automatically.
// Caller applies the values via their ORM:
gormDB.Model(&Product{}).Where("id = ?", 42).Updates(result.Values)
```

---

## Common pitfalls

These are the issues real users hit on the first integration:

1. **Pre-v1.1.0 GORM `WHERE WHERE` bug.** v1.0.x snapshot SELECT
   emits malformed SQL (`WHERE WHERE …`) and cancels user writes with
   PostgreSQL `42601`. Always pin `v1.1.0` or later.

2. **`response_headers` column missing after upgrade from v1.0.x.**
   `AutoMigrate` is `CREATE TABLE IF NOT EXISTS` — it does not add
   columns to existing tables. Run the ALTER manually:

   ```sql
   -- PostgreSQL
   ALTER TABLE audit_api_logs ADD COLUMN response_headers JSONB;
   -- MySQL
   ALTER TABLE audit_api_logs ADD COLUMN response_headers JSON;
   -- SQLite
   ALTER TABLE audit_api_logs ADD COLUMN response_headers TEXT;
   ```

3. **Recursive auditing of the audit table.** If the user stores
   `audit_logs` in the same DB as their domain tables AND uses a
   global ORM that runs through the adapter, add the audit table name
   to `ExcludeEntities`.

4. **Bulk back-fills creating millions of audit rows.** For
   migrations and one-off bulk writes, bypass the GORM plugin:

   ```go
   gormDB.Session(&gorm.Session{SkipHooks: true}).
       CreateInBatches(rows, 1000)
   ```

5. **`old_values` shows `null` instead of being absent on CREATE.**
   Fixed in v1.1.0 — was a typed-nil-map bug. Upgrade.

6. **Struct API bodies not getting redacted.** Pre-v1.1.0,
   `RedactBodyFields` only walked `map[string]any`. v1.1.0+ handles
   structs/pointers via a JSON round-trip — match is on JSON tag
   names (not Go field names).

7. **Retention.** Audit tables grow forever by default. Set up a
   cron:

   ```go
   _, _ = auditor.Purge(ctx, time.Now().AddDate(0, 0, -90))
   ```

---

## Quick-start checklist (paste-ready)

When the user says "set up go-audit," confirm these decisions before
writing code:

1. Dialect? (postgres / mysql / sqlite)
2. ORM? (gorm / bun / ent / none — manual recording)
3. Which audits enabled? (data / api / both)
4. Field exclusion list — at minimum: `password` and any token columns.
5. Header redaction list — at minimum: `Authorization`, `X-API-Key`, `Cookie`.
6. Single audit pool or dedicated `*sql.DB`?
7. Error mode: fail loud (default) or silent?

Once you have these, generate the `audit.Config{...}` literal, the
`AutoMigrate(ctx)` call, and the one-line adapter registration. That's
the whole integration.

---

## Reference

- Repo: <https://github.com/gopackx/go-audit>
- Docs site: <https://go-audit.dev> (or wherever the user's docs render).
- Issue patterns: check the changelog page first when something
  behaves unexpectedly — most early surprises are documented as
  release notes for v1.1.0+.
