package bun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gopackx/go-audit"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite"
)

type Product struct {
	bun.BaseModel `bun:"table:products"`

	ID    int64  `bun:",pk,autoincrement"`
	Name  string `bun:"name"`
	Price int    `bun:"price"`
}

func newBunDB(t *testing.T) *bun.DB {
	t.Helper()
	sqldb, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })
	db := bun.NewDB(sqldb, sqlitedialect.New())
	if _, err := db.NewCreateTable().Model((*Product)(nil)).Exec(context.Background()); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func newAuditor(t *testing.T) audit.Auditor {
	t.Helper()
	a, err := audit.NewWithStore(audit.NewMemoryStore(), noopDialect{}, audit.Config{
		Dialect:   audit.SQLite,
		UserFunc:  func(context.Context) (string, string) { return "tester", "user" },
		DataAudit: audit.DataAuditConfig{Enabled: true},
	})
	if err != nil {
		t.Fatalf("NewWithStore: %v", err)
	}
	return a
}

func TestBunHook_InsertUpdateDelete(t *testing.T) {
	ctx := context.Background()
	db := newBunDB(t)
	a := newAuditor(t)
	Register(db, a)

	p := &Product{Name: "widget", Price: 100}
	if _, err := db.NewInsert().Model(p).Exec(ctx); err != nil {
		t.Fatalf("insert: %v", err)
	}

	p.Price = 200
	if _, err := db.NewUpdate().Model(p).WherePK().Exec(ctx); err != nil {
		t.Fatalf("update: %v", err)
	}

	if _, err := db.NewDelete().Model(p).WherePK().Exec(ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}

	logs, err := a.Query(ctx, audit.DataFilter{EntityType: "products"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(logs) != 3 {
		t.Fatalf("want 3 logs, got %d: %+v", len(logs), logs)
	}
	// Query returns newest first.
	del, upd, cre := logs[0], logs[1], logs[2]
	if del.Action != audit.ActionDelete || upd.Action != audit.ActionUpdate || cre.Action != audit.ActionCreate {
		t.Fatalf("unexpected actions: %s/%s/%s", del.Action, upd.Action, cre.Action)
	}

	// UPDATE must carry old_values captured pre-query.
	var oldMap map[string]any
	if err := json.Unmarshal(upd.OldValues, &oldMap); err != nil {
		t.Fatalf("update old_values unmarshal: %v", err)
	}
	// SQLite returns numerics as float64 when scanned into interface{}.
	if v, ok := oldMap["price"].(float64); !ok || v != 100 {
		t.Fatalf("update old_values.price: want 100, got %v", oldMap["price"])
	}
	var newMap map[string]any
	_ = json.Unmarshal(upd.NewValues, &newMap)
	if v, ok := newMap["price"].(float64); !ok || v != 200 {
		t.Fatalf("update new_values.price: want 200, got %v", newMap["price"])
	}

	// DELETE should carry old_values too.
	var delOld map[string]any
	_ = json.Unmarshal(del.OldValues, &delOld)
	if v, ok := delOld["price"].(float64); !ok || v != 200 {
		t.Fatalf("delete old_values.price: want 200, got %v", delOld["price"])
	}
}

func TestBunHook_BulkInsertSharedTxID(t *testing.T) {
	ctx := context.Background()
	db := newBunDB(t)
	a := newAuditor(t)
	Register(db, a)

	products := []Product{
		{Name: "a", Price: 1},
		{Name: "b", Price: 2},
		{Name: "c", Price: 3},
	}
	if _, err := db.NewInsert().Model(&products).Exec(ctx); err != nil {
		t.Fatalf("bulk insert: %v", err)
	}
	logs, _ := a.Query(ctx, audit.DataFilter{EntityType: "products"})
	if len(logs) != 3 {
		t.Fatalf("want 3 logs, got %d", len(logs))
	}
	tx := logs[0].TransactionID
	if tx == "" {
		t.Fatalf("expected non-empty transaction_id")
	}
	for _, l := range logs[1:] {
		if l.TransactionID != tx {
			t.Fatalf("bulk insert rows should share transaction_id, got %q vs %q", l.TransactionID, tx)
		}
	}
}

func TestBunHook_CustomWhereBulkUpdate(t *testing.T) {
	ctx := context.Background()
	db := newBunDB(t)
	a := newAuditor(t)
	Register(db, a)

	// Seed three rows (also audited as creates).
	seed := []Product{
		{Name: "cheap", Price: 50},
		{Name: "mid", Price: 99},
		{Name: "pricey", Price: 500},
	}
	if _, err := db.NewInsert().Model(&seed).Exec(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Bulk update via custom WHERE, no Model rows → snapshot must come from
	// the rendered WHERE extraction path.
	_, err := db.NewUpdate().
		Model((*Product)(nil)).
		Set("price = price + 1").
		Where("price < ?", 100).
		Exec(ctx)
	if err != nil {
		t.Fatalf("bulk update: %v", err)
	}

	logs, _ := a.Query(ctx, audit.DataFilter{
		EntityType: "products",
		Action:     audit.ActionUpdate,
	})
	if len(logs) != 2 {
		t.Fatalf("want 2 update logs (cheap + mid), got %d", len(logs))
	}
	// All updates in this statement must share one transaction_id.
	if logs[0].TransactionID == "" || logs[0].TransactionID != logs[1].TransactionID {
		t.Fatalf("expected shared non-empty txID, got %q vs %q",
			logs[0].TransactionID, logs[1].TransactionID)
	}
	// Each entry must carry an old_values snapshot captured from the
	// rendered WHERE clause.
	for _, l := range logs {
		if len(l.OldValues) == 0 {
			t.Fatalf("update log missing old_values: %+v", l)
		}
		var old map[string]any
		_ = json.Unmarshal(l.OldValues, &old)
		if _, ok := old["price"]; !ok {
			t.Fatalf("old_values missing price: %v", old)
		}
	}
}

func TestBunHook_CustomWhereBulkDelete(t *testing.T) {
	ctx := context.Background()
	db := newBunDB(t)
	a := newAuditor(t)
	Register(db, a)

	seed := []Product{
		{Name: "a", Price: 10},
		{Name: "b", Price: 20},
		{Name: "c", Price: 30},
	}
	if _, err := db.NewInsert().Model(&seed).Exec(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := db.NewDelete().
		Model((*Product)(nil)).
		Where("price <= ?", 20).
		Exec(ctx)
	if err != nil {
		t.Fatalf("bulk delete: %v", err)
	}

	logs, _ := a.Query(ctx, audit.DataFilter{
		EntityType: "products",
		Action:     audit.ActionDelete,
	})
	if len(logs) != 2 {
		t.Fatalf("want 2 delete logs, got %d", len(logs))
	}
	for _, l := range logs {
		if len(l.OldValues) == 0 {
			t.Fatalf("delete log missing old_values: %+v", l)
		}
	}
}

func TestExtractWhere(t *testing.T) {
	cases := []struct{ in, want string }{
		{`UPDATE "t" SET "x" = 1 WHERE "y" = 2`, `"y" = 2`},
		{`DELETE FROM "t" WHERE "y" = 2 RETURNING id`, `"y" = 2`},
		{`DELETE FROM "t" WHERE "y" = 2 ORDER BY id LIMIT 10`, `"y" = 2`},
		// WHERE keyword inside a string literal must not match.
		{`UPDATE "t" SET "x" = 'a WHERE b' WHERE "y" = 3`, `"y" = 3`},
		// Nested subquery WHERE must not match.
		{`UPDATE "t" SET "x" = (SELECT 1 FROM s WHERE s.id = t.id) WHERE "y" = 4`, `"y" = 4`},
		// No WHERE → empty.
		{`DELETE FROM "t"`, ``},
	}
	for _, c := range cases {
		if got := extractWhere(c.in); got != c.want {
			t.Errorf("extractWhere(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

// noopDialect lets us wire NewWithStore without pulling the core's private
// dialect types. Only the behavior exercised by these tests is needed.
type noopDialect struct{}

func (noopDialect) Name() audit.DialectType             { return audit.SQLite }
func (noopDialect) CreateDataTableSQL(string) string    { return "" }
func (noopDialect) CreateAPITableSQL(string) string     { return "" }
func (noopDialect) CreateIndexesSQL(string) []string    { return nil }
func (noopDialect) CreateAPIIndexesSQL(string) []string { return nil }
func (noopDialect) Placeholder(int) string              { return "?" }

// errStore wraps a real Store but always fails on SaveDataLog, simulating an
// audit-store outage.
type errStore struct{ audit.Store }

var errStoreDown = errors.New("audit store down")

func (*errStore) SaveDataLog(context.Context, string, audit.AuditLog) error { return errStoreDown }

func newFailingAuditor(t *testing.T, mode audit.ErrorMode, logf func(string, ...any)) audit.Auditor {
	t.Helper()
	a, err := audit.NewWithStore(&errStore{Store: audit.NewMemoryStore()}, noopDialect{}, audit.Config{
		Dialect:   audit.SQLite,
		UserFunc:  func(context.Context) (string, string) { return "tester", "user" },
		Logger:    logf,
		DataAudit: audit.DataAuditConfig{Enabled: true, OnError: mode},
	})
	if err != nil {
		t.Fatalf("NewWithStore: %v", err)
	}
	return a
}

// Under ErrorFailLoud the auditor returns the store error. Bun's AfterQuery
// cannot alter the result of the already-executed statement, so the hook must
// route it to the WithErrorHandler callback — and the business write succeeds.
func TestBunHook_ErrorFailLoud_RoutesToErrorHandler(t *testing.T) {
	ctx := context.Background()
	db := newBunDB(t)
	var logged []string
	a := newFailingAuditor(t, audit.ErrorFailLoud, func(f string, args ...any) {
		logged = append(logged, fmt.Sprintf(f, args...))
	})
	var handled []error
	Register(db, a, WithErrorHandler(func(_ context.Context, err error) { handled = append(handled, err) }))

	p := &Product{Name: "widget", Price: 100}
	if _, err := db.NewInsert().Model(p).Exec(ctx); err != nil {
		t.Fatalf("insert must still succeed, got: %v", err)
	}
	var n int
	if err := db.NewSelect().Model((*Product)(nil)).ColumnExpr("count(*)").Scan(ctx, &n); err != nil || n != 1 {
		t.Fatalf("row must be persisted: n=%d err=%v", n, err)
	}

	if len(handled) != 1 {
		t.Fatalf("want 1 handled error, got %d: %v", len(handled), handled)
	}
	if !errors.Is(handled[0], errStoreDown) {
		t.Fatalf("handler error should wrap the store error, got %v", handled[0])
	}
	if !strings.Contains(handled[0].Error(), "record create products/1") {
		t.Fatalf("handler error should name action and entity, got %q", handled[0])
	}
	if len(logged) != 0 {
		t.Fatalf("custom handler replaces default logging, but Logger got %v", logged)
	}
}

// Without WithErrorHandler the default handler logs via Config.Logger.
func TestBunHook_ErrorFailLoud_DefaultHandlerLogs(t *testing.T) {
	ctx := context.Background()
	db := newBunDB(t)
	var logged []string
	a := newFailingAuditor(t, audit.ErrorFailLoud, func(f string, args ...any) {
		logged = append(logged, fmt.Sprintf(f, args...))
	})
	Register(db, a)

	if _, err := db.NewInsert().Model(&Product{Name: "widget"}).Exec(ctx); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], errStoreDown.Error()) {
		t.Fatalf("want one log line mentioning the store error, got %v", logged)
	}
}

// Under ErrorFailSilent the auditor itself logs and returns nil, so the
// error handler is never invoked and nothing bubbles up.
func TestBunHook_ErrorFailSilent_SwallowsAndLogs(t *testing.T) {
	ctx := context.Background()
	db := newBunDB(t)
	var logged []string
	a := newFailingAuditor(t, audit.ErrorFailSilent, func(f string, args ...any) {
		logged = append(logged, fmt.Sprintf(f, args...))
	})
	handlerCalls := 0
	Register(db, a, WithErrorHandler(func(context.Context, error) { handlerCalls++ }))

	if _, err := db.NewInsert().Model(&Product{Name: "widget"}).Exec(ctx); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if handlerCalls != 0 {
		t.Fatalf("handler must not run under ErrorFailSilent, ran %d times", handlerCalls)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "save data log") {
		t.Fatalf("auditor should have logged the swallowed error once, got %v", logged)
	}
}
