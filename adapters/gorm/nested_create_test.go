package gorm

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	audit "github.com/gopackx/go-audit"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

// openTestDB returns a fresh on-disk SQLite GORM DB inside t.TempDir(). On-
// disk avoids the ":memory:" per-connection-DB and the shared-cache
// deadlock-with-MaxOpenConns(1) traps without sacrificing test isolation.
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db") + "?_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlog.Default.LogMode(gormlog.Silent),
	})
	if err != nil {
		t.Fatalf("open gorm: %v", err)
	}
	// Close the connection before TempDir cleanup runs — without this, Windows
	// refuses to delete the db file because the SQLite handle is still open.
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// Parent / Child are minimal GORM models with a HasMany association — the
// shape that produced the 2-items -> 4-rows duplication in pre-fix builds.
type Parent struct {
	ID       uint
	Name     string
	Children []Child `gorm:"foreignKey:ParentID"`
}

type Child struct {
	ID       uint
	ParentID uint
	Name     string
}

// TestNestedCreate_NoDuplicateAuditRows reproduces issue A2: creating a parent
// with a HasMany slice in one `db.Create` call must produce exactly one audit
// row per ORM row written — never more.
func TestNestedCreate_NoDuplicateAuditRows(t *testing.T) {
	gormDB := openTestDB(t)
	if err := gormDB.AutoMigrate(&Parent{}, &Child{}); err != nil {
		t.Fatalf("migrate domain tables: %v", err)
	}

	// Use the in-memory audit store so we isolate this test from SQLite
	// write-locking on the same connection that the GORM nested transaction
	// is using. We're testing dedup logic, not the SQL store.
	auditor, err := audit.NewWithStore(audit.NewMemoryStore(), audit.DialectFor(audit.SQLite), audit.Config{
		Dialect: audit.SQLite,
		UserFunc: func(ctx context.Context) (string, string) {
			return "tester", "user"
		},
		DataAudit: audit.DataAuditConfig{Enabled: true},
	})
	if err != nil {
		t.Fatalf("audit.NewWithStore: %v", err)
	}
	if err := gormDB.Use(Plugin(auditor)); err != nil {
		t.Fatalf("register plugin: %v", err)
	}

	// One Create with a HasMany slice — the canonical "nested Create".
	p := Parent{
		Name: "p-1",
		Children: []Child{
			{Name: "c-1"},
			{Name: "c-2"},
		},
	}
	if err := gormDB.Create(&p).Error; err != nil {
		t.Fatalf("nested create: %v", err)
	}

	// Verify domain rows are correct (sanity). Use a fresh session so we
	// don't accidentally re-trigger callbacks via the same statement.
	var (
		parentCount int64
		childCount  int64
	)
	gormDB.Session(&gorm.Session{NewDB: true}).Model(&Parent{}).Count(&parentCount)
	gormDB.Session(&gorm.Session{NewDB: true}).Model(&Child{}).Count(&childCount)
	if parentCount != 1 || childCount != 2 {
		t.Fatalf("unexpected domain row counts: parents=%d children=%d", parentCount, childCount)
	}

	// Now the audit log: exactly 1 parent + 2 child rows.
	ctx := context.Background()
	parents, err := auditor.Query(ctx, audit.DataFilter{EntityType: "parents"})
	if err != nil {
		t.Fatalf("query parents audit: %v", err)
	}
	children, err := auditor.Query(ctx, audit.DataFilter{EntityType: "children"})
	if err != nil {
		t.Fatalf("query children audit: %v", err)
	}

	if len(parents) != 1 {
		t.Errorf("parent audit rows: got %d, want 1", len(parents))
	}
	if len(children) != 2 {
		t.Errorf("child audit rows: got %d, want 2 (A2 regression — nested-Create dedup broken)", len(children))
	}

	// Each child audit row must reference a distinct entity_id.
	if len(children) >= 2 {
		seen := map[string]struct{}{}
		for _, l := range children {
			if _, dup := seen[l.EntityID]; dup {
				t.Errorf("duplicate child audit entry for entity_id=%q", l.EntityID)
			}
			seen[l.EntityID] = struct{}{}
		}
	}
}

// ParentPtr uses a pointer slice — a different reflect shape that previously
// caused duplicate-row reports in some versions of GORM.
type ParentPtr struct {
	ID       uint
	Name     string
	Children []*Child `gorm:"foreignKey:ParentID"`
}

// TestNestedCreate_PointerSlice covers `[]*Child` as the HasMany shape.
func TestNestedCreate_PointerSlice(t *testing.T) {
	gormDB := openTestDB(t)
	if err := gormDB.AutoMigrate(&ParentPtr{}, &Child{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	auditor, err := audit.NewWithStore(audit.NewMemoryStore(), audit.DialectFor(audit.SQLite), audit.Config{
		Dialect:   audit.SQLite,
		UserFunc:  func(ctx context.Context) (string, string) { return "tester", "user" },
		DataAudit: audit.DataAuditConfig{Enabled: true},
	})
	if err != nil {
		t.Fatalf("audit.NewWithStore: %v", err)
	}
	_ = gormDB.Use(Plugin(auditor))

	p := ParentPtr{
		Name: "p-ptr",
		Children: []*Child{
			{Name: "c-1"},
			{Name: "c-2"},
		},
	}
	if err := gormDB.Create(&p).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	children, _ := auditor.Query(context.Background(), audit.DataFilter{EntityType: "children"})
	if len(children) != 2 {
		t.Errorf("pointer-slice children audit rows: got %d, want 2", len(children))
	}
}

// TestNestedSave covers `db.Save(&parent)` — uses a different GORM internal
// path (upsert) than Create.
func TestNestedSave(t *testing.T) {
	gormDB := openTestDB(t)
	if err := gormDB.AutoMigrate(&Parent{}, &Child{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	auditor, err := audit.NewWithStore(audit.NewMemoryStore(), audit.DialectFor(audit.SQLite), audit.Config{
		Dialect:   audit.SQLite,
		UserFunc:  func(ctx context.Context) (string, string) { return "tester", "user" },
		DataAudit: audit.DataAuditConfig{Enabled: true},
	})
	if err != nil {
		t.Fatalf("audit.NewWithStore: %v", err)
	}
	_ = gormDB.Use(Plugin(auditor))

	p := Parent{
		Name: "p-save",
		Children: []Child{
			{Name: "c-1"},
			{Name: "c-2"},
		},
	}
	if err := gormDB.Save(&p).Error; err != nil {
		t.Fatalf("save: %v", err)
	}

	parents, _ := auditor.Query(context.Background(), audit.DataFilter{EntityType: "parents"})
	children, _ := auditor.Query(context.Background(), audit.DataFilter{EntityType: "children"})
	t.Logf("via Save: parents=%d children=%d", len(parents), len(children))
	if len(parents) != 1 {
		t.Errorf("Save parent audit rows: got %d, want 1", len(parents))
	}
	if len(children) != 2 {
		t.Errorf("Save child audit rows: got %d, want 2", len(children))
	}
}

// TestBatchCreate_NoDuplicateAuditRows covers the other shape of the same
// concern: a plain batch insert via `db.Create(&slice)` must also not
// duplicate rows. Different code path inside GORM than nested associations,
// but the dedup guarantee should hold the same way.
func TestBatchCreate_NoDuplicateAuditRows(t *testing.T) {
	gormDB := openTestDB(t)
	if err := gormDB.AutoMigrate(&Child{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	auditor, err := audit.NewWithStore(audit.NewMemoryStore(), audit.DialectFor(audit.SQLite), audit.Config{
		Dialect:   audit.SQLite,
		UserFunc:  func(ctx context.Context) (string, string) { return "tester", "user" },
		DataAudit: audit.DataAuditConfig{Enabled: true},
	})
	if err != nil {
		t.Fatalf("audit.NewWithStore: %v", err)
	}
	_ = gormDB.Use(Plugin(auditor))

	rows := []Child{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	if err := gormDB.Create(&rows).Error; err != nil {
		t.Fatalf("batch create: %v", err)
	}

	logs, err := auditor.Query(context.Background(), audit.DataFilter{EntityType: "children"})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(logs) != 3 {
		t.Errorf("batch create audit rows: got %d, want 3", len(logs))
	}
}
