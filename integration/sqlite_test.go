package integration

import (
	"database/sql"
	"testing"

	"github.com/gopackx/go-audit"
	_ "modernc.org/sqlite"
)

func TestSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	runDialectSuite(t, db, audit.SQLite)
}
