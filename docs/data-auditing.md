# Data Auditing

go-audit automatically tracks data changes (create, update, delete) through your ORM's lifecycle hooks. Once an adapter is registered, every write operation is audited with zero manual code.

## How It Works

1. **Before a write** — the adapter snapshots the current row(s) from the database (the "old values")
2. **After the write** — the adapter reads the new values from the ORM model
3. **Diff** — go-audit compares old vs new and stores only the changed fields
4. **Persist** — the resulting audit log is inserted into the `audit_logs` table

```
User writes → ORM hook fires → Snapshot old → Execute write → Diff → Store audit log
```

## Actions

| Constant | Value | old_values | new_values |
|----------|-------|------------|------------|
| `audit.ActionCreate` | `"create"` | `null` | All fields |
| `audit.ActionUpdate` | `"update"` | Changed fields (old) | Changed fields (new) |
| `audit.ActionDelete` | `"delete"` | All fields | `null` |
| `audit.ActionSoftDelete` | `"soft_delete"` | All fields | `{"deleted_at": "..."}` |
| `audit.ActionRestore` | `"restore"` | Current state | Target state |

### Create

Only `new_values` is populated. Contains all non-excluded fields.

```json
{
  "action": "create",
  "old_values": null,
  "new_values": {"name": "Widget", "price": 100, "status": "active"}
}
```

### Update

Both `old_values` and `new_values` contain **only the fields that changed**. Unchanged fields are omitted.

```json
{
  "action": "update",
  "old_values": {"price": 100},
  "new_values": {"price": 150}
}
```

If no fields actually changed (values are identical), the audit log is **skipped entirely**.

### Delete

Only `old_values` is populated. Contains all non-excluded fields at the time of deletion.

```json
{
  "action": "delete",
  "old_values": {"name": "Widget", "price": 150, "status": "active"},
  "new_values": null
}
```

### Soft Delete (GORM only)

Detected automatically when the model has a `gorm.DeletedAt` field. Recorded as `action: "soft_delete"` with the `deleted_at` timestamp in `new_values`.

```go
type Product struct {
    ID        uint           `gorm:"primaryKey"`
    Name      string
    DeletedAt gorm.DeletedAt `gorm:"index"` // enables soft delete
}

db.Delete(&product)             // -> action: "soft_delete"
db.Unscoped().Delete(&product)  // -> action: "delete"
```

## Field Exclusion

Sensitive fields are stripped from both `old_values` and `new_values` before storage:

```go
DataAudit: audit.DataAuditConfig{
    Enabled:       true,
    ExcludeFields: []string{"password", "remember_token", "ssn"},
},
```

Even if the model contains a `password` field, it will never appear in audit logs.

## Entity Exclusion

Skip auditing entirely for certain entity types:

```go
DataAudit: audit.DataAuditConfig{
    Enabled:         true,
    ExcludeEntities: []string{"sessions", "cache_entries"},
},
```

## Batch Operations

When a single ORM call affects multiple rows (e.g., `WHERE price < 100`), each affected row gets its own audit log entry. All entries share the same `transaction_id` so you can group them.

```go
// Updates 50 rows -> produces 50 audit log entries, all with the same transaction_id
db.WithContext(ctx).Model(&Product{}).
    Where("price < ?", 100).
    Updates(Product{Status: "archived"})
```

## Skipping Old Values

For high-throughput systems where the pre-change SELECT is too expensive:

```go
DataAudit: audit.DataAuditConfig{
    Enabled:       true,
    SkipOldValues: true, // No SELECT before writes
},
```

This means `old_values` will always be empty in audit logs, but you save one database query per write operation.

## Metadata

Attach arbitrary metadata to audit entries via the `DataEntry.Metadata` field. This is available when recording manually or through adapter-specific mechanisms:

```go
auditor.RecordDataChange(ctx, audit.DataEntry{
    EntityType: "orders",
    EntityID:   "order-123",
    Action:     audit.ActionUpdate,
    OldValues:  map[string]any{"status": "pending"},
    NewValues:  map[string]any{"status": "shipped"},
    Metadata:   map[string]any{"reason": "customer request", "ticket": "SUP-456"},
})
```

## Audit Log Schema

Each data change produces one row in the `audit_logs` table:

| Column | Type | Description |
|--------|------|-------------|
| `id` | BIGINT (auto) | Primary key |
| `entity_type` | VARCHAR(100) | Table/model name (e.g. `"products"`) |
| `entity_id` | VARCHAR(100) | Primary key of the affected row |
| `action` | VARCHAR(20) | `create`, `update`, `delete`, `soft_delete`, `restore` |
| `old_values` | JSON/JSONB | Previous field values (only changed fields for updates) |
| `new_values` | JSON/JSONB | New field values (only changed fields for updates) |
| `user_id` | VARCHAR(100) | Who made the change (from `UserFunc`) |
| `user_type` | VARCHAR(50) | User type/role (from `UserFunc`) |
| `metadata` | JSON/JSONB | Arbitrary metadata |
| `transaction_id` | VARCHAR(100) | Groups related changes together |
| `created_at` | TIMESTAMP | When the change was recorded |
