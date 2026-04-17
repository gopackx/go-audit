// Example: wiring go-audit with Bun + SQLite. Creates, updates, and deletes
// a product and prints the resulting audit trail.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"github.com/gopackx/go-audit"
	auditbun "github.com/gopackx/go-audit/adapters/bun"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite"
)

type Product struct {
	bun.BaseModel `bun:"table:products"`

	ID       int64  `bun:",pk,autoincrement"`
	Name     string `bun:"name"`
	Price    int    `bun:"price"`
	Password string `bun:"password"`
}

type userCtxKey struct{}

func main() {
	sqldb, err := sql.Open("sqlite", ":memory:")
	must(err)
	db := bun.NewDB(sqldb, sqlitedialect.New())

	ctx := context.Background()
	_, err = db.NewCreateTable().Model((*Product)(nil)).Exec(ctx)
	must(err)

	auditor, err := audit.New(sqldb, audit.Config{
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
	must(auditor.AutoMigrate(ctx))
	auditbun.Register(db, auditor)

	ctx = context.WithValue(ctx, userCtxKey{}, "alice")

	p := &Product{Name: "Widget", Price: 100, Password: "should-not-leak"}
	_, err = db.NewInsert().Model(p).Exec(ctx)
	must(err)

	p.Price = 150
	_, err = db.NewUpdate().Model(p).WherePK().Exec(ctx)
	must(err)

	_, err = db.NewDelete().Model(p).WherePK().Exec(ctx)
	must(err)

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
