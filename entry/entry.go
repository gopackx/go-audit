// Package entry contains the persistent data types and query filters used
// across go-audit. These are leaf types with no project dependencies, so
// every other package can depend on entry without risking import cycles.
package entry

import (
	"encoding/json"
	"time"
)

// AuditLog is one row in the data-change audit table.
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

// AuditAPILog is one row in the API-call audit table.
type AuditAPILog struct {
	ID             uint64          `json:"id"`
	Service        string          `json:"service"`
	Endpoint       string          `json:"endpoint"`
	Method         string          `json:"method"`
	StatusCode     int             `json:"status_code"`
	RequestHeaders  json.RawMessage `json:"request_headers,omitempty"`
	ResponseHeaders json.RawMessage `json:"response_headers,omitempty"`
	RequestBody     json.RawMessage `json:"request_body,omitempty"`
	ResponseBody    json.RawMessage `json:"response_body,omitempty"`
	DurationMs     int             `json:"duration_ms"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	UserID         string          `json:"user_id,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	TransactionID  string          `json:"transaction_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

// DataFilter narrows a query over audit_logs.
type DataFilter struct {
	EntityType    string
	EntityID      string
	Action        string
	UserID        string
	TransactionID string
	DateFrom      time.Time
	DateTo        time.Time
	Limit         int
	Offset        int
}

// APIFilter narrows a query over audit_api_logs.
type APIFilter struct {
	Service       string
	StatusCode    int
	UserID        string
	TransactionID string
	DateFrom      time.Time
	DateTo        time.Time
	Limit         int
	Offset        int
}

// TransactionLog is the combined view for a transaction ID.
type TransactionLog struct {
	TransactionID string
	DataLogs      []AuditLog
	APILogs       []AuditAPILog
}
