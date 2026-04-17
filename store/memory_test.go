package store

import (
	"context"
	"testing"
	"time"

	"github.com/gopackx/go-audit/entry"
)

func TestMemory_SaveAndQueryDataLog(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()

	row := entry.AuditLog{
		EntityType: "products",
		EntityID:   "1",
		Action:     "create",
		UserID:     "alice",
		CreatedAt:  now,
	}
	if err := s.SaveDataLog(ctx, "audit_logs", row); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.QueryDataLogs(ctx, "audit_logs", entry.DataFilter{EntityType: "products"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 log, got %d", len(got))
	}
	if got[0].EntityID != "1" {
		t.Fatalf("entity_id = %q, want 1", got[0].EntityID)
	}
	if got[0].ID == 0 {
		t.Fatalf("ID should be assigned automatically")
	}
}

func TestMemory_QueryFilter_AppliesAllConditions(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()

	rows := []entry.AuditLog{
		{EntityType: "products", EntityID: "1", Action: "create", UserID: "alice", CreatedAt: now},
		{EntityType: "products", EntityID: "2", Action: "update", UserID: "alice", CreatedAt: now},
		{EntityType: "products", EntityID: "1", Action: "delete", UserID: "bob", CreatedAt: now},
		{EntityType: "orders", EntityID: "1", Action: "create", UserID: "alice", CreatedAt: now},
	}
	for _, r := range rows {
		if err := s.SaveDataLog(ctx, "audit_logs", r); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	got, err := s.QueryDataLogs(ctx, "audit_logs", entry.DataFilter{
		EntityType: "products",
		EntityID:   "1",
		Action:     "create",
		UserID:     "alice",
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 row matching all filters, got %d", len(got))
	}
}

func TestMemory_QueryRespectsLimitAndOffset(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()

	for i := 0; i < 5; i++ {
		_ = s.SaveDataLog(ctx, "audit_logs", entry.AuditLog{
			EntityType: "products",
			EntityID:   "x",
			Action:     "create",
			UserID:     "u",
			CreatedAt:  now.Add(time.Duration(i) * time.Second),
		})
	}

	page1, _ := s.QueryDataLogs(ctx, "audit_logs", entry.DataFilter{Limit: 2})
	if len(page1) != 2 {
		t.Fatalf("page1: want 2, got %d", len(page1))
	}

	page2, _ := s.QueryDataLogs(ctx, "audit_logs", entry.DataFilter{Limit: 2, Offset: 2})
	if len(page2) != 2 {
		t.Fatalf("page2: want 2, got %d", len(page2))
	}
	if page1[0].ID == page2[0].ID {
		t.Fatalf("offset should skip page1 results, but page2 starts with same row")
	}
}

func TestMemory_QueryDateRange(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()

	_ = s.SaveDataLog(ctx, "audit_logs", entry.AuditLog{EntityType: "p", EntityID: "old", Action: "create", UserID: "u", CreatedAt: now.Add(-48 * time.Hour)})
	_ = s.SaveDataLog(ctx, "audit_logs", entry.AuditLog{EntityType: "p", EntityID: "new", Action: "create", UserID: "u", CreatedAt: now})

	got, _ := s.QueryDataLogs(ctx, "audit_logs", entry.DataFilter{
		DateFrom: now.Add(-24 * time.Hour),
	})
	if len(got) != 1 || got[0].EntityID != "new" {
		t.Fatalf("DateFrom filter failed: got %+v", got)
	}
}

func TestMemory_SaveAndQueryAPILog(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()

	row := entry.AuditAPILog{
		Service:    "stripe",
		Endpoint:   "/v1/charges",
		Method:     "POST",
		StatusCode: 200,
		CreatedAt:  now,
	}
	if err := s.SaveAPILog(ctx, "audit_api_logs", row); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.QueryAPILogs(ctx, "audit_api_logs", entry.APIFilter{Service: "stripe"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 1 || got[0].StatusCode != 200 {
		t.Fatalf("want 1 row with status 200, got %+v", got)
	}
}

func TestMemory_Purge(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()

	_ = s.SaveDataLog(ctx, "audit_logs", entry.AuditLog{EntityType: "p", EntityID: "old", Action: "create", UserID: "u", CreatedAt: now.Add(-48 * time.Hour)})
	_ = s.SaveDataLog(ctx, "audit_logs", entry.AuditLog{EntityType: "p", EntityID: "new", Action: "create", UserID: "u", CreatedAt: now})

	n, err := s.Purge(ctx, "audit_logs", now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 purged, got %d", n)
	}

	remaining, _ := s.QueryDataLogs(ctx, "audit_logs", entry.DataFilter{})
	if len(remaining) != 1 || remaining[0].EntityID != "new" {
		t.Fatalf("expected only fresh row to remain, got %+v", remaining)
	}
}

func TestMemory_Exec_NoOp(t *testing.T) {
	s := NewMemoryStore()
	if err := s.Exec(context.Background(), "CREATE TABLE foo"); err != nil {
		t.Fatalf("Exec should be a no-op, got error: %v", err)
	}
}
