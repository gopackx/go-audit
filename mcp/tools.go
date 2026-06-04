package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	audit "github.com/gopackx/go-audit"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerTools wires every MCP tool the server exposes. Keep tool
// descriptions specific — the AI uses them to decide *when* to call.
func registerTools(srv *server.MCPServer, a audit.Auditor) {
	srv.AddTool(queryDataLogsTool(), queryDataLogs(a))
	srv.AddTool(queryAPILogsTool(), queryAPILogs(a))
	srv.AddTool(queryByTransactionTool(), queryByTransaction(a))
	srv.AddTool(snapshotEntityTool(), snapshotEntity(a))
	srv.AddTool(recentChangesTool(), recentChanges(a))
}

// ---------- query_data_logs ----------

func queryDataLogsTool() mcp.Tool {
	return mcp.NewTool("query_data_logs",
		mcp.WithDescription(
			"Search the data-change audit log (audit_logs). Use to answer "+
				"questions like 'who changed product 42 last week', 'show every "+
				"delete on users in March', or 'list all updates by user_id=alice'. "+
				"Returns rows ordered newest-first. All filters are optional but "+
				"combinable — narrow as much as possible to keep responses small.",
		),
		mcp.WithString("entity_type", mcp.Description("Table / entity name (e.g. 'products', 'orders').")),
		mcp.WithString("entity_id", mcp.Description("Primary key value as string (compound PKs are JSON-encoded).")),
		mcp.WithString("action", mcp.Description("One of: create | update | delete | soft_delete | restore.")),
		mcp.WithString("user_id", mcp.Description("Actor that performed the change.")),
		mcp.WithString("transaction_id", mcp.Description("Transaction ID linking related changes.")),
		mcp.WithString("date_from", mcp.Description("RFC3339 lower bound on created_at (inclusive).")),
		mcp.WithString("date_to", mcp.Description("RFC3339 upper bound on created_at (inclusive).")),
		mcp.WithNumber("limit", mcp.Description("Max rows to return (default 50, hard cap 500).")),
		mcp.WithNumber("offset", mcp.Description("Skip first N rows for pagination.")),
	)
}

func queryDataLogs(a audit.Auditor) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		f := audit.DataFilter{
			EntityType:    str(req, "entity_type"),
			EntityID:      str(req, "entity_id"),
			Action:        str(req, "action"),
			UserID:        str(req, "user_id"),
			TransactionID: str(req, "transaction_id"),
			Limit:         clampLimit(intVal(req, "limit")),
			Offset:        intVal(req, "offset"),
		}
		if v, ok := timeVal(req, "date_from"); ok {
			f.DateFrom = v
		}
		if v, ok := timeVal(req, "date_to"); ok {
			f.DateTo = v
		}
		logs, err := a.Query(ctx, f)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{
			"count": len(logs),
			"logs":  logs,
		})
	}
}

// ---------- query_api_logs ----------

func queryAPILogsTool() mcp.Tool {
	return mcp.NewTool("query_api_logs",
		mcp.WithDescription(
			"Search the outbound API call log (audit_api_logs). Use to answer "+
				"questions like 'list every failing call to the bca service', "+
				"'show payment calls on 2026-04-13', or 'which API calls happened "+
				"inside transaction X'. Returns rows ordered newest-first.",
		),
		mcp.WithString("service", mcp.Description("Service name (e.g. 'bca', 'stripe').")),
		mcp.WithNumber("status_code", mcp.Description("Exact HTTP status code to filter on.")),
		mcp.WithString("user_id", mcp.Description("Actor that triggered the call.")),
		mcp.WithString("transaction_id", mcp.Description("Transaction ID linking related calls/changes.")),
		mcp.WithString("date_from", mcp.Description("RFC3339 lower bound on created_at.")),
		mcp.WithString("date_to", mcp.Description("RFC3339 upper bound on created_at.")),
		mcp.WithNumber("limit", mcp.Description("Max rows (default 50, cap 500).")),
		mcp.WithNumber("offset", mcp.Description("Pagination offset.")),
	)
}

func queryAPILogs(a audit.Auditor) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		f := audit.APIFilter{
			Service:       str(req, "service"),
			StatusCode:    intVal(req, "status_code"),
			UserID:        str(req, "user_id"),
			TransactionID: str(req, "transaction_id"),
			Limit:         clampLimit(intVal(req, "limit")),
			Offset:        intVal(req, "offset"),
		}
		if v, ok := timeVal(req, "date_from"); ok {
			f.DateFrom = v
		}
		if v, ok := timeVal(req, "date_to"); ok {
			f.DateTo = v
		}
		logs, err := a.API().Query(ctx, f)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{
			"count": len(logs),
			"logs":  logs,
		})
	}
}

// ---------- query_by_transaction ----------

func queryByTransactionTool() mcp.Tool {
	return mcp.NewTool("query_by_transaction",
		mcp.WithDescription(
			"Return every data change AND outbound API call recorded under one "+
				"transaction_id. The canonical way to reconstruct the full story "+
				"of one business operation (e.g. 'what happened during checkout "+
				"transaction X').",
		),
		mcp.WithString("transaction_id",
			mcp.Description("Transaction ID to look up."),
			mcp.Required(),
		),
	)
}

func queryByTransaction(a audit.Auditor) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		txID := str(req, "transaction_id")
		if txID == "" {
			return mcp.NewToolResultError("transaction_id is required"), nil
		}
		out, err := a.QueryByTransaction(ctx, txID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(out)
	}
}

// ---------- snapshot_entity ----------

func snapshotEntityTool() mcp.Tool {
	return mcp.NewTool("snapshot_entity",
		mcp.WithDescription(
			"Reconstruct the state of an entity at a point in time by replaying "+
				"its audit log up to that timestamp. Returns the field map (the "+
				"row as it looked then) or null if the entity didn't exist or was "+
				"deleted at that point. Use to answer 'what did order #42 look "+
				"like before yesterday's incident'.",
		),
		mcp.WithString("entity_type",
			mcp.Description("Table / entity name."),
			mcp.Required(),
		),
		mcp.WithString("entity_id",
			mcp.Description("Primary key value as string."),
			mcp.Required(),
		),
		mcp.WithString("at",
			mcp.Description("RFC3339 timestamp to snapshot at."),
			mcp.Required(),
		),
	)
}

func snapshotEntity(a audit.Auditor) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		et := str(req, "entity_type")
		eid := str(req, "entity_id")
		at, ok := timeVal(req, "at")
		if et == "" || eid == "" || !ok {
			return mcp.NewToolResultError("entity_type, entity_id, and RFC3339 'at' are all required"), nil
		}
		state, err := a.Snapshot(ctx, et, eid, at)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{
			"entity_type": et,
			"entity_id":   eid,
			"at":          at.Format(time.RFC3339),
			"state":       state, // nil → was deleted / never existed
		})
	}
}

// ---------- recent_changes ----------

func recentChangesTool() mcp.Tool {
	return mcp.NewTool("recent_changes",
		mcp.WithDescription(
			"Convenience tool: return the N most recent data-change audit rows, "+
				"optionally filtered to one entity_type. Useful for 'what's been "+
				"happening lately' / activity-feed queries without making the "+
				"caller construct a filter object.",
		),
		mcp.WithString("entity_type", mcp.Description("Optional table / entity name to scope to.")),
		mcp.WithNumber("limit", mcp.Description("Number of rows (default 20, cap 200).")),
	)
}

func recentChanges(a audit.Auditor) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		limit := intVal(req, "limit")
		if limit <= 0 {
			limit = 20
		}
		if limit > 200 {
			limit = 200
		}
		logs, err := a.Query(ctx, audit.DataFilter{
			EntityType: str(req, "entity_type"),
			Limit:      limit,
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{
			"count": len(logs),
			"logs":  logs,
		})
	}
}

// ---------- helpers ----------

// str returns the named string argument or "".
func str(req mcp.CallToolRequest, name string) string {
	if v, ok := req.Params.Arguments[name]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// intVal returns the named numeric argument coerced to int (JSON numbers
// arrive as float64 over the MCP wire).
func intVal(req mcp.CallToolRequest, name string) int {
	if v, ok := req.Params.Arguments[name]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

// timeVal parses an RFC3339 timestamp argument. Returns (zero, false) when
// the argument is missing or unparseable.
func timeVal(req mcp.CallToolRequest, name string) (time.Time, bool) {
	s := str(req, name)
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// clampLimit caps user-supplied limits so a careless 1_000_000 doesn't blow
// up the context window of the calling AI.
func clampLimit(n int) int {
	switch {
	case n <= 0:
		return 50
	case n > 500:
		return 500
	}
	return n
}

// jsonResult marshals v as a JSON text result. MCP tool results are an array
// of content items; one text item with the serialized payload is the most
// portable shape across hosts.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}
