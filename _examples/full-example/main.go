// Example: full go-audit demo — GORM + SQLite with data auditing, API call
// logging, soft deletes, transaction correlation, querying, and retention.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gopackx/go-audit"
	auditgorm "github.com/gopackx/go-audit/adapters/gorm"
	"gorm.io/gorm"
)

type Product struct {
	ID        uint           `gorm:"primaryKey"`
	Name      string         `gorm:"size:100"`
	Price     int
	Password  string
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

type userCtxKey struct{}

func main() {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	must(err)
	must(db.AutoMigrate(&Product{}))

	sqlDB, err := db.DB()
	must(err)

	// -----------------------------------------------------------------------
	// 1. Setup: auditor with both data + API audit enabled
	// -----------------------------------------------------------------------
	auditor, err := audit.New(sqlDB, audit.Config{
		Dialect: audit.SQLite,
		UserFunc: func(ctx context.Context) (string, string) {
			if v, _ := ctx.Value(userCtxKey{}).(string); v != "" {
				return v, "user"
			}
			return "system", "system"
		},
		DataAudit: audit.DataAuditConfig{
			Enabled:       true,
			ExcludeFields: []string{"password"},
		},
		APIAudit: audit.APIAuditConfig{
			Enabled:          true,
			RedactHeaders:    []string{"Authorization", "X-API-Key"},
			RedactBodyFields: []string{"token", "card_number"},
			MaxBodySize:      4096,
		},
	})
	must(err)
	must(auditor.AutoMigrate(context.Background()))
	must(db.Use(auditgorm.Plugin(auditor)))

	ctx := context.WithValue(context.Background(), userCtxKey{}, "alice")

	// -----------------------------------------------------------------------
	// 2. Create a product (auto-audited by GORM)
	// -----------------------------------------------------------------------
	fmt.Println("=== Create ===")
	p := Product{Name: "Widget", Price: 100, Password: "should-not-leak"}
	db.WithContext(ctx).Create(&p)
	fmt.Printf("  created product #%d\n", p.ID)

	// -----------------------------------------------------------------------
	// 3. Update the product (auto-audited, only changed fields stored)
	// -----------------------------------------------------------------------
	fmt.Println("\n=== Update ===")
	p.Price = 150
	p.Name = "Widget Pro"
	db.WithContext(ctx).Save(&p)
	fmt.Println("  updated price=150, name=Widget Pro")

	// -----------------------------------------------------------------------
	// 4. API call with transaction correlation
	// -----------------------------------------------------------------------
	fmt.Println("\n=== API Call + Transaction Correlation ===")
	txID := audit.NewTransactionID()
	txCtx := audit.WithTransactionID(ctx, txID)

	// Simulate an API call to a payment gateway.
	must(auditor.API().Record(txCtx, audit.APIEntry{
		Service:    "payment-gateway",
		Endpoint:   "/api/v1/charge",
		Method:     "POST",
		StatusCode: 200,
		RequestHeaders: map[string]string{
			"Authorization": "Bearer super-secret-token",
			"Content-Type":  "application/json",
		},
		RequestBody: map[string]any{
			"amount":      15000,
			"currency":    "USD",
			"token":       "tok_visa_4242",
			"card_number": "4111-1111-1111-1111",
		},
		ResponseBody: map[string]any{
			"charge_id": "ch_001",
			"status":    "succeeded",
		},
		DurationMs: 230,
	}))

	// Data change under the same transaction.
	p.Price = 200
	db.WithContext(txCtx).Save(&p)
	fmt.Printf("  txID=%s\n", txID)

	// -----------------------------------------------------------------------
	// 5. Soft delete (action recorded as "soft_delete")
	// -----------------------------------------------------------------------
	fmt.Println("\n=== Soft Delete ===")
	db.WithContext(ctx).Delete(&p)
	fmt.Printf("  soft-deleted product #%d\n", p.ID)

	// -----------------------------------------------------------------------
	// 6. Query all audit logs for this product
	// -----------------------------------------------------------------------
	fmt.Println("\n=== Data Audit Trail ===")
	logs, err := auditor.Query(ctx, audit.DataFilter{EntityType: "products"})
	must(err)
	fmt.Printf("  %d entries:\n", len(logs))
	for _, l := range logs {
		fmt.Printf("    [%s] %s#%s by %s  old=%s new=%s\n",
			l.Action, l.EntityType, l.EntityID, l.UserID,
			jsonStr(l.OldValues), jsonStr(l.NewValues))
	}

	// -----------------------------------------------------------------------
	// 7. Query API logs
	// -----------------------------------------------------------------------
	fmt.Println("\n=== API Audit Trail ===")
	apiLogs, err := auditor.API().Query(ctx, audit.APIFilter{Service: "payment-gateway"})
	must(err)
	fmt.Printf("  %d entries:\n", len(apiLogs))
	for _, l := range apiLogs {
		fmt.Printf("    %s %s -> %d (%dms)  headers=%s body=%s\n",
			l.Method, l.Endpoint, l.StatusCode, l.DurationMs,
			jsonStr(l.RequestHeaders), jsonStr(l.RequestBody))
	}

	// -----------------------------------------------------------------------
	// 8. QueryByTransaction — correlate data + API logs
	// -----------------------------------------------------------------------
	fmt.Println("\n=== Transaction Correlation ===")
	bundle, err := auditor.QueryByTransaction(ctx, txID)
	must(err)
	fmt.Printf("  transaction %s:\n", bundle.TransactionID)
	fmt.Printf("    data changes: %d\n", len(bundle.DataLogs))
	for _, l := range bundle.DataLogs {
		fmt.Printf("      [%s] %s#%s\n", l.Action, l.EntityType, l.EntityID)
	}
	fmt.Printf("    api calls: %d\n", len(bundle.APILogs))
	for _, l := range bundle.APILogs {
		fmt.Printf("      %s %s -> %d\n", l.Method, l.Endpoint, l.StatusCode)
	}

	// -----------------------------------------------------------------------
	// 9. Purge — retention cleanup (delete logs older than 1 hour ago; none
	// will match since we just created them, but this shows the API)
	// -----------------------------------------------------------------------
	fmt.Println("\n=== Purge (retention) ===")
	res, err := auditor.Purge(ctx, time.Now().Add(-1*time.Hour))
	must(err)
	fmt.Printf("  purged: data=%d api=%d (expected 0 — logs are fresh)\n",
		res.DataLogs, res.APILogs)

	fmt.Println("\nDone.")
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func jsonStr(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}
