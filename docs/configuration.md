# Configuration Reference

## Config Struct

```go
type Config struct {
    Dialect   audit.DialectType   // Required: PostgreSQL | MySQL | SQLite
    UserFunc  audit.UserFunc      // Required when DataAudit is enabled
    DataAudit audit.DataAuditConfig
    APIAudit  audit.APIAuditConfig
    Logger    func(format string, args ...any) // Optional: custom logger for silent errors
}
```

## Dialect

Specifies the SQL dialect for DDL generation and query building.

| Value | Constant | Notes |
|-------|----------|-------|
| `"postgres"` | `audit.PostgreSQL` | Uses JSONB, GIN indexes, `$n` placeholders |
| `"mysql"` | `audit.MySQL` | Uses JSON type, `?` placeholders |
| `"sqlite"` | `audit.SQLite` | Uses TEXT for JSON fields, `?` placeholders |

```go
auditor, _ := audit.New(db, audit.Config{
    Dialect: audit.PostgreSQL,
})
```

### Custom Dialects

You can register custom dialects for other databases:

```go
audit.RegisterDialect("cockroachdb", myCustomDialect)
```

The dialect must implement the `audit.Dialect` interface.

## UserFunc

A function that extracts user identity from `context.Context`. Required when `DataAudit.Enabled` is true.

```go
UserFunc: func(ctx context.Context) (userID string, userType string) {
    // Extract from your auth middleware context
    claims := ctx.Value("auth_claims").(*Claims)
    return claims.UserID, claims.Role // e.g. "user-123", "admin"
},
```

Common patterns:

```go
// From JWT claims
UserFunc: func(ctx context.Context) (string, string) {
    if claims, ok := ctx.Value(authKey{}).(*Claims); ok {
        return claims.Subject, claims.Role
    }
    return "anonymous", "guest"
},

// From API key
UserFunc: func(ctx context.Context) (string, string) {
    if key, ok := ctx.Value("api_key").(string); ok {
        return key, "api"
    }
    return "system", "system"
},
```

## DataAuditConfig

Controls data-change auditing (create/update/delete tracking).

```go
type DataAuditConfig struct {
    Table           string      // Table name. Default: "audit_logs"
    Enabled         bool        // Enable data auditing. Default: false
    ExcludeFields   []string    // Fields to never store in old/new values
    ExcludeEntities []string    // Entity types to skip entirely
    SkipOldValues   bool        // Don't snapshot pre-change values (saves one SELECT per write)
    OnError         ErrorMode   // How to handle storage failures
}
```

### Table

Custom table name for data audit logs. Must be a valid SQL identifier (letters, digits, underscores; max 63 chars). Default: `"audit_logs"`.

```go
DataAudit: audit.DataAuditConfig{
    Table: "my_data_audit",
},
```

### ExcludeFields

Fields listed here are stripped from both `old_values` and `new_values` before persisting. Use this for sensitive data that should never appear in audit logs.

```go
ExcludeFields: []string{"password", "remember_token", "ssn", "credit_card"},
```

### ExcludeEntities

Entity types listed here are skipped entirely. No audit log is recorded for them.

```go
ExcludeEntities: []string{"sessions", "cache_entries", "job_batches"},
```

### SkipOldValues

When `true`, ORM adapters will not issue a `SELECT` to capture pre-change values on UPDATE/DELETE. The resulting audit log will have empty `old_values`. Trades audit completeness for performance (one fewer query per write).

```go
SkipOldValues: true, // Faster writes, but no old_values in logs
```

### OnError (ErrorMode)

Controls behavior when a storage operation fails.

| Value | Constant | Behavior |
|-------|----------|----------|
| `0` | `audit.ErrorFailLoud` | Returns the error to the caller. In GORM, attaches via `AddError`. **Default.** |
| `1` | `audit.ErrorFailSilent` | Logs the error via `Config.Logger` and returns `nil`. Use when audit failures shouldn't block writes. |

```go
DataAudit: audit.DataAuditConfig{
    OnError: audit.ErrorFailSilent, // Don't block writes if audit DB is down
},
```

## APIAuditConfig

Controls third-party API call logging.

```go
type APIAuditConfig struct {
    Table            string      // Table name. Default: "audit_api_logs"
    Enabled          bool        // Enable API auditing. Default: false
    RedactHeaders    []string    // Header names to redact (case-insensitive)
    RedactBodyFields []string    // Body field names to redact (recursive)
    MaxBodySize      int         // Truncate bodies above this size in bytes. Default: 4096
    OnError          ErrorMode   // How to handle storage failures
}
```

### RedactHeaders

Headers listed here have their values replaced with `***REDACTED***` before storage. Matching is case-insensitive.

```go
RedactHeaders: []string{"Authorization", "X-API-Key", "X-Secret-Token"},
```

Result: `{"Authorization": "***REDACTED***", "Content-Type": "application/json"}`

### RedactBodyFields

Field names listed here are redacted recursively in both request and response bodies. Works with nested objects.

```go
RedactBodyFields: []string{"password", "secret", "token", "card_number"},
```

Input:
```json
{"user": "alice", "password": "s3cret", "nested": {"token": "abc"}}
```

Stored as:
```json
{"user": "alice", "password": "***REDACTED***", "nested": {"token": "***REDACTED***"}}
```

### MaxBodySize

Request and response bodies larger than this (in bytes) are truncated to a valid JSON envelope with a preview. Default: `4096` (4 KiB).

```go
MaxBodySize: 8192, // 8 KiB
```

## Logger

Custom logger for errors swallowed under `ErrorFailSilent`. Falls back to `log.Printf` if nil.

```go
Logger: func(format string, args ...any) {
    slog.Warn(fmt.Sprintf(format, args...))
},
```

## Validation Rules

- At least one of `DataAudit.Enabled` or `APIAudit.Enabled` must be `true`
- `UserFunc` is required when `DataAudit.Enabled` is `true`
- Table names must match `^[A-Za-z_][A-Za-z0-9_]{0,62}$` (prevents SQL injection)

## Full Example

```go
auditor, err := audit.New(sqlDB, audit.Config{
    Dialect: audit.PostgreSQL,
    UserFunc: func(ctx context.Context) (string, string) {
        u := middleware.UserFromContext(ctx)
        return u.ID, u.Role
    },
    Logger: func(format string, args ...any) {
        slog.Warn(fmt.Sprintf(format, args...))
    },
    DataAudit: audit.DataAuditConfig{
        Table:           "audit_logs",
        Enabled:         true,
        ExcludeFields:   []string{"password", "remember_token", "ssn"},
        ExcludeEntities: []string{"sessions", "cache"},
        SkipOldValues:   false,
        OnError:         audit.ErrorFailLoud,
    },
    APIAudit: audit.APIAuditConfig{
        Table:            "audit_api_logs",
        Enabled:          true,
        RedactHeaders:    []string{"Authorization", "X-API-Key"},
        RedactBodyFields: []string{"password", "secret", "token", "card_number"},
        MaxBodySize:      4096,
        OnError:          audit.ErrorFailSilent,
    },
})
```

## Minimal Config (Data Audit Only)

```go
auditor, _ := audit.New(sqlDB, audit.Config{
    Dialect:  audit.PostgreSQL,
    UserFunc: userFromCtx,
    DataAudit: audit.DataAuditConfig{Enabled: true},
    // APIAudit not set — disabled, table not created
})
```

## Minimal Config (API Audit Only)

```go
auditor, _ := audit.New(sqlDB, audit.Config{
    Dialect: audit.PostgreSQL,
    APIAudit: audit.APIAuditConfig{
        Enabled:       true,
        RedactHeaders: []string{"Authorization"},
    },
    // DataAudit not set — disabled
    // UserFunc not required when only API audit is enabled
})
```
