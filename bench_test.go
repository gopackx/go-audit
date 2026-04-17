package audit

import (
	"context"
	"testing"
)

func benchAuditor(b *testing.B) Auditor {
	b.Helper()
	a, err := NewWithStore(NewMemoryStore(), DialectFor(SQLite), Config{
		Dialect:  SQLite,
		UserFunc: func(ctx context.Context) (string, string) { return "bench-user", "user" },
		DataAudit: DataAuditConfig{
			Enabled:       true,
			ExcludeFields: []string{"password", "secret"},
		},
		APIAudit: APIAuditConfig{
			Enabled:          true,
			RedactHeaders:    []string{"Authorization", "X-Api-Key"},
			RedactBodyFields: []string{"token", "password"},
		},
	})
	if err != nil {
		b.Fatalf("NewWithStore: %v", err)
	}
	return a
}

func sampleOld() map[string]any {
	return map[string]any{
		"id":         1,
		"name":       "Alice",
		"email":      "alice@example.com",
		"age":        30,
		"role":       "admin",
		"active":     true,
		"department": "engineering",
		"level":      5,
		"salary":     100000,
		"created_at": "2025-01-01T00:00:00Z",
	}
}

func sampleNew() map[string]any {
	return map[string]any{
		"id":         1,
		"name":       "Alice Smith",
		"email":      "alice@example.com",
		"age":        31,
		"role":       "superadmin",
		"active":     true,
		"department": "engineering",
		"level":      5,
		"salary":     100000,
		"created_at": "2025-01-01T00:00:00Z",
	}
}

func BenchmarkDiffResult_Update(b *testing.B) {
	exclude := []string{"password", "secret"}
	old := sampleOld()
	nw := sampleNew()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		diffResult(ActionUpdate, old, nw, exclude)
	}
}

func BenchmarkDiffResult_Create(b *testing.B) {
	exclude := []string{"password", "secret"}
	nw := sampleNew()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		diffResult(ActionCreate, nil, nw, exclude)
	}
}

func BenchmarkRecordDataChange_Create(b *testing.B) {
	a := benchAuditor(b)
	ctx := context.Background()
	entry := DataEntry{
		EntityType: "users",
		EntityID:   "42",
		Action:     ActionCreate,
		NewValues:  sampleNew(),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := a.RecordDataChange(ctx, entry); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRecordDataChange_Update(b *testing.B) {
	a := benchAuditor(b)
	ctx := context.Background()
	entry := DataEntry{
		EntityType: "users",
		EntityID:   "42",
		Action:     ActionUpdate,
		OldValues:  sampleOld(),
		NewValues:  sampleNew(),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := a.RecordDataChange(ctx, entry); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAPIRecord(b *testing.B) {
	a := benchAuditor(b)
	ctx := context.Background()
	entry := APIEntry{
		Service:    "payment-gateway",
		Endpoint:   "https://api.example.com/v1/charge",
		Method:     "POST",
		StatusCode: 200,
		RequestHeaders: map[string]string{
			"Authorization": "Bearer eyJhbGciOiJIUzI1NiJ9.test",
			"Content-Type":  "application/json",
			"X-Api-Key":     "sk-live-abc123",
			"Accept":        "application/json",
		},
		RequestBody: map[string]any{
			"amount":   50000,
			"currency": "IDR",
			"token":    "tok_visa_4242",
			"metadata": map[string]any{"order_id": "ORD-001"},
		},
		ResponseBody: map[string]any{
			"id":       "ch_abc123",
			"status":   "succeeded",
			"password": "should-be-redacted",
		},
		DurationMs: 230,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := a.API().Record(ctx, entry); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNewTransactionID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = NewTransactionID()
	}
}
