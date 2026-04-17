# Getting Started

## Installation

Install the core package (zero external dependencies):

```bash
go get github.com/gopackx/go-audit
```

Then install the adapter for your ORM:

```bash
# Pick one (or more)
go get github.com/gopackx/go-audit/adapters/gorm
go get github.com/gopackx/go-audit/adapters/bun
go get github.com/gopackx/go-audit/adapters/ent
```

## Requirements

- Go 1.21+
- One of: PostgreSQL, MySQL, or SQLite

## Quick Start (GORM + SQLite)

```go
package main

import (
    "context"
    "log"

    "github.com/glebarez/sqlite"
    "github.com/gopackx/go-audit"
    auditgorm "github.com/gopackx/go-audit/adapters/gorm"
    "gorm.io/gorm"
)

type Product struct {
    ID    uint   `gorm:"primaryKey"`
    Name  string `gorm:"size:100"`
    Price int
}

func main() {
    // 1. Open your database as usual
    db, err := gorm.Open(sqlite.Open("app.db"), &gorm.Config{})
    if err != nil {
        log.Fatal(err)
    }
    db.AutoMigrate(&Product{})

    sqlDB, _ := db.DB()

    // 2. Create the auditor
    auditor, err := audit.New(sqlDB, audit.Config{
        Dialect: audit.SQLite,
        UserFunc: func(ctx context.Context) (string, string) {
            userID, _ := ctx.Value("user_id").(string)
            if userID == "" {
                return "system", "system"
            }
            return userID, "user"
        },
        DataAudit: audit.DataAuditConfig{
            Enabled:       true,
            ExcludeFields: []string{"password", "remember_token"},
        },
    })
    if err != nil {
        log.Fatal(err)
    }

    // 3. Create audit tables
    auditor.AutoMigrate(context.Background())

    // 4. Register the GORM plugin — all CRUD is now auto-audited
    db.Use(auditgorm.Plugin(auditor))

    // 5. Use your database normally — auditing happens automatically
    ctx := context.WithValue(context.Background(), "user_id", "alice")

    db.WithContext(ctx).Create(&Product{Name: "Widget", Price: 100})
    // ^ automatically records: action=create, new_values={"name":"Widget","price":100}
}
```

After `db.Use(auditgorm.Plugin(auditor))`, every `Create`, `Save`, `Update`, and `Delete` through that `*gorm.DB` is audited automatically. No manual `Record()` calls needed.

## Quick Start (Bun + SQLite)

```go
package main

import (
    "context"
    "database/sql"
    "log"

    "github.com/gopackx/go-audit"
    auditbun "github.com/gopackx/go-audit/adapters/bun"
    "github.com/uptrace/bun"
    "github.com/uptrace/bun/dialect/sqlitedialect"
    _ "modernc.org/sqlite"
)

type Product struct {
    bun.BaseModel `bun:"table:products"`
    ID            int64  `bun:",pk,autoincrement"`
    Name          string `bun:"name"`
    Price         int    `bun:"price"`
}

func main() {
    sqldb, _ := sql.Open("sqlite", ":memory:")
    db := bun.NewDB(sqldb, sqlitedialect.New())

    ctx := context.Background()
    db.NewCreateTable().Model((*Product)(nil)).Exec(ctx)

    auditor, err := audit.New(sqldb, audit.Config{
        Dialect: audit.SQLite,
        UserFunc: func(ctx context.Context) (string, string) {
            return "alice", "user"
        },
        DataAudit: audit.DataAuditConfig{Enabled: true},
    })
    if err != nil {
        log.Fatal(err)
    }
    auditor.AutoMigrate(ctx)

    // Register Bun hooks — all operations auto-audited after this
    auditbun.Register(db, auditor)

    p := &Product{Name: "Widget", Price: 100}
    db.NewInsert().Model(p).Exec(ctx)
    // ^ automatically audited
}
```

## Quick Start (Ent)

```go
import (
    "github.com/gopackx/go-audit"
    entaudit "github.com/gopackx/go-audit/adapters/ent"
)

// After creating your ent client:
client.Use(entaudit.Hook(auditor))

// All create/update/delete mutations are now auto-audited
```

## Next Steps

- [Configuration Reference](./configuration.md) — all config options explained
- [Data Auditing](./data-auditing.md) — how data change tracking works
- [API Auditing](./api-auditing.md) — logging third-party API calls
- [ORM Adapters](./adapters.md) — GORM, Bun, and Ent adapter details
- [Querying](./querying.md) — searching and filtering audit logs
- [Advanced Features](./advanced.md) — transaction correlation, snapshot/restore, retention
