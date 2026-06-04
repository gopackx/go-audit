package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"

	audit "github.com/gopackx/go-audit"
)

// envConfig holds the connection details the MCP server needs to open the
// audit store. All values come from the process environment so the server
// can be configured via the standard Claude Code MCP `env` block.
type envConfig struct {
	Dialect   audit.DialectType
	DSN       string
	DataTable string
	APITable  string
}

const (
	envDialect   = "GOAUDIT_DIALECT"
	envDSN       = "GOAUDIT_DSN"
	envDataTable = "GOAUDIT_DATA_TABLE"
	envAPITable  = "GOAUDIT_API_TABLE"
)

// loadConfig reads required and optional env vars and returns a usable
// envConfig or a descriptive error. The error messages double as the
// "first-run" docs the user sees if they forget a value.
func loadConfig() (envConfig, error) {
	cfg := envConfig{
		DataTable: os.Getenv(envDataTable),
		APITable:  os.Getenv(envAPITable),
	}
	if cfg.DataTable == "" {
		cfg.DataTable = "audit_logs"
	}
	if cfg.APITable == "" {
		cfg.APITable = "audit_api_logs"
	}

	switch d := os.Getenv(envDialect); d {
	case "postgres", "postgresql":
		cfg.Dialect = audit.PostgreSQL
	case "mysql":
		cfg.Dialect = audit.MySQL
	case "sqlite", "sqlite3":
		cfg.Dialect = audit.SQLite
	case "":
		return cfg, fmt.Errorf("%s is required (postgres | mysql | sqlite)", envDialect)
	default:
		return cfg, fmt.Errorf("%s=%q is not a recognised dialect (use postgres | mysql | sqlite)", envDialect, d)
	}

	cfg.DSN = os.Getenv(envDSN)
	if cfg.DSN == "" {
		return cfg, fmt.Errorf("%s is required (driver-specific connection string)", envDSN)
	}
	return cfg, nil
}

// openDB returns an *sql.DB for the configured dialect. The MCP server
// intentionally relies on a stdlib driver being import-side-effect-registered
// somewhere in the binary (see drivers_*.go build tag files). If the user
// builds with an unsupported driver tag combo, the error here points them at
// the README.
func openDB(cfg envConfig) (*sql.DB, error) {
	driver := driverNameFor(cfg.Dialect)
	if driver == "" {
		return nil, errors.New("no database driver compiled into this binary for the requested dialect")
	}
	db, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("sql.Open(%s): %w", driver, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("db ping failed: %w", err)
	}
	return db, nil
}
