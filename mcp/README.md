# audit-mcp — MCP server for go-audit

An [MCP](https://modelcontextprotocol.io) server that exposes go-audit's
query primitives as tools so AI assistants (Claude Code, Claude.ai,
Cursor, etc.) can investigate audit history conversationally.

> "Tell me everything that happened during transaction
> `20260413T090000-...`" → the AI calls `query_by_transaction` and
> summarizes the result.
>
> "What did order #42 look like before yesterday's deploy?" → the AI
> calls `snapshot_entity` with `at=<deploy timestamp>` and reads back
> the field map.

## Tools

| Tool                    | Purpose                                                                                |
| ----------------------- | -------------------------------------------------------------------------------------- |
| `query_data_logs`       | Filtered search of `audit_logs` (entity, action, user, date range, transaction).      |
| `query_api_logs`        | Filtered search of `audit_api_logs` (service, status, user, date range, transaction). |
| `query_by_transaction`  | Combined data + API logs for one `transaction_id`.                                     |
| `snapshot_entity`       | Reconstruct an entity's state at a point in time (replay).                            |
| `recent_changes`        | Activity-feed shortcut — last N changes, optionally per entity_type.                  |

All query tools return JSON; row counts are capped (default 50, max 500)
to keep results in the AI's context window.

## Install

```bash
# Installs the binary as `mcp` (Go names it after the last element of the
# import path). Requires mcp/v1.1.1 or later -- see Versioning below.
go install github.com/gopackx/go-audit/mcp@latest

# Rename for clarity (optional). `go install` writes to $GOBIN, or to
# $GOPATH/bin when GOBIN is unset:
BIN="$(go env GOBIN)"; BIN="${BIN:-$(go env GOPATH)/bin}"
mv "$BIN/mcp" "$BIN/audit-mcp"   # on Windows the file is mcp.exe

# Or build from a clone with the name you want:
git clone https://github.com/gopackx/go-audit && cd go-audit/mcp
go build -o audit-mcp .
```

The binary embeds pure-Go drivers for PostgreSQL (pgx), MySQL, and
SQLite (`modernc.org/sqlite`), so no cgo / no native libs required.

## Configure

The server connects to the **same database** that holds the audit
tables your app writes to. Configuration is via environment variables:

| Var                    | Required | Description                                                                  |
| ---------------------- | -------- | ---------------------------------------------------------------------------- |
| `GOAUDIT_DIALECT`      | yes      | `postgres` \| `mysql` \| `sqlite`                                            |
| `GOAUDIT_DSN`          | yes      | Driver-specific connection string (see below)                                |
| `GOAUDIT_DATA_TABLE`   | no       | Override the data-audit table name (default `audit_logs`)                    |
| `GOAUDIT_API_TABLE`    | no       | Override the API-audit table name (default `audit_api_logs`)                 |

DSN examples:

```
# PostgreSQL (pgx)
postgres://audit_reader:secret@db.internal:5432/myapp?sslmode=require

# MySQL
audit_reader:secret@tcp(db.internal:3306)/myapp?parseTime=true

# SQLite
file:/var/lib/myapp/audit.db?mode=ro
```

**Recommendation:** point this at a **read-only** DB user. The MCP
server never writes; granting only `SELECT` on the two audit tables
gives a minimal blast radius if the AI host is compromised.

## Wire it into Claude Code

Add to `~/.claude/mcp.json` (or your project's `.mcp.json`):

```json
{
  "mcpServers": {
    "go-audit": {
      "command": "audit-mcp",
      "env": {
        "GOAUDIT_DIALECT": "postgres",
        "GOAUDIT_DSN": "postgres://audit_reader:secret@db.internal:5432/myapp?sslmode=require"
      }
    }
  }
}
```

Restart Claude Code, and the five tools above appear in tool listings.
Then you can ask things like:

- "Show me every update to `orders` for user `alice` in the last 24
  hours."
- "Did anything change on product 42 between 10:00 and 11:00 today?
  If so, show the diff."
- "Give me the full transaction view for `<tx-id>`."
- "Reconstruct what user 17 looked like at 2026-04-01T00:00:00Z."

## Wire it into Claude.ai (Desktop)

Same JSON, in the Desktop app's MCP settings panel. Look for "Edit
Config" → paste the `mcpServers` block above.

## Wire it into Cursor

Cursor reads `~/.cursor/mcp.json` with the same shape.

## Smoke test

Confirm the binary handshakes properly without touching a real DB by
pointing it at an in-memory SQLite:

```bash
GOAUDIT_DIALECT=sqlite GOAUDIT_DSN=":memory:" ./audit-mcp <<EOF
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
EOF
```

You should see two JSON-RPC response lines on stdout, the second
listing all five tools.

## Read-only by design

This MCP server **never** issues `INSERT`, `UPDATE`, `DELETE`, or
`ALTER`. The exposed tools call only `auditor.Query`,
`auditor.API().Query`, `auditor.QueryByTransaction`, and
`auditor.Snapshot`. `auditor.Purge` and `auditor.Restore` are
intentionally not exposed — destructive operations belong in your
app's own admin tooling, not in an AI tool surface.

## Versioning

Tagged independently from the core as `mcp/vX.Y.Z`. Install a specific
version with:

```bash
go install github.com/gopackx/go-audit/mcp@v1.1.1
```

> **Note:** `mcp/v1.1.0` (and earlier) cannot be installed with
> `go install`: its `go.mod` carries a `replace` directive pointing at the
> local checkout, which `go install pkg@version` rejects. Use `mcp/v1.1.1`
> or later.

### Local development

`mcp/go.mod` requires a **published** go-audit release (no `replace`
directive), so a plain `go build` inside `mcp/` compiles against that
release from the module proxy, not against the core in your checkout.
To try the server against unreleased core changes, add a temporary
replace and drop it again before committing:

```bash
cd mcp
go mod edit -replace github.com/gopackx/go-audit=../
go build -o audit-mcp .
go mod edit -dropreplace github.com/gopackx/go-audit && go mod tidy
```

The repo deliberately has no committed `go.work`: a workspace would merge
the dependency graphs of every module (and force the newest module's Go
version on all of them), so per-module tests would no longer reflect each
module's own `go.mod`.
