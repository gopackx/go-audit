package audit

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func newTestAuditor(t *testing.T) Auditor {
	t.Helper()
	a, err := NewWithStore(NewMemoryStore(), DialectFor(SQLite), Config{
		Dialect: SQLite,
		UserFunc: func(ctx context.Context) (string, string) {
			if v, _ := ctx.Value(testUserKey{}).(string); v != "" {
				return v, "user"
			}
			return "anon", "user"
		},
		DataAudit: DataAuditConfig{Enabled: true, ExcludeFields: []string{"password"}},
		APIAudit:  APIAuditConfig{Enabled: true, RedactHeaders: []string{"Authorization"}, RedactBodyFields: []string{"token"}},
	})
	if err != nil {
		t.Fatalf("NewWithStore: %v", err)
	}
	return a
}

type testUserKey struct{}

func TestRecordDataChange_Create(t *testing.T) {
	a := newTestAuditor(t)
	ctx := context.Background()
	err := a.RecordDataChange(ctx, DataEntry{
		EntityType: "products",
		EntityID:   "1",
		Action:     ActionCreate,
		NewValues:  map[string]any{"name": "A", "password": "secret"},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	logs, _ := a.Query(ctx, DataFilter{EntityType: "products"})
	if len(logs) != 1 {
		t.Fatalf("want 1 log, got %d", len(logs))
	}
	var got map[string]any
	_ = json.Unmarshal(logs[0].NewValues, &got)
	if _, hasPw := got["password"]; hasPw {
		t.Fatalf("password should be excluded")
	}
	if got["name"] != "A" {
		t.Fatalf("expected name=A, got %v", got["name"])
	}
	if logs[0].UserID != "anon" {
		t.Fatalf("expected userID=anon, got %q", logs[0].UserID)
	}
}

func TestRecordDataChange_UpdateNoChangeSkipped(t *testing.T) {
	a := newTestAuditor(t)
	ctx := context.Background()
	_ = a.RecordDataChange(ctx, DataEntry{
		EntityType: "products", EntityID: "1", Action: ActionUpdate,
		OldValues: map[string]any{"name": "A"}, NewValues: map[string]any{"name": "A"},
	})
	logs, _ := a.Query(ctx, DataFilter{})
	if len(logs) != 0 {
		t.Fatalf("no-op update should not record, got %d", len(logs))
	}
}

func TestAPIAuditor_RedactsHeadersAndBody(t *testing.T) {
	a := newTestAuditor(t)
	ctx := context.Background()
	err := a.API().Record(ctx, APIEntry{
		Service:        "bca",
		Endpoint:       "/x",
		Method:         "POST",
		StatusCode:     200,
		RequestHeaders: map[string]string{"Authorization": "Bearer x", "Content-Type": "application/json"},
		RequestBody:    map[string]any{"token": "s3cret", "amount": 100},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	logs, _ := a.API().Query(ctx, APIFilter{})
	if len(logs) != 1 {
		t.Fatalf("want 1 log, got %d", len(logs))
	}
	var headers map[string]string
	_ = json.Unmarshal(logs[0].RequestHeaders, &headers)
	if headers["Authorization"] != redactedMarker {
		t.Fatalf("Authorization should be redacted, got %q", headers["Authorization"])
	}
	if headers["Content-Type"] != "application/json" {
		t.Fatalf("Content-Type should be preserved")
	}
	var body map[string]any
	_ = json.Unmarshal(logs[0].RequestBody, &body)
	if body["token"] != redactedMarker {
		t.Fatalf("token should be redacted")
	}
	if body["amount"].(float64) != 100 {
		t.Fatalf("amount should be preserved")
	}
}

func TestQueryByTransaction(t *testing.T) {
	a := newTestAuditor(t)
	ctx := WithTransactionID(context.Background(), "tx-1")
	_ = a.RecordDataChange(ctx, DataEntry{EntityType: "o", EntityID: "1", Action: ActionCreate, NewValues: map[string]any{"x": 1}})
	_ = a.API().Record(ctx, APIEntry{Service: "s", Endpoint: "/e", Method: "GET", StatusCode: 200})
	out, err := a.QueryByTransaction(ctx, "tx-1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(out.DataLogs) != 1 || len(out.APILogs) != 1 {
		t.Fatalf("expected 1+1 logs, got %d+%d", len(out.DataLogs), len(out.APILogs))
	}
}

func TestPurge(t *testing.T) {
	s := NewMemoryStore()
	cfg := Config{
		Dialect:   SQLite,
		UserFunc:  func(context.Context) (string, string) { return "u", "user" },
		DataAudit: DataAuditConfig{Enabled: true},
		APIAudit:  APIAuditConfig{Enabled: true},
	}
	a, err := NewWithStore(s, DialectFor(SQLite), cfg)
	if err != nil {
		t.Fatalf("NewWithStore: %v", err)
	}
	cfg.applyDefaults()

	now := time.Now().UTC()
	old := AuditLog{EntityType: "p", EntityID: "1", Action: ActionCreate, UserID: "u", CreatedAt: now.Add(-48 * time.Hour)}
	fresh := AuditLog{EntityType: "p", EntityID: "2", Action: ActionCreate, UserID: "u", CreatedAt: now}
	if err := s.SaveDataLog(context.Background(), cfg.DataAudit.Table, old); err != nil {
		t.Fatalf("seed old: %v", err)
	}
	if err := s.SaveDataLog(context.Background(), cfg.DataAudit.Table, fresh); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}
	oldAPI := AuditAPILog{Service: "s", Endpoint: "/e", Method: "GET", CreatedAt: now.Add(-48 * time.Hour)}
	if err := s.SaveAPILog(context.Background(), cfg.APIAudit.Table, oldAPI); err != nil {
		t.Fatalf("seed api: %v", err)
	}

	res, err := a.Purge(context.Background(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if res.DataLogs != 1 || res.APILogs != 1 {
		t.Fatalf("expected 1+1 purged, got %d+%d", res.DataLogs, res.APILogs)
	}
	remaining, _ := a.Query(context.Background(), DataFilter{})
	if len(remaining) != 1 || remaining[0].EntityID != "2" {
		t.Fatalf("expected only fresh row to remain, got %+v", remaining)
	}
}

func TestSnapshot_RebuildsStateFromLogs(t *testing.T) {
	a := newTestAuditor(t)
	ctx := context.Background()

	// create -> name=Widget, price=100
	_ = a.RecordDataChange(ctx, DataEntry{
		EntityType: "products", EntityID: "1", Action: ActionCreate,
		NewValues: map[string]any{"name": "Widget", "price": 100},
	})
	t1 := time.Now().UTC()
	time.Sleep(5 * time.Millisecond)

	// update -> price 100->150
	_ = a.RecordDataChange(ctx, DataEntry{
		EntityType: "products", EntityID: "1", Action: ActionUpdate,
		OldValues: map[string]any{"price": 100}, NewValues: map[string]any{"price": 150},
	})
	t2 := time.Now().UTC()
	time.Sleep(5 * time.Millisecond)

	// update -> name Widget->Widget Pro
	_ = a.RecordDataChange(ctx, DataEntry{
		EntityType: "products", EntityID: "1", Action: ActionUpdate,
		OldValues: map[string]any{"name": "Widget"}, NewValues: map[string]any{"name": "Widget Pro"},
	})
	t3 := time.Now().UTC()

	// Snapshot at t1: should be create state
	snap1, err := a.Snapshot(ctx, "products", "1", t1)
	if err != nil {
		t.Fatalf("snapshot t1: %v", err)
	}
	if snap1["name"] != "Widget" || snap1["price"] != float64(100) {
		t.Fatalf("t1: expected name=Widget price=100, got %v", snap1)
	}

	// Snapshot at t2: should have price=150
	snap2, err := a.Snapshot(ctx, "products", "1", t2)
	if err != nil {
		t.Fatalf("snapshot t2: %v", err)
	}
	if snap2["price"] != float64(150) {
		t.Fatalf("t2: expected price=150, got %v", snap2["price"])
	}
	if snap2["name"] != "Widget" {
		t.Fatalf("t2: expected name=Widget, got %v", snap2["name"])
	}

	// Snapshot at t3: should have name=Widget Pro, price=150
	snap3, err := a.Snapshot(ctx, "products", "1", t3)
	if err != nil {
		t.Fatalf("snapshot t3: %v", err)
	}
	if snap3["name"] != "Widget Pro" || snap3["price"] != float64(150) {
		t.Fatalf("t3: expected name=Widget Pro price=150, got %v", snap3)
	}
}

func TestSnapshot_DeletedEntityReturnsNil(t *testing.T) {
	a := newTestAuditor(t)
	ctx := context.Background()

	_ = a.RecordDataChange(ctx, DataEntry{
		EntityType: "products", EntityID: "1", Action: ActionCreate,
		NewValues: map[string]any{"name": "A"},
	})
	time.Sleep(5 * time.Millisecond)
	_ = a.RecordDataChange(ctx, DataEntry{
		EntityType: "products", EntityID: "1", Action: ActionDelete,
		OldValues: map[string]any{"name": "A"},
	})

	snap, err := a.Snapshot(ctx, "products", "1", time.Now().UTC())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap != nil {
		t.Fatalf("expected nil for deleted entity, got %v", snap)
	}
}

func TestSnapshot_NonexistentEntityReturnsNil(t *testing.T) {
	a := newTestAuditor(t)
	snap, err := a.Snapshot(context.Background(), "products", "999", time.Now().UTC())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap != nil {
		t.Fatalf("expected nil for nonexistent entity, got %v", snap)
	}
}

func TestRestore_RevertsToEarlierState(t *testing.T) {
	a := newTestAuditor(t)
	ctx := context.Background()

	// create -> price=100
	_ = a.RecordDataChange(ctx, DataEntry{
		EntityType: "products", EntityID: "1", Action: ActionCreate,
		NewValues: map[string]any{"name": "Widget", "price": 100},
	})
	t1 := time.Now().UTC()
	time.Sleep(5 * time.Millisecond)

	// update -> price=200
	_ = a.RecordDataChange(ctx, DataEntry{
		EntityType: "products", EntityID: "1", Action: ActionUpdate,
		OldValues: map[string]any{"price": 100}, NewValues: map[string]any{"price": 200},
	})
	time.Sleep(5 * time.Millisecond)

	// Restore to t1 (price should be 100 again)
	result, err := a.Restore(ctx, "products", "1", t1)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if result.WasDeleted {
		t.Fatalf("entity should not be deleted")
	}
	if result.Values["price"] != float64(100) {
		t.Fatalf("expected price=100, got %v", result.Values["price"])
	}

	// Verify a "restore" audit entry was recorded.
	logs, _ := a.Query(ctx, DataFilter{EntityType: "products", Action: ActionRestore})
	if len(logs) != 1 {
		t.Fatalf("expected 1 restore log, got %d", len(logs))
	}
	// The restore entry should have old=current state, new=target state.
	var newVals map[string]any
	_ = json.Unmarshal(logs[0].NewValues, &newVals)
	if newVals["price"] != float64(100) {
		t.Fatalf("restore log new_values should have price=100, got %v", newVals["price"])
	}
}

func TestRestore_DeletedEntity(t *testing.T) {
	a := newTestAuditor(t)
	ctx := context.Background()

	_ = a.RecordDataChange(ctx, DataEntry{
		EntityType: "products", EntityID: "1", Action: ActionCreate,
		NewValues: map[string]any{"name": "A"},
	})
	time.Sleep(5 * time.Millisecond)
	_ = a.RecordDataChange(ctx, DataEntry{
		EntityType: "products", EntityID: "1", Action: ActionDelete,
		OldValues: map[string]any{"name": "A"},
	})
	time.Sleep(5 * time.Millisecond)

	result, err := a.Restore(ctx, "products", "1", time.Now().UTC())
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !result.WasDeleted {
		t.Fatalf("expected WasDeleted=true")
	}
	if result.Values != nil {
		t.Fatalf("expected nil values for deleted entity, got %v", result.Values)
	}
}

func TestNewTransactionID_Unique(t *testing.T) {
	a, b := NewTransactionID(), NewTransactionID()
	if a == b {
		t.Fatalf("txIDs should differ")
	}
	if len(a) < 20 {
		t.Fatalf("txID too short: %q", a)
	}
}
