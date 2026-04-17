# Querying Audit Logs

go-audit provides query methods for both data change logs and API call logs, with filtering, pagination, and cross-concern correlation via transaction IDs.

## Querying Data Change Logs

Use `auditor.Query()` with a `DataFilter`:

```go
logs, err := auditor.Query(ctx, audit.DataFilter{
    EntityType: "products",
    EntityID:   "42",
    Action:     audit.ActionUpdate,
    UserID:     "alice",
    DateFrom:   time.Now().AddDate(0, -1, 0), // last 30 days
    DateTo:     time.Now(),
    Limit:      50,
    Offset:     0,
})
```

### DataFilter Fields

All fields are optional. Only non-zero fields are included in the WHERE clause.

| Field | Type | Description |
|-------|------|-------------|
| `EntityType` | `string` | Filter by entity type (e.g. `"products"`) |
| `EntityID` | `string` | Filter by entity ID |
| `Action` | `string` | Filter by action (`"create"`, `"update"`, `"delete"`, `"soft_delete"`, `"restore"`) |
| `UserID` | `string` | Filter by user who made the change |
| `TransactionID` | `string` | Filter by transaction ID |
| `DateFrom` | `time.Time` | Only logs created at or after this time |
| `DateTo` | `time.Time` | Only logs created at or before this time |
| `Limit` | `int` | Max rows to return. 0 = no limit |
| `Offset` | `int` | Skip this many rows (for pagination) |

Results are ordered by `created_at DESC` (newest first).

### Examples

```go
// All changes to a specific entity
logs, _ := auditor.Query(ctx, audit.DataFilter{
    EntityType: "products",
    EntityID:   "42",
})

// All deletes by a specific user
logs, _ := auditor.Query(ctx, audit.DataFilter{
    Action: audit.ActionDelete,
    UserID: "admin-1",
})

// All changes in the last hour, paginated
logs, _ := auditor.Query(ctx, audit.DataFilter{
    DateFrom: time.Now().Add(-1 * time.Hour),
    Limit:    20,
    Offset:   0,  // page 1
})

// Page 2
logs, _ = auditor.Query(ctx, audit.DataFilter{
    DateFrom: time.Now().Add(-1 * time.Hour),
    Limit:    20,
    Offset:   20,
})
```

## Querying API Call Logs

Use `auditor.API().Query()` with an `APIFilter`:

```go
apiLogs, err := auditor.API().Query(ctx, audit.APIFilter{
    Service:    "stripe",
    StatusCode: 500,
    DateFrom:   time.Now().Add(-24 * time.Hour),
    Limit:      100,
})
```

### APIFilter Fields

All fields are optional.

| Field | Type | Description |
|-------|------|-------------|
| `Service` | `string` | Filter by service name (e.g. `"stripe"`) |
| `StatusCode` | `int` | Filter by HTTP status code |
| `UserID` | `string` | Filter by user |
| `TransactionID` | `string` | Filter by transaction ID |
| `DateFrom` | `time.Time` | Only logs created at or after this time |
| `DateTo` | `time.Time` | Only logs created at or before this time |
| `Limit` | `int` | Max rows to return |
| `Offset` | `int` | Skip rows for pagination |

Results are ordered by `created_at DESC` (newest first).

### Examples

```go
// All calls to a service
apiLogs, _ := auditor.API().Query(ctx, audit.APIFilter{
    Service: "payment-gateway",
})

// Failed calls in the last 24 hours
apiLogs, _ := auditor.API().Query(ctx, audit.APIFilter{
    StatusCode: 500,
    DateFrom:   time.Now().Add(-24 * time.Hour),
})

// All API calls by a specific user
apiLogs, _ := auditor.API().Query(ctx, audit.APIFilter{
    UserID: "alice",
    Limit:  50,
})
```

## Cross-Concern Query by Transaction

Use `auditor.QueryByTransaction()` to fetch both data changes and API calls linked by a transaction ID:

```go
logs, err := auditor.QueryByTransaction(ctx, txID)
// logs.TransactionID  -> the transaction ID
// logs.DataLogs       -> []AuditLog (data changes)
// logs.APILogs        -> []AuditAPILog (API calls)
```

This returns a `TransactionLog`:

```go
type TransactionLog struct {
    TransactionID string
    DataLogs      []AuditLog
    APILogs       []AuditAPILog
}
```

### Example: Full Transaction Trace

```go
txID := audit.NewTransactionID()
ctx = audit.WithTransactionID(ctx, txID)

// 1. API call to payment gateway
auditor.API().Record(ctx, audit.APIEntry{
    Service:  "stripe",
    Endpoint: "/v1/charges",
    Method:   "POST",
    // ...
})

// 2. Data change (auto-audited, same txID from context)
db.WithContext(ctx).Save(&order)

// 3. Query the full transaction
bundle, _ := auditor.QueryByTransaction(ctx, txID)
fmt.Printf("Transaction %s:\n", bundle.TransactionID)
fmt.Printf("  %d data changes\n", len(bundle.DataLogs))
fmt.Printf("  %d API calls\n", len(bundle.APILogs))
```

## Working with Results

### AuditLog Fields

```go
type AuditLog struct {
    ID            uint64          `json:"id"`
    EntityType    string          `json:"entity_type"`
    EntityID      string          `json:"entity_id"`
    Action        string          `json:"action"`
    OldValues     json.RawMessage `json:"old_values,omitempty"`
    NewValues     json.RawMessage `json:"new_values,omitempty"`
    UserID        string          `json:"user_id"`
    UserType      string          `json:"user_type,omitempty"`
    Metadata      json.RawMessage `json:"metadata,omitempty"`
    TransactionID string          `json:"transaction_id,omitempty"`
    CreatedAt     time.Time       `json:"created_at"`
}
```

### Parsing JSON Values

`OldValues` and `NewValues` are `json.RawMessage`. Unmarshal them as needed:

```go
for _, log := range logs {
    if log.Action == audit.ActionUpdate {
        var old, new map[string]any
        json.Unmarshal(log.OldValues, &old)
        json.Unmarshal(log.NewValues, &new)

        for key := range new {
            fmt.Printf("  %s: %v -> %v\n", key, old[key], new[key])
        }
    }
}
```

### Serializing to JSON

All structs have JSON tags and serialize cleanly:

```go
data, _ := json.MarshalIndent(logs, "", "  ")
fmt.Println(string(data))
```
