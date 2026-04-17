package audit

import (
	"context"
	"encoding/json"
)

// UserFunc extracts user identity from a context.
type UserFunc func(ctx context.Context) (userID string, userType string)

// DataEntry is the payload an ORM adapter passes to the auditor for a data
// change. OldValues/NewValues are raw field maps; the auditor diffs and
// redacts them before persisting.
type DataEntry struct {
	EntityType string
	EntityID   string
	Action     string
	OldValues  map[string]any
	NewValues  map[string]any
	Metadata   map[string]any
	// TransactionID overrides the context-derived txID when set.
	TransactionID string
}

// APIEntry is the payload recorded for a third-party API call.
type APIEntry struct {
	Service        string
	Endpoint       string
	Method         string
	StatusCode     int
	RequestHeaders map[string]string
	RequestBody    any
	ResponseBody   any
	DurationMs     int
	ErrorMessage   string
	Metadata       map[string]any
	TransactionID  string
}

// RestoreResult holds the outcome of a Restore call.
type RestoreResult struct {
	EntityType string         `json:"entity_type"`
	EntityID   string         `json:"entity_id"`
	Values     map[string]any `json:"values,omitempty"`
	WasDeleted bool           `json:"was_deleted"`
}

// PurgeResult reports how many rows were deleted from each audit table.
type PurgeResult struct {
	DataLogs int64
	APILogs  int64
}

// jsonMarshal is a tiny helper so callers don't import encoding/json twice.
func jsonMarshal(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}
