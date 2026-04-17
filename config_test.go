package audit

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestValidate_RejectsNilUserFunc(t *testing.T) {
	_, err := NewWithStore(NewMemoryStore(), DialectFor(SQLite), Config{
		Dialect:   SQLite,
		DataAudit: DataAuditConfig{Enabled: true},
	})
	if err == nil || !strings.Contains(err.Error(), "UserFunc") {
		t.Fatalf("expected UserFunc error, got: %v", err)
	}
}

func TestValidate_RejectsBadTableName(t *testing.T) {
	cases := []string{
		"audit_logs; DROP TABLE users;--",
		"weird name",
		`"quoted"`,
		"",
	}
	// Empty string uses the default table, so craft a config that explicitly
	// sets it after applyDefaults has no chance to override — we assign a
	// tampered value.
	for _, bad := range cases[:3] {
		_, err := NewWithStore(NewMemoryStore(), DialectFor(SQLite), Config{
			Dialect:   SQLite,
			UserFunc:  func(context.Context) (string, string) { return "u", "user" },
			DataAudit: DataAuditConfig{Enabled: true, Table: bad},
		})
		if err == nil {
			t.Fatalf("expected error for table %q, got nil", bad)
		}
	}
}

func TestValidate_RejectsNoAuditEnabled(t *testing.T) {
	_, err := NewWithStore(NewMemoryStore(), DialectFor(SQLite), Config{Dialect: SQLite})
	if err == nil {
		t.Fatalf("expected error when nothing enabled")
	}
}

func TestValidate_AcceptsValid(t *testing.T) {
	_, err := NewWithStore(NewMemoryStore(), DialectFor(SQLite), Config{
		Dialect:   SQLite,
		UserFunc:  func(context.Context) (string, string) { return "u", "user" },
		DataAudit: DataAuditConfig{Enabled: true},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestErrorFailSilent_SwallowsAndLogs(t *testing.T) {
	var logged string
	a, err := NewWithStore(&errStore{Store: NewMemoryStore()}, DialectFor(SQLite), Config{
		Dialect:  SQLite,
		UserFunc: func(context.Context) (string, string) { return "u", "user" },
		Logger:   func(format string, args ...any) { logged = fmt.Sprintf(format, args...) },
		DataAudit: DataAuditConfig{
			Enabled: true,
			OnError: ErrorFailSilent,
		},
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	err = a.RecordDataChange(context.Background(), DataEntry{
		EntityType: "x", EntityID: "1", Action: ActionCreate,
		NewValues: map[string]any{"a": 1},
	})
	if err != nil {
		t.Fatalf("expected swallowed error, got %v", err)
	}
	if !strings.Contains(logged, "save data log") {
		t.Fatalf("expected log message, got %q", logged)
	}
}

// errStore wraps a real Store but always fails on SaveDataLog.
type errStore struct{ Store }

func (*errStore) SaveDataLog(context.Context, string, AuditLog) error {
	return context.DeadlineExceeded // arbitrary non-nil error
}
