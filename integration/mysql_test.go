package integration

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gopackx/go-audit"
)

// TestMySQL runs the shared audit suite against MySQL. Requires a reachable
// server; set GO_AUDIT_MYSQL_DSN (e.g.
// "audit:audit@tcp(localhost:3306)/audit?parseTime=true") to enable.
func TestMySQL(t *testing.T) {
	dsn := os.Getenv("GO_AUDIT_MYSQL_DSN")
	if dsn == "" {
		t.Skip("GO_AUDIT_MYSQL_DSN not set; skipping")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping mysql: %v", err)
	}

	runDialectSuite(t, db, audit.MySQL)
}
