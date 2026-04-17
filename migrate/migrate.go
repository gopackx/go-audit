// Package migrate creates audit tables and indexes via the supplied store and
// dialect. Safe to call repeatedly; all DDL uses IF NOT EXISTS.
package migrate

import (
	"context"

	"github.com/gopackx/go-audit/dialect"
	"github.com/gopackx/go-audit/store"
)

// Config holds the table names and enabled flags needed by Migrate.
type Config struct {
	DataTable   string
	APITable    string
	DataEnabled bool
	APIEnabled  bool
}

// Migrate creates any enabled audit tables (and their indexes) via the
// supplied store. Safe to call repeatedly; all DDL uses IF NOT EXISTS.
func Migrate(ctx context.Context, s store.Store, d dialect.Dialect, cfg Config) error {
	if cfg.DataEnabled {
		if err := s.Exec(ctx, d.CreateDataTableSQL(cfg.DataTable)); err != nil {
			return err
		}
		for _, stmt := range d.CreateIndexesSQL(cfg.DataTable) {
			if err := s.Exec(ctx, stmt); err != nil {
				return err
			}
		}
	}
	if cfg.APIEnabled {
		if err := s.Exec(ctx, d.CreateAPITableSQL(cfg.APITable)); err != nil {
			return err
		}
		for _, stmt := range d.CreateAPIIndexesSQL(cfg.APITable) {
			if err := s.Exec(ctx, stmt); err != nil {
				return err
			}
		}
	}
	return nil
}
