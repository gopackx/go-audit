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

	// enter/leave bracket each callback chain so the transaction-scoped dedup
	// set spans every statement of one logical write (parent + nested
	// associations + FK-backfill) and is torn down when the outermost
	// operation unwinds. enter runs first (recording the root operation kind);
	// leave runs last.
	enterCreate := func(db *gorm.DB) { cbs.enter(db, audit.ActionCreate) }
	enterUpdate := func(db *gorm.DB) { cbs.enter(db, audit.ActionUpdate) }
	enterDelete := func(db *gorm.DB) { cbs.enter(db, audit.ActionDelete) }
	leave := func(db *gorm.DB) { cbs.leave(db) }

	c := db.Callback()
	if err := c.Create().Before("gorm:create").Register("go_audit:enter_create", enterCreate); err != nil {
		return err
	}
	if err := c.Create().Before("gorm:create").Register("go_audit:capture_pks", cbs.captureExistingPKs); err != nil {
		return err
	}
	if err := c.Create().After("gorm:create").Register("go_audit:after_create", cbs.afterCreate); err != nil {
		return err
	}
	if err := c.Create().After("gorm:after_create").Register("go_audit:leave_create", leave); err != nil {
		return err
	}
	if err := c.Update().Before("gorm:update").Register("go_audit:enter_update", enterUpdate); err != nil {
		return err
	}
	if err := c.Update().Before("gorm:update").Register("go_audit:before_update", cbs.beforeUpdate); err != nil {
		return err
	}
	if err := c.Update().After("gorm:update").Register("go_audit:after_update", cbs.afterUpdate); err != nil {
		return err
	}
	if err := c.Update().After("gorm:after_update").Register("go_audit:leave_update", leave); err != nil {
		return err
	}
	if err := c.Delete().Before("gorm:delete").Register("go_audit:enter_delete", enterDelete); err != nil {
		return err
	}
	if err := c.Delete().Before("gorm:delete").Register("go_audit:before_delete", cbs.beforeDelete); err != nil {
		return err
	}
	if err := c.Delete().After("gorm:delete").Register("go_audit:after_delete", cbs.afterDelete); err != nil {
		return err
	}
	if err := c.Delete().After("gorm:after_delete").Register("go_audit:leave_delete", leave); err != nil {
		return err
	}
	return nil
}
