# Advanced Features

## Transaction Correlation

go-audit can link data changes and API calls together using a shared `transaction_id`. This gives you full traceability: user action -> API call -> data change.

### Generating a Transaction ID

```go
txID := audit.NewTransactionID()
// Format: "20260417T103045-a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4"
// Prefix: YYYYMMDDTHHmmss (sortable)
// Suffix: 128-bit random hex (unique)
```

### Attaching to Context

```go
ctx = audit.WithTransactionID(ctx, txID)
```

Once attached, both ORM adapters and `auditor.API().Record()` will automatically use this transaction ID.

### Reading from Context

```go
txID := audit.TransactionIDFromContext(ctx)
```

### Full Example

```go
func processPayment(ctx context.Context, order Order) error {
    txID := audit.NewTransactionID()
    ctx = audit.WithTransactionID(ctx, txID)

    // 1. Call payment gateway — recorded with txID
    start := time.Now()
    resp, err := stripe.Charge(ctx, order.Amount)
    auditor.API().Record(ctx, audit.APIEntry{
        Service:    "stripe",
        Endpoint:   "/v1/charges",
        Method:     "POST",
        StatusCode: resp.StatusCode,
        DurationMs: int(time.Since(start).Milliseconds()),
    })

    // 2. Update order status — auto-audited with same txID
    order.Status = "paid"
    order.ChargeID = resp.ChargeID
    db.WithContext(ctx).Save(&order)

    // 3. Create shipment record — auto-audited with same txID
    db.WithContext(ctx).Create(&Shipment{OrderID: order.ID, Status: "pending"})

    // Later: query everything tied to this transaction
    // auditor.QueryByTransaction(ctx, txID)
    return nil
}
```

### Batch Operations

When an ORM adapter processes a batch write (e.g., `WHERE price < 100`), it automatically generates a transaction ID and groups all resulting audit entries under it.

---

## Snapshot

Reconstruct the state of an entity at any point in time by replaying all audit logs up to that moment.

```go
// What did Product#42 look like yesterday at 3pm?
state, err := auditor.Snapshot(ctx, "products", "42", yesterday)
// state = map[string]any{"name": "Widget", "price": 100, "status": "active"}
```

### How It Works

1. Fetches all audit logs for the entity up to the given time, ordered chronologically
2. Replays them in order:
   - `create`: sets initial state from `new_values`
   - `update`: applies `new_values` on top of current state
   - `delete` / `soft_delete`: marks entity as deleted
   - `restore`: reapplies `new_values`
3. Returns the reconstructed state, or `nil` if the entity was deleted at that time

### Return Values

| Scenario | Return |
|----------|--------|
| Entity existed and was active | `map[string]any{...}` with field values |
| Entity was deleted at that time | `nil` |
| Entity didn't exist yet | `nil` |

### Limitations

- Only fields captured in audit logs are reconstructed. If `ExcludeFields` was used, those fields won't appear.
- Accuracy depends on having complete audit history from the entity's creation.

---

## Restore

Revert an entity to a previous state. This is a two-step process:

1. go-audit reconstructs the target state via `Snapshot`
2. go-audit records a `"restore"` audit entry (old=current, new=target)
3. **You** apply the returned values via your ORM

```go
result, err := auditor.Restore(ctx, "products", "42", yesterday)
if err != nil {
    return err
}

if result.WasDeleted {
    // Entity was deleted at the target time — delete it now
    db.Delete(&product, 42)
} else {
    // Apply the restored values
    db.Model(&Product{}).Where("id = ?", 42).Updates(result.Values)
}
```

### RestoreResult

```go
type RestoreResult struct {
    EntityType string         `json:"entity_type"`
    EntityID   string         `json:"entity_id"`
    Values     map[string]any `json:"values,omitempty"`
    WasDeleted bool           `json:"was_deleted"`
}
```

| Field | Description |
|-------|-------------|
| `Values` | The reconstructed field values to apply. `nil` if entity was deleted. |
| `WasDeleted` | `true` if the entity was deleted at the target time. |

### Audit Trail for Restore

The restore itself is recorded as an audit entry:

```json
{
  "action": "restore",
  "entity_type": "products",
  "entity_id": "42",
  "old_values": {"name": "Widget Pro", "price": 200},
  "new_values": {"name": "Widget", "price": 100}
}
```

The ORM adapter will **not** double-record the subsequent `Updates()` call because the restore entry already captures the change.

---

## Retention / Purge

Delete audit logs older than a given cutoff. Useful for compliance or storage management.

```go
result, err := auditor.Purge(ctx, time.Now().AddDate(0, 0, -90)) // delete logs older than 90 days
if err != nil {
    return err
}

fmt.Printf("Purged %d data logs, %d API logs\n", result.DataLogs, result.APILogs)
```

### PurgeResult

```go
type PurgeResult struct {
    DataLogs int64  // rows deleted from audit_logs
    APILogs  int64  // rows deleted from audit_api_logs
}
```

- Only enabled tables are purged. If `APIAudit.Enabled` is `false`, `APILogs` will be `0`.
- The purge deletes rows where `created_at < before` (strictly older).

### Scheduling Purge

Run purge on a schedule (e.g., daily cron job):

```go
// In your cron handler or scheduled task
func purgeOldAuditLogs(ctx context.Context, auditor audit.Auditor) {
    cutoff := time.Now().AddDate(0, 0, -90) // keep 90 days
    result, err := auditor.Purge(ctx, cutoff)
    if err != nil {
        log.Printf("purge failed: %v", err)
        return
    }
    log.Printf("purged audit logs: data=%d api=%d", result.DataLogs, result.APILogs)
}
```

---

## Error Handling Modes

go-audit supports two error modes, configurable independently for data and API audit:

### ErrorFailLoud (Default)

Storage errors are returned to the caller. In GORM, this means the error is attached via `db.AddError()` and surfaces through `db.Error`.

```go
DataAudit: audit.DataAuditConfig{
    OnError: audit.ErrorFailLoud,
},
```

Use this for **compliance-strict deployments** where a failed audit log should block the operation.

### ErrorFailSilent

Storage errors are logged (via `Config.Logger`) and the caller receives `nil`. The business write succeeds even if audit storage fails.

```go
DataAudit: audit.DataAuditConfig{
    OnError: audit.ErrorFailSilent,
},
Logger: func(format string, args ...any) {
    slog.Warn(fmt.Sprintf(format, args...))
},
```

Use this for **best-effort auditing** where application availability is more important than audit completeness.

---

## Custom Store

Replace the default `database/sql` store with your own implementation:

```go
type Store interface {
    SaveDataLog(ctx context.Context, table string, log AuditLog) error
    SaveAPILog(ctx context.Context, table string, log AuditAPILog) error
    QueryDataLogs(ctx context.Context, table string, filter DataFilter) ([]AuditLog, error)
    QueryAPILogs(ctx context.Context, table string, filter APIFilter) ([]AuditAPILog, error)
    Purge(ctx context.Context, table string, before time.Time) (int64, error)
    Exec(ctx context.Context, stmt string) error
}
```

Use `audit.NewWithStore()` instead of `audit.New()`:

```go
auditor, err := audit.NewWithStore(myCustomStore, myDialect, audit.Config{...})
```

### Built-in Stores

| Store | Constructor | Use Case |
|-------|-------------|----------|
| SQL Store | `audit.NewSQLStore(db, dialect)` | Production — uses `database/sql` |
| Memory Store | `audit.NewMemoryStore()` | Testing — in-memory, no database needed |

---

## Custom Dialect

Register a custom dialect for databases not covered by the built-in three:

```go
type Dialect interface {
    Name() DialectType
    CreateDataTableSQL(table string) string
    CreateAPITableSQL(table string) string
    CreateIndexesSQL(table string) []string
    CreateAPIIndexesSQL(table string) []string
    Placeholder(n int) string
}

audit.RegisterDialect("cockroachdb", &CockroachDialect{})
```

The dialect registry is thread-safe.
