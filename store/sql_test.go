package store

import "testing"

func TestIdentRE_Accepts(t *testing.T) {
	good := []string{"audit_logs", "AuditLogs", "_internal", "x", "a1_b2"}
	for _, s := range good {
		if !identRE.MatchString(s) {
			t.Errorf("expected %q to match identRE", s)
		}
	}
}

func TestIdentRE_Rejects(t *testing.T) {
	bad := []string{
		"",
		"1audit",
		"audit logs",
		"audit-logs",
		"audit;DROP",
		`"audit"`,
		"audit\x00logs",
	}
	for _, s := range bad {
		if identRE.MatchString(s) {
			t.Errorf("expected %q to be rejected by identRE", s)
		}
	}
}

func TestNullableJSON(t *testing.T) {
	if v := nullableJSON(nil); v != nil {
		t.Errorf("nil -> nil, got %v", v)
	}
	if v := nullableJSON([]byte{}); v != nil {
		t.Errorf("empty -> nil, got %v", v)
	}
	if v := nullableJSON([]byte(`{"a":1}`)); v != `{"a":1}` {
		t.Errorf("non-empty -> string, got %v", v)
	}
}

func TestNullableString(t *testing.T) {
	if v := nullableString(""); v != nil {
		t.Errorf("empty -> nil, got %v", v)
	}
	if v := nullableString("hello"); v != "hello" {
		t.Errorf("non-empty -> string, got %v", v)
	}
}
