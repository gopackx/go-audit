package gorm

import (
	"context"
	"testing"

	audit "github.com/gopackx/go-audit"
	"gorm.io/gorm"
)

// A2b: a parent Update that still carries loaded HasMany associations makes GORM
// re-upsert the children; the adapter records each as `create` a 2nd time.
// FAILS today (order_items/create == 4), should be 2.
func TestParentUpdateDoesNotReauditChildAsCreate(t *testing.T) {
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
	order := Order{Status: "pending", Total: 3298.81, Items: []OrderItem{
		{ProductID: 1, Quantity: 2, UnitPrice: 981.12},
		{ProductID: 2, Quantity: 1, UnitPrice: 1336.57},
	}}
	if err := db.WithContext(ctx).Create(&order).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	// order STILL carries .Items — this is the trigger.
	if err := db.WithContext(ctx).Model(&order).Update("status", "paid").Error; err != nil {
		t.Fatalf("update: %v", err)
	}
	logs, _ := auditor.QueryByTransaction(ctx, txID)
	counts := map[string]int{}
	for _, e := range logs.DataLogs {
		counts[e.EntityType+"/"+e.Action]++
	}
	t.Logf("counts: %+v", counts)
	if counts["order_items/create"] != 2 {
		t.Errorf("order_items/create: got %d, want 2", counts["order_items/create"])
	}
}

// Guard: a genuinely NEW child (PK still zero) added to the parent during an
// Update must still be audited as `create`. This pins the precision of the A2b
// fix — it must skip re-saved (already-persisted) children but never suppress
// real new inserts that happen to occur inside an update.
func TestParentUpdateAuditsNewChildren(t *testing.T) {
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
	ctx := context.Background()

	// First create: 2 items.
	order := Order{Status: "pending", Items: []OrderItem{
		{ProductID: 1, Quantity: 2, UnitPrice: 10},
		{ProductID: 2, Quantity: 1, UnitPrice: 20},
	}}
	if err := db.WithContext(ctx).Create(&order).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	// Append a brand-new item (PK zero) and save associations via the parent.
	txID := audit.NewTransactionID()
	ctx2 := audit.WithTransactionID(ctx, txID)
	order.Status = "paid"
	order.Items = append(order.Items, OrderItem{ProductID: 3, Quantity: 5, UnitPrice: 30})
	if err := db.WithContext(ctx2).Session(&gorm.Session{FullSaveAssociations: true}).
		Updates(&order).Error; err != nil {
		t.Fatalf("update: %v", err)
	}

	logs, _ := auditor.QueryByTransaction(ctx2, txID)
	newCreates := 0
	for _, e := range logs.DataLogs {
		if e.EntityType == "order_items" && e.Action == audit.ActionCreate {
			newCreates++
		}
	}
	// Exactly the ONE new child (id 3) should be audited as create in this tx;
	// the two pre-existing children (ids 1,2) are re-saves and must be skipped.
	if newCreates != 1 {
		t.Errorf("new-child create audits in update tx: got %d, want 1", newCreates)
	}
}

// Control — PASSES: omitting associations avoids the re-upsert (pins the workaround).
func TestParentUpdateOmitAssociationsControl(t *testing.T) {
	db := openOrderDB(t)
	_ = db.AutoMigrate(&Order{}, &OrderItem{})
	auditor, _ := audit.NewWithStore(audit.NewMemoryStore(), audit.DialectFor(audit.SQLite), audit.Config{
		Dialect: audit.SQLite, DataAudit: audit.DataAuditConfig{Enabled: true},
		UserFunc: func(ctx context.Context) (string, string) { return "t", "u" },
	})
	_ = db.Use(Plugin(auditor))
	txID := audit.NewTransactionID()
	ctx := audit.WithTransactionID(context.Background(), txID)
	order := Order{Status: "pending", Items: []OrderItem{{ProductID: 1, Quantity: 2}, {ProductID: 2, Quantity: 1}}}
	_ = db.WithContext(ctx).Create(&order).Error
	_ = db.WithContext(ctx).Model(&order).Omit("Items").Update("status", "paid").Error
	logs, _ := auditor.QueryByTransaction(ctx, txID)
	n := 0
	for _, e := range logs.DataLogs {
		if e.EntityType == "order_items" && e.Action == "create" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("control: order_items/create=%d, want 2", n)
	}
}
