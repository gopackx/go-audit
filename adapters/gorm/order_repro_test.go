package gorm

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	audit "github.com/gopackx/go-audit"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

// Order / OrderItem mirror the reporter's GoStore models verbatim: Order has a
// soft-delete column, OrderItem does not; HasMany via value slice.
type Order struct {
	ID          uint64 `gorm:"primaryKey"`
	UserID      uint64
	OrderNumber string
	Status      string
	Total       float64        `gorm:"type:decimal(12,2)"`
	Notes       *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   gorm.DeletedAt `gorm:"index"`
	Items       []OrderItem    `gorm:"foreignKey:OrderID"`
}

type OrderItem struct {
	ID        uint64 `gorm:"primaryKey"`
	OrderID   uint64
	ProductID uint64
	Quantity  int
	UnitPrice float64 `gorm:"type:decimal(10,2)"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func openOrderDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "order.db") + "?_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlog.Default.LogMode(gormlog.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// TestNestedCreateDoesNotDuplicateChildAudit is the reporter's drop-in repro.
func TestNestedCreateDoesNotDuplicateChildAudit(t *testing.T) {
	db := openOrderDB(t)
	if err := db.AutoMigrate(&Order{}, &OrderItem{}); err != nil {
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

	order := Order{
		Status: "pending",
		Total:  3298.81,
		Items: []OrderItem{
			{ProductID: 1, Quantity: 2, UnitPrice: 981.12},
			{ProductID: 2, Quantity: 1, UnitPrice: 1336.57},
		},
	}
	if err := db.WithContext(ctx).Create(&order).Error; err != nil {
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
	t.Logf("audit counts by entity/action: %+v", counts)

	if got := counts["order_items/create"]; got != 2 {
		t.Errorf("order_items/create: got %d, want 2 (A2 — child create duplicated)", got)
	}
	if got := counts["orders/create"]; got != 1 {
		t.Errorf("orders/create: got %d, want 1", got)
	}
}
