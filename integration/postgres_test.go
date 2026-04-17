package integration

import (
	"database/sql"
	"os"
	"testing"

	"github.com/gopackx/go-audit"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestPostgres runs the shared audit suite against Postgres. Requires a
// reachable server; set GO_AUDIT_POSTGRES_DSN (e.g.
// "postgres://audit:audit@localhost:5432/audit?sslmode=disable") to enable.
func TestPostgres(t *testing.T) {
	dsn := os.Getenv("GO_AUDIT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("GO_AUDIT_POSTGRES_DSN not set; skipping")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}

	runDialectSuite(t, db, audit.PostgreSQL)
}
