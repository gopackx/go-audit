# ORM Adapters

go-audit supports three Go ORMs through adapter packages. Each adapter hooks into its ORM's lifecycle to automatically capture data changes — no manual recording needed.

## GORM Adapter

**Install:**
```bash
go get github.com/gopackx/go-audit/adapters/gorm
```

**Register:**
```go
import auditgorm "github.com/gopackx/go-audit/adapters/gorm"

db.Use(auditgorm.Plugin(auditor))
```

After registration, every `Create`, `Save`, `Update`, and `Delete` is audited automatically.

### How It Works

The GORM adapter registers four callbacks:

| Callback | When | What It Does |
|----------|------|-------------|
| `beforeUpdate` | Before UPDATE | Snapshots old values via `SELECT` with the same WHERE clause |
| `afterCreate` | After INSERT | Records `action: "create"` with all field values |
| `afterUpdate` | After UPDATE | Diffs old vs new, records `action: "update"` with changed fields only |
| `beforeDelete` | Before DELETE | Snapshots old values via `SELECT` |
| `afterDelete` | After DELETE | Records `action: "delete"` (or `"soft_delete"` for soft deletes) |

### Soft Deletes

Models with `gorm.DeletedAt` are automatically detected:

```go
type Product struct {
    ID        uint           `gorm:"primaryKey"`
    Name      string
    DeletedAt gorm.DeletedAt `gorm:"index"`
}

db.Delete(&product)             // -> action: "soft_delete", new_values: {"deleted_at": "..."}
db.Unscoped().Delete(&product)  // -> action: "delete", new_values: null
```

### Batch Operations

Bulk updates/deletes produce one audit log per affected row, all sharing the same `transaction_id`:

```go
db.Model(&Product{}).Where("price < ?", 100).Updates(Product{Status: "archived"})
// -> N audit logs, one per row, same transaction_id
```

### Entity Type

The entity type is derived from the GORM schema table name (e.g., `"products"` for a `Product` model).

### Compound Primary Keys

Compound PKs are serialized as JSON: `["us-east-1","user-42"]`.

---

## Bun Adapter

**Install:**
```bash
go get github.com/gopackx/go-audit/adapters/bun
```

**Register:**
```go
import auditbun "github.com/gopackx/go-audit/adapters/bun"

auditbun.Register(db, auditor)
```

### How It Works

The Bun adapter implements `bun.QueryHook` with two methods:

| Hook | When | What It Does |
|------|------|-------------|
| `BeforeQuery` | Before INSERT/UPDATE/DELETE | Snapshots old values for UPDATE/DELETE |
| `AfterQuery` | After INSERT/UPDATE/DELETE | Records the audit entry |

### Snapshot Strategies

Old values are captured via a pre-query `SELECT`. The adapter uses two strategies:

1. **By Primary Key** — when the model has PKs set (via `WherePK()` or `Bulk()`). Supports compound PKs.
2. **By WHERE clause** — extracts the WHERE clause from the rendered SQL and runs `SELECT * FROM <table> WHERE <same>`. This covers custom-predicate bulk updates/deletes.

```go
// Strategy 1: PK-based snapshot
db.NewUpdate().Model(&product).WherePK().Exec(ctx)

// Strategy 2: WHERE-clause extraction
db.NewUpdate().Model((*Product)(nil)).
    Set("status = ?", "archived").
    Where("price < ?", 100).
    Exec(ctx)
```

### Important: Transaction Visibility

The snapshot SELECT runs against the base `*bun.DB`, so uncommitted writes inside an active `*bun.Tx` are not visible. For strict transaction-local snapshots, load rows explicitly before the write.

### Entity Type

Derived from the Bun table name (e.g., `"products"`).

---

## Ent Adapter

**Install:**
```bash
go get github.com/gopackx/go-audit/adapters/ent
```

**Register:**
```go
import entaudit "github.com/gopackx/go-audit/adapters/ent"

client.Use(entaudit.Hook(auditor))
```

### How It Works

The Ent adapter returns an `ent.Hook` that intercepts all mutations:

| Operation | Action | Old Values |
|-----------|--------|------------|
| `OpCreate` | `"create"` | None |
| `OpUpdate`, `OpUpdateOne` | `"update"` | Captured via `m.OldField()` |
| `OpDelete`, `OpDeleteOne` | `"delete"` | Captured via `m.OldField()` |

### Single-Row vs Bulk

- **Single-row** (`UpdateOne`, `DeleteOne`): captures old values via `ent.Mutation.OldField` and the entity ID via `m.ID()`.
- **Bulk** (`Update`, `Delete`): fetches affected IDs from the mutation, logs each with new values.

### Entity Type

Derived from `m.Type()` on the mutation (e.g., `"Product"` -> lowercased to match convention).

### Old Values

The adapter uses reflection to call `m.OldField(ctx)` for each changed field, capturing the pre-mutation value. This works through Ent's built-in mutation tracking.

---

## Adapter Comparison

| Feature | GORM | Bun | Ent |
|---------|------|-----|-----|
| Auto-create/update/delete | Yes | Yes | Yes |
| Old values on update | Yes (SELECT) | Yes (SELECT) | Yes (`OldField`) |
| Old values on delete | Yes (SELECT) | Yes (SELECT) | Yes (`OldField`) |
| Soft delete detection | Yes | No | No |
| Batch operation support | Yes | Yes | Yes |
| Compound PK support | Yes | Yes | N/A |
| Extra query per write | 1 SELECT | 1 SELECT | 0 (uses mutation cache) |
| Skip old values option | `SkipOldValues: true` | `SkipOldValues: true` | `SkipOldValues: true` |

## Writing a Custom Adapter

Implement the `audit.Adapter` interface:

```go
type Adapter interface {
    Register(auditor audit.Auditor) error
}
```

Your adapter should:

1. Hook into the ORM's lifecycle (before/after create, update, delete)
2. Snapshot old values before writes (unless `SkipOldValues` is set)
3. Call `auditor.RecordDataChange(ctx, audit.DataEntry{...})` after each write
4. Handle batch operations by emitting one entry per affected row
5. Use `audit.NewTransactionID()` for batch grouping and attach via `audit.WithTransactionID(ctx, txID)`

The auditor handles diffing, field exclusion, user extraction, and persistence.
