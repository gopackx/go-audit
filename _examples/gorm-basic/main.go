// Example: wiring go-audit with GORM + SQLite. Creates, updates, and deletes
// a product and prints the resulting audit trail.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/glebarez/sqlite"
	"github.com/gopackx/go-audit"
	auditgorm "github.com/gopackx/go-audit/adapters/gorm"
	"gorm.io/gorm"
)

type Product struct {
	ID       uint   `gorm:"primaryKey"`
	Name     string `gorm:"size:100"`
	Price    int
	Password string
}

type userCtxKey struct{}

func main() {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	must(err)
	must(db.AutoMigrate(&Product{}))

	sqlDB, err := db.DB()
	must(err)

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
	})
	must(err)
	must(auditor.AutoMigrate(context.Background()))
	must(db.Use(auditgorm.Plugin(auditor)))

	ctx := context.WithValue(context.Background(), userCtxKey{}, "alice")

	p := Product{Name: "Widget", Price: 100, Password: "should-not-leak"}
	db.WithContext(ctx).Create(&p)

	p.Price = 150
	db.WithContext(ctx).Save(&p)

	db.WithContext(ctx).Delete(&p)

	logs, err := auditor.Query(ctx, audit.DataFilter{EntityType: "products"})
	must(err)
	fmt.Printf("audit trail (%d rows):\n", len(logs))
	for _, l := range logs {
		fmt.Printf("  [%s] %s#%s by %s  old=%s new=%s\n",
			l.Action, l.EntityType, l.EntityID, l.UserID,
			prettyJSON(l.OldValues), prettyJSON(l.NewValues))
	}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func prettyJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}
