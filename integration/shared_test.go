package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/gopackx/go-audit"
)

// newAuditorForDialect builds an auditor for the given dialect against an open DB.
func newAuditorForDialect(t *testing.T, db *sql.DB, dialect audit.DialectType) audit.Auditor {
	t.Helper()
	a, err := audit.New(db, audit.Config{
		Dialect: dialect,
		UserFunc: func(ctx context.Context) (string, string) {
			return "alice", "user"
		},
		DataAudit: audit.DataAuditConfig{
			Enabled:       true,
			ExcludeFields: []string{"password"},
		},
		APIAudit: audit.APIAuditConfig{
			Enabled:          true,
			RedactHeaders:    []string{"Authorization"},
			RedactBodyFields: []string{"secret"},
		},
	})
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	if err := a.AutoMigrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return a
}

// runDialectSuite exercises the full data + API + transaction flow against
// whatever dialect / driver the caller wires up.
func runDialectSuite(t *testing.T, db *sql.DB, dialect audit.DialectType) {
	t.Helper()

	ctx := context.Background()

	t.Run("DataAudit", func(t *testing.T) {
		resetTables(t, db, dialect)
		a := newAuditorForDialect(t, db, dialect)

		if err := a.RecordDataChange(ctx, audit.DataEntry{
			EntityType: "products", EntityID: "42", Action: audit.ActionCreate,
			NewValues: map[string]any{"name": "Widget", "price": 100, "password": "secret"},
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := a.RecordDataChange(ctx, audit.DataEntry{
			EntityType: "products", EntityID: "42", Action: audit.ActionUpdate,
			OldValues: map[string]any{"name": "Widget", "price": 100},
			NewValues: map[string]any{"name": "Widget", "price": 150},
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		if err := a.RecordDataChange(ctx, audit.DataEntry{
			EntityType: "products", EntityID: "42", Action: audit.ActionDelete,
			OldValues: map[string]any{"name": "Widget", "price": 150},
		}); err != nil {
			t.Fatalf("delete: %v", err)
		}

		logs, err := a.Query(ctx, audit.DataFilter{EntityType: "products", EntityID: "42"})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if len(logs) != 3 {
			t.Fatalf("want 3 logs, got %d", len(logs))
		}
		if logs[0].Action != audit.ActionDelete || logs[2].Action != audit.ActionCreate {
			t.Fatalf("unexpected ordering: %s / %s / %s",
				logs[0].Action, logs[1].Action, logs[2].Action)
		}
		for _, l := range logs {
			for _, raw := range [][]byte{l.OldValues, l.NewValues} {
				if raw == nil {
					continue
				}
				var m map[string]any
				_ = json.Unmarshal(raw, &m)
				if _, ok := m["password"]; ok {
					t.Fatalf("password leaked: %s", raw)
				}
			}
		}
	})

	t.Run("APIAudit", func(t *testing.T) {
		resetTables(t, db, dialect)
		a := newAuditorForDialect(t, db, dialect)

		if err := a.API().Record(ctx, audit.APIEntry{
			Service: "bca", Endpoint: "/api/v1/transfer", Method: "POST", StatusCode: 200,
			RequestHeaders: map[string]string{"Authorization": "Bearer x", "X-Trace": "t1"},
			RequestBody:    map[string]any{"secret": "leakme", "amount": 500},
			ResponseBody:   map[string]any{"ok": true},
			DurationMs:     42,
		}); err != nil {
			t.Fatalf("api record: %v", err)
		}
		logs, err := a.API().Query(ctx, audit.APIFilter{Service: "bca"})
		if err != nil {
			t.Fatalf("api query: %v", err)
		}
		if len(logs) != 1 {
			t.Fatalf("want 1 api log, got %d", len(logs))
		}
		var headers map[string]string
		_ = json.Unmarshal(logs[0].RequestHeaders, &headers)
		if headers["Authorization"] == "Bearer x" {
			t.Fatalf("Authorization should be redacted")
		}
		var body map[string]any
		_ = json.Unmarshal(logs[0].RequestBody, &body)
		if body["secret"] == "leakme" {
			t.Fatalf("secret should be redacted")
		}
	})

	t.Run("QueryByTransaction", func(t *testing.T) {
		resetTables(t, db, dialect)
		a := newAuditorForDialect(t, db, dialect)
		txID := "tx-int-" + string(dialect)
		tctx := audit.WithTransactionID(ctx, txID)
		_ = a.RecordDataChange(tctx, audit.DataEntry{
			EntityType: "orders", EntityID: "o-1", Action: audit.ActionCreate,
			NewValues: map[string]any{"total": 1000},
		})
		_ = a.API().Record(tctx, audit.APIEntry{
			Service: "stripe", Endpoint: "/charges", Method: "POST", StatusCode: 200,
		})
		out, err := a.QueryByTransaction(tctx, txID)
		if err != nil {
			t.Fatalf("QueryByTransaction: %v", err)
		}
		if len(out.DataLogs) != 1 || len(out.APILogs) != 1 {
			t.Fatalf("want 1+1, got %d+%d", len(out.DataLogs), len(out.APILogs))
		}
	})
}

// resetTables drops and recreates the audit tables so each subtest starts
// from a clean slate. For in-memory SQLite the tables already start empty,
// but Postgres / MySQL persist across connections.
func resetTables(t *testing.T, db *sql.DB, dialect audit.DialectType) {
	t.Helper()
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS audit_logs",
		"DROP TABLE IF EXISTS audit_api_logs",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("reset (%s): %v", stmt, err)
		}
	}
}
