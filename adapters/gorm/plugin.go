// Package gorm is the GORM adapter for go-audit. It registers callbacks that
// capture create/update/delete events and dispatches them to the Auditor.
package gorm

import (
	"github.com/gopackx/go-audit"
	"gorm.io/gorm"
)

// Plugin wires the auditor into a *gorm.DB. Register it via `db.Use(Plugin(auditor))`.
func Plugin(auditor audit.Auditor) gorm.Plugin {
	return &plugin{auditor: auditor}
}

type plugin struct {
	auditor audit.Auditor
}

func (p *plugin) Name() string { return "go-audit" }

func (p *plugin) Initialize(db *gorm.DB) error {
	cbs := newCallbacks(p.auditor)
	if err := db.Callback().Create().After("gorm:create").Register("go_audit:after_create", cbs.afterCreate); err != nil {
		return err
	}
	if err := db.Callback().Update().Before("gorm:update").Register("go_audit:before_update", cbs.beforeUpdate); err != nil {
		return err
	}
	if err := db.Callback().Update().After("gorm:update").Register("go_audit:after_update", cbs.afterUpdate); err != nil {
		return err
	}
	if err := db.Callback().Delete().Before("gorm:delete").Register("go_audit:before_delete", cbs.beforeDelete); err != nil {
		return err
	}
	if err := db.Callback().Delete().After("gorm:delete").Register("go_audit:after_delete", cbs.afterDelete); err != nil {
		return err
	}
	return nil
}
