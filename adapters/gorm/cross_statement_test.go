package gorm

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	audit "github.com/gopackx/go-audit"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlog "gorm.io/gorm/logger"
)

// backfillItem models the reporter's OrderItem. Its parent's AfterCreate hook
// re-upserts the items to simulate the GORM ≥ v1.31 / Postgres "FK back-fill"
// pass: the child create-callback chain runs a SECOND time, in a separate
// statement, within the SAME auto-transaction of one db.Create(&parent).
type backfillItem struct {
	ID        uint64 `gorm:"primaryKey"`
	BackfillID uint64
	Name      string
}

type backfillParent struct {
	ID    uint64 `gorm:"primaryKey"`
	Name  string
	Items []backfillItem `gorm:"foreignKey:BackfillID"`
}

// AfterCreate re-runs the child create chain over the same rows, exactly like
// GORM's FK-backfill upsert. Without transaction-scoped dedup this produces a
// second set of order_items/create audit rows (the reporter's "2 items -> 4
// rows"); with it, the second pass is recognised as already-audited.
func (p *backfillParent) AfterCreate(tx *gorm.DB) error {
	if len(p.Items) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"backfill_id"}),
	}).Create(&p.Items).Error
}

// TestNestedCreateBackfillDedup is the faithful reproduction of A2: a single
// db.Create(&parent) whose association/back-fill handling fires the child
// create chain twice within one auto-transaction must still produce exactly one
// audit row per child.
func TestNestedCreateBackfillDedup(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "b.db") + "?_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlog.Default.LogMode(gormlog.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if s, err := db.DB(); err == nil {
			_ = s.Close()
		}
	})
	if err := db.AutoMigrate(&backfillParent{}, &backfillItem{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	auditor, err := audit.NewWithStore(audit.NewMemoryStore(), audit.DialectFor(audit.SQLite), audit.Config{
		Dialect:   audit.SQLite,
		UserFunc:  func(ctx context.Context) (string, string) { return "tester", "user" },
		DataAudit: audit.DataAuditConfig{Enabled: true},
	})
	if err != nil {
		t.Fatalf("auditor: %v", err)
	}
	if err := db.Use(Plugin(auditor)); err != nil {
		t.Fatalf("plugin: %v", err)
	}

	txID := audit.NewTransactionID()
	ctx := audit.WithTransactionID(context.Background(), txID)

	parent := backfillParent{
		Name: "p",
		Items: []backfillItem{
			{Name: "i-1"},
			{Name: "i-2"},
		},
	}
	if err := db.WithContext(ctx).Create(&parent).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	logs, err := auditor.QueryByTransaction(ctx, txID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	counts := map[string]int{}
	for _, e := range logs.DataLogs {
		counts[e.EntityType+"/"+e.Action]++
	}
	t.Logf("audit counts: %+v", counts)
	if got := counts["backfill_items/create"]; got != 2 {
		t.Errorf("backfill_items/create: got %d, want 2 (A2 — child create duplicated across backfill pass)", got)
	}
	if got := counts["backfill_parents/create"]; got != 1 {
		t.Errorf("backfill_parents/create: got %d, want 1", got)
	}
}
