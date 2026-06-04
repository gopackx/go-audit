package main

import (
	// Pure-Go drivers so the MCP server compiles without cgo.
	_ "github.com/jackc/pgx/v5/stdlib" // postgres
	_ "github.com/go-sql-driver/mysql" // mysql
	_ "modernc.org/sqlite"             // sqlite

	audit "github.com/gopackx/go-audit"
)

// driverNameFor maps an audit.DialectType to the database/sql driver name
// registered by the blank imports above. Kept in one place so adding a new
// dialect / driver is a single-file change.
func driverNameFor(d audit.DialectType) string {
	switch d {
	case audit.PostgreSQL:
		return "pgx"
	case audit.MySQL:
		return "mysql"
	case audit.SQLite:
		return "sqlite"
	default:
		return ""
	}
}
