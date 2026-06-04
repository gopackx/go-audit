// Command audit-mcp is a Model Context Protocol server that exposes go-audit
// query primitives (data logs, API logs, transaction view, snapshot) as MCP
// tools so AI agents (Claude Code, Claude.ai, etc.) can investigate audit
// history conversationally.
//
// Configuration is entirely via environment variables — see config.go and the
// adjacent README. The server speaks stdio MCP, which is the transport every
// supported host uses.
package main

import (
	"context"
	"fmt"
	"os"

	audit "github.com/gopackx/go-audit"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	if err := run(); err != nil {
		// stdio MCP servers must keep stdout clean — diagnostics go to stderr.
		fmt.Fprintf(os.Stderr, "audit-mcp: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	db, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	auditor, err := audit.New(db, audit.Config{
		Dialect: cfg.Dialect,
		// Querying-only — DataAudit / APIAudit "Enabled" doesn't gate Query
		// in the auditor, only Record / Migrate, but we still enable both
		// here so QueryByTransaction loads both halves.
		DataAudit: audit.DataAuditConfig{
			Enabled: true,
			Table:   cfg.DataTable,
		},
		APIAudit: audit.APIAuditConfig{
			Enabled: true,
			Table:   cfg.APITable,
		},
		// The MCP server is read-only; UserFunc is never invoked by Query*.
		// We still need it set because audit.New validates it when
		// DataAudit.Enabled is true.
		UserFunc: func(ctx context.Context) (string, string) { return "", "" },
	})
	if err != nil {
		return fmt.Errorf("init auditor: %w", err)
	}

	srv := server.NewMCPServer(
		"go-audit",
		"1.0.0",
		server.WithToolCapabilities(false),
		server.WithLogging(),
	)
	registerTools(srv, auditor)

	// stdio transport — what Claude Code and Claude.ai both use for MCP.
	return server.ServeStdio(srv)
}
