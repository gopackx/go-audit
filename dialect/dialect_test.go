package dialect

import (
	"strings"
	"testing"
)

func TestFor(t *testing.T) {
	cases := []struct {
		in  Type
		out Type
		nil bool
	}{
		{PostgreSQL, PostgreSQL, false},
		{MySQL, MySQL, false},
		{SQLite, SQLite, false},
		{"unknown", "", true},
	}
	for _, c := range cases {
		got := For(c.in)
		if c.nil {
			if got != nil {
				t.Fatalf("expected nil for %q", c.in)
			}
			continue
		}
		if got.Name() != c.out {
			t.Fatalf("For(%q).Name() = %q, want %q", c.in, got.Name(), c.out)
		}
	}
}

func TestPostgresPlaceholder(t *testing.T) {
	d := PostgresDialect{}
	if got := d.Placeholder(3); got != "$3" {
		t.Fatalf("placeholder = %q, want $3", got)
	}
}

func TestMySQLPlaceholder(t *testing.T) {
	d := MySQLDialect{}
	if got := d.Placeholder(5); got != "?" {
		t.Fatalf("placeholder = %q, want ?", got)
	}
}

func TestSQLitePlaceholder(t *testing.T) {
	d := SQLiteDialect{}
	if got := d.Placeholder(2); got != "?" {
		t.Fatalf("placeholder = %q, want ?", got)
	}
}

func TestDataTableDDLContainsColumns(t *testing.T) {
	required := []string{"entity_type", "entity_id", "action", "old_values", "new_values", "user_id", "transaction_id"}
	for _, d := range []Dialect{PostgresDialect{}, MySQLDialect{}, SQLiteDialect{}} {
		sql := d.CreateDataTableSQL("audit_logs")
		for _, col := range required {
			if !strings.Contains(sql, col) {
				t.Errorf("%s DDL missing column %q", d.Name(), col)
			}
		}
	}
}

func TestAPITableDDLContainsColumns(t *testing.T) {
	required := []string{"service", "endpoint", "method", "status_code", "request_headers", "response_body", "duration_ms", "transaction_id"}
	for _, d := range []Dialect{PostgresDialect{}, MySQLDialect{}, SQLiteDialect{}} {
		sql := d.CreateAPITableSQL("audit_api_logs")
		for _, col := range required {
			if !strings.Contains(sql, col) {
				t.Errorf("%s API DDL missing column %q", d.Name(), col)
			}
		}
	}
}

func TestPostgresIndexes_HasGIN(t *testing.T) {
	d := PostgresDialect{}
	indexes := d.CreateIndexesSQL("audit_logs")
	var hasGIN bool
	for _, idx := range indexes {
		if strings.Contains(idx, "USING GIN") {
			hasGIN = true
			break
		}
	}
	if !hasGIN {
		t.Fatalf("expected at least one GIN index in postgres data indexes")
	}
}

func TestMySQLIndexes_DeclaredInline(t *testing.T) {
	d := MySQLDialect{}
	if got := d.CreateIndexesSQL("audit_logs"); got != nil {
		t.Fatalf("expected nil (inline indexes), got %v", got)
	}
	if got := d.CreateAPIIndexesSQL("audit_api_logs"); got != nil {
		t.Fatalf("expected nil (inline indexes), got %v", got)
	}
}

func TestRegister_Override(t *testing.T) {
	original := For(SQLite)
	t.Cleanup(func() { Register(SQLite, original) })

	custom := PostgresDialect{} // any non-nil dialect, doesn't matter which
	Register(SQLite, custom)
	got := For(SQLite)
	if _, ok := got.(PostgresDialect); !ok {
		t.Fatalf("expected override to PostgresDialect, got %T", got)
	}
}

func TestRegister_NilIgnored(t *testing.T) {
	before := For(PostgreSQL)
	Register(PostgreSQL, nil)
	after := For(PostgreSQL)
	if before != after {
		t.Fatalf("registering nil should not change registry")
	}
}
