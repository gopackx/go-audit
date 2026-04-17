# API Call Auditing

go-audit provides a dedicated system for logging third-party API calls — payment gateways, shipping APIs, email services, etc. Unlike data auditing (which is automatic via ORM hooks), API call logging is manual: you call `auditor.API().Record()` after each API call.

## Setup

Enable API auditing in your config:

```go
auditor, _ := audit.New(sqlDB, audit.Config{
    Dialect: audit.PostgreSQL,
    APIAudit: audit.APIAuditConfig{
        Enabled:          true,
        RedactHeaders:    []string{"Authorization", "X-API-Key"},
        RedactBodyFields: []string{"password", "secret", "token", "card_number"},
        MaxBodySize:      4096,
    },
})
auditor.AutoMigrate(ctx)
```

## Recording an API Call

```go
start := time.Now()
resp, err := paymentClient.Charge(ctx, chargeReq)
elapsed := time.Since(start)

err = auditor.API().Record(ctx, audit.APIEntry{
    Service:    "stripe",
    Endpoint:   "/v1/charges",
    Method:     "POST",
    StatusCode: resp.StatusCode,
    RequestHeaders: map[string]string{
        "Authorization": "Bearer sk_live_xxx",
        "Content-Type":  "application/json",
    },
    RequestBody: map[string]any{
        "amount":   5000,
        "currency": "usd",
        "token":    "tok_visa_4242",
    },
    ResponseBody: map[string]any{
        "id":     "ch_1234",
        "status": "succeeded",
    },
    DurationMs:   int(elapsed.Milliseconds()),
    ErrorMessage: "", // Populate on error
    Metadata: map[string]any{
        "order_id": "order-789",
    },
})
```

## APIEntry Fields

| Field | Type | Description |
|-------|------|-------------|
| `Service` | `string` | Service name (e.g. `"stripe"`, `"bca"`, `"sendgrid"`) |
| `Endpoint` | `string` | API endpoint path |
| `Method` | `string` | HTTP method (`GET`, `POST`, etc.) |
| `StatusCode` | `int` | HTTP response status code |
| `RequestHeaders` | `map[string]string` | Request headers (redacted before storage) |
| `RequestBody` | `any` | Request body — any JSON-serializable value |
| `ResponseBody` | `any` | Response body — any JSON-serializable value |
| `DurationMs` | `int` | Request duration in milliseconds |
| `ErrorMessage` | `string` | Error message if the call failed |
| `Metadata` | `map[string]any` | Arbitrary metadata |
| `TransactionID` | `string` | Override context-derived transaction ID |

## Auto-Redaction

### Header Redaction

Headers listed in `RedactHeaders` are replaced with `***REDACTED***` before storage. Matching is **case-insensitive**.

```go
RedactHeaders: []string{"Authorization", "X-API-Key"},
```

Input:
```json
{"Authorization": "Bearer sk_live_xxx", "Content-Type": "application/json"}
```

Stored as:
```json
{"Authorization": "***REDACTED***", "Content-Type": "application/json"}
```

### Body Field Redaction

Fields listed in `RedactBodyFields` are redacted **recursively** in both request and response bodies. Works with nested objects.

```go
RedactBodyFields: []string{"token", "card_number", "secret"},
```

Input:
```json
{
  "amount": 5000,
  "token": "tok_visa_4242",
  "billing": {
    "card_number": "4111-1111-1111-1111",
    "name": "Alice"
  }
}
```

Stored as:
```json
{
  "amount": 5000,
  "token": "***REDACTED***",
  "billing": {
    "card_number": "***REDACTED***",
    "name": "Alice"
  }
}
```

## Body Size Truncation

Bodies larger than `MaxBodySize` (default: 4096 bytes) are truncated to a valid JSON envelope with a preview of the original content. This prevents oversized payloads from bloating your audit table.

```go
MaxBodySize: 8192, // 8 KiB
```

## Recording Errors

When an API call fails, capture the error:

```go
resp, err := client.Do(req)
entry := audit.APIEntry{
    Service:  "payment-gateway",
    Endpoint: "/charge",
    Method:   "POST",
}

if err != nil {
    entry.ErrorMessage = err.Error()
    entry.StatusCode = 0
} else {
    entry.StatusCode = resp.StatusCode
}

auditor.API().Record(ctx, entry)
```

## Querying API Logs

```go
// All failed calls to the payment gateway in the last 24 hours
logs, _ := auditor.API().Query(ctx, audit.APIFilter{
    Service:    "payment-gateway",
    StatusCode: 500,
    DateFrom:   time.Now().Add(-24 * time.Hour),
    Limit:      100,
})

for _, l := range logs {
    fmt.Printf("%s %s -> %d (%dms) error=%s\n",
        l.Method, l.Endpoint, l.StatusCode, l.DurationMs, l.ErrorMessage)
}
```

See [Querying](./querying.md) for full filter options.

## Transaction Correlation

Link API calls to data changes using a shared transaction ID:

```go
txID := audit.NewTransactionID()
ctx = audit.WithTransactionID(ctx, txID)

// API call
resp, _ := client.Transfer(ctx, req)
auditor.API().Record(ctx, audit.APIEntry{...})

// Data change (auto-audited, same txID)
db.WithContext(ctx).Save(&transaction)

// Query everything in this transaction
logs, _ := auditor.QueryByTransaction(ctx, txID)
// logs.DataLogs  -> data changes
// logs.APILogs   -> API calls
```

See [Advanced Features](./advanced.md) for more on transaction correlation.

## API Log Schema

Each API call produces one row in the `audit_api_logs` table:

| Column | Type | Description |
|--------|------|-------------|
| `id` | BIGINT (auto) | Primary key |
| `service` | VARCHAR(100) | Service name |
| `endpoint` | VARCHAR(500) | API endpoint |
| `method` | VARCHAR(10) | HTTP method |
| `status_code` | INT | HTTP status code |
| `request_headers` | JSON/JSONB | Request headers (redacted) |
| `request_body` | JSON/JSONB | Request body (redacted + truncated) |
| `response_body` | JSON/JSONB | Response body (redacted + truncated) |
| `duration_ms` | INT | Duration in milliseconds |
| `error_message` | TEXT | Error message if failed |
| `user_id` | VARCHAR(100) | Who made the call (from context) |
| `metadata` | JSON/JSONB | Arbitrary metadata |
| `transaction_id` | VARCHAR(100) | Links to related data changes |
| `created_at` | TIMESTAMP | When recorded |
