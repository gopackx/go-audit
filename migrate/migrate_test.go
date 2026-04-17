package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/gopackx/go-audit/dialect"
	"github.com/gopackx/go-audit/store"
)

// recordingStore wraps a real Store and captures every Exec call so tests can
// assert which DDL statements Migrate emitted.
type recordingStore struct {
	store.Store
	statements []string
}

func (r *recordingStore) Exec(ctx context.Context, stmt string) error {
	r.statements = append(r.statements, stmt)
	return nil
}

func newRecording() *recordingStore {
	return &recordingStore{Store: store.NewMemoryStore()}
}

func TestMigrate_BothEnabled_EmitsBothTables(t *testing.T) {
	rs := newRecording()
	err := Migrate(context.Background(), rs, dialect.SQLiteDialect{}, Config{
		DataTable:   "audit_logs",
		APITable:    "audit_api_logs",
		DataEnabled: true,
		APIEnabled:  true,
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	joined := strings.Join(rs.statements, "\n")
	if !strings.Contains(joined, "CREATE TABLE IF NOT EXISTS audit_logs") {
		t.Errorf("missing data table DDL")
	}
	if !strings.Contains(joined, "CREATE TABLE IF NOT EXISTS audit_api_logs") {
		t.Errorf("missing api table DDL")
	}
	if !strings.Contains(joined, "CREATE INDEX IF NOT EXISTS idx_audit_logs_entity") {
		t.Errorf("missing data table indexes")
	}
}

func TestMigrate_DataOnly_SkipsAPITable(t *testing.T) {
	rs := newRecording()
	err := Migrate(context.Background(), rs, dialect.SQLiteDialect{}, Config{
		DataTable:   "audit_logs",
		APITable:    "audit_api_logs",
		DataEnabled: true,
		APIEnabled:  false,
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, s := range rs.statements {
		if strings.Contains(s, "audit_api_logs") {
			t.Errorf("API table should not be created when APIEnabled=false: %q", s)
		}
	}
}

func TestMigrate_APIOnly_SkipsDataTable(t *testing.T) {
	rs := newRecording()
	err := Migrate(context.Background(), rs, dialect.SQLiteDialect{}, Config{
		DataTable:   "audit_logs",
		APITable:    "audit_api_logs",
		DataEnabled: false,
		APIEnabled:  true,
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, s := range rs.statements {
		if strings.Contains(s, " audit_logs ") || strings.HasSuffix(s, " audit_logs") {
			t.Errorf("data table should not be created when DataEnabled=false: %q", s)
		}
	}
}

func TestMigrate_NothingEnabled_NoOp(t *testing.T) {
	rs := newRecording()
	err := Migrate(context.Background(), rs, dialect.SQLiteDialect{}, Config{
		DataTable: "audit_logs",
		APITable:  "audit_api_logs",
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(rs.statements) != 0 {
		t.Errorf("expected no statements, got %d: %v", len(rs.statements), rs.statements)
	}
}

func TestMigrate_MySQL_DeclaresIndexesInline(t *testing.T) {
	// MySQL CreateIndexesSQL returns nil because indexes go inline in CREATE
	// TABLE — Migrate should still emit the CREATE TABLE without erroring.
	rs := newRecording()
	err := Migrate(context.Background(), rs, dialect.MySQLDialect{}, Config{
		DataTable:   "audit_logs",
		APITable:    "audit_api_logs",
		DataEnabled: true,
		APIEnabled:  true,
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Two CREATE TABLE statements, no separate CREATE INDEX statements.
	if len(rs.statements) != 2 {
		t.Errorf("expected 2 statements (both CREATE TABLE), got %d", len(rs.statements))
	}
	for _, s := range rs.statements {
		if !strings.Contains(s, "CREATE TABLE") {
			t.Errorf("expected CREATE TABLE statement, got %q", s)
		}
	}
}
