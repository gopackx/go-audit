package gorm

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	audit "github.com/gopackx/go-audit"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

// ProbeProduct is a plain model with an auto-increment PK.
type ProbeProduct struct {
	ID    uint
	Name  string
	Price float64
}

// ProbeSoft is the same shape with GORM soft delete.
type ProbeSoft struct {
	ID        uint
	Name      string
	Price     float64
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

// openLoggedDB opens an on-disk SQLite DB whose GORM logger writes every
// executed statement (with its [rows:N] count) into buf.
func openLoggedDB(t *testing.T, buf *bytes.Buffer) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "probe.db") + "?_pragma=busy_timeout(5000)"
	logger := gormlog.New(log.New(buf, "", 0), gormlog.Config{LogLevel: gormlog.Info})
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if s, err := db.DB(); err == nil {
			_ = s.Close()
		}
	})
	return db
}

func newProbeAuditor(t *testing.T, cfg audit.DataAuditConfig) audit.Auditor {
	t.Helper()
	cfg.Enabled = true
	a, err := audit.NewWithStore(audit.NewMemoryStore(), audit.DialectFor(audit.SQLite), audit.Config{
		Dialect:   audit.SQLite,
		UserFunc:  func(ctx context.Context) (string, string) { return "tester", "user" },
		DataAudit: cfg,
	})
	if err != nil {
		t.Fatalf("auditor: %v", err)
	}
	return a
}

// setupProbe migrates the probe tables, registers the plugin and seeds three
// ProbeProduct rows (ids 1..3, prices 10/30/50) without auditing them.
func setupProbe(t *testing.T, db *gorm.DB, cfg audit.DataAuditConfig) (audit.Auditor, []ProbeProduct) {
	t.Helper()
	if err := db.AutoMigrate(&ProbeProduct{}, &ProbeSoft{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	rows := []ProbeProduct{{Name: "a", Price: 10}, {Name: "b", Price: 30}, {Name: "c", Price: 50}}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newProbeAuditor(t, cfg)
	if err := db.Use(Plugin(a)); err != nil {
		t.Fatalf("plugin: %v", err)
	}
	return a, rows
}

var rowsRe = regexp.MustCompile(`\[rows:(-?\d+)\]\s+(.*)$`)

// firstSelect returns the first logged SELECT on table and its [rows:N].
func firstSelect(t *testing.T, logOut, table string) (string, int) {
	t.Helper()
	for _, line := range strings.Split(logOut, "\n") {
		m := rowsRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		sql := m[2]
		if strings.HasPrefix(sql, "SELECT") && strings.Contains(sql, table) {
			n, _ := strconv.Atoi(m[1])
			return sql, n
		}
	}
	t.Fatalf("no SELECT on %s logged; log:\n%s", table, logOut)
	return "", 0
}

func assertTargetedSnapshot(t *testing.T, op string, buf *bytes.Buffer, wantRows int) {
	t.Helper()
	sql, n := firstSelect(t, buf.String(), "probe_products")
	if !strings.Contains(sql, "WHERE") {
		t.Errorf("%s: snapshot SELECT has no WHERE (full-table scan): %s", op, sql)
	}
	if !strings.Contains(sql, "`id`") && !strings.Contains(sql, `"id"`) && !strings.Contains(sql, " id ") {
		t.Errorf("%s: snapshot SELECT does not reference the id column: %s", op, sql)
	}
	if n != wantRows {
		t.Errorf("%s: snapshot loaded %d rows, want %d: %s", op, n, wantRows, sql)
	}
}

// TestSnapshotIsScopedToPrimaryKey pins Defect 1: GORM adds the PK predicate
// only inside gorm:update/gorm:delete, so a snapshot that copied the WHERE
// clause alone ran "SELECT * FROM probe_products" for PK-driven writes.
func TestSnapshotIsScopedToPrimaryKey(t *testing.T) {
	var buf bytes.Buffer
	db := openLoggedDB(t, &buf)
	_, rows := setupProbe(t, db, audit.DataAuditConfig{})
	// Two extra rows so the batch delete below (2 rows) can't pass by luck.
	extra := []ProbeProduct{{Name: "d", Price: 70}, {Name: "e", Price: 90}}
	if err := db.Create(&extra).Error; err != nil {
		t.Fatalf("seed extra: %v", err)
	}

	buf.Reset()
	p1 := rows[0]
	if err := db.Model(&p1).Update("price", 99).Error; err != nil {
		t.Fatalf("update: %v", err)
	}
	assertTargetedSnapshot(t, "Model(&p).Update", &buf, 1)

	buf.Reset()
	p2 := rows[1]
	p2.Name = "b2"
	if err := db.Save(&p2).Error; err != nil {
		t.Fatalf("save: %v", err)
	}
	assertTargetedSnapshot(t, "Save(&p)", &buf, 1)

	buf.Reset()
	p3 := rows[2]
	if err := db.Delete(&p3).Error; err != nil {
		t.Fatalf("delete: %v", err)
	}
	assertTargetedSnapshot(t, "Delete(&p)", &buf, 1)

	buf.Reset()
	batch := []ProbeProduct{p1, p2}
	if err := db.Delete(&batch).Error; err != nil {
		t.Fatalf("batch delete: %v", err)
	}
	assertTargetedSnapshot(t, "Delete(&rows)", &buf, 2)
}

// TestSnapshotUnscopedHardDeleteOfSoftDeleted: the snapshot must inherit
// Unscoped, otherwise its implicit "deleted_at IS NULL" hides the row that
// db.Unscoped().Delete is about to remove.
func TestSnapshotUnscopedHardDeleteOfSoftDeleted(t *testing.T) {
	var buf bytes.Buffer
	db := openLoggedDB(t, &buf)
	a, _ := setupProbe(t, db, audit.DataAuditConfig{})

	s := ProbeSoft{Name: "s", Price: 5}
	if err := db.Create(&s).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := db.Delete(&s).Error; err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if err := db.Unscoped().Delete(&s).Error; err != nil {
		t.Fatalf("hard delete: %v", err)
	}
	logs, _ := a.Query(context.Background(), audit.DataFilter{EntityType: "probe_softs", Action: audit.ActionDelete})
	if len(logs) != 1 {
		t.Fatalf("hard delete entries: got %d, want 1", len(logs))
	}
	old := decode(t, logs[0].OldValues)
	if old["deleted_at"] == nil {
		t.Errorf("hard-delete old_values should come from the snapshot (incl. deleted_at), got %v", old)
	}
}

func decode(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return m
}

func assertNoBogusIDs(t *testing.T, logs []audit.AuditLog) {
	t.Helper()
	for _, l := range logs {
		if l.EntityID == "0" || l.EntityID == "" {
			t.Errorf("bogus entity_id %q on %s entry", l.EntityID, l.Action)
		}
	}
}

// TestBulkUpdateWhere_PerRowEntries pins Defect 2 for updates.
func TestBulkUpdateWhere_PerRowEntries(t *testing.T) {
	var buf bytes.Buffer
	db := openLoggedDB(t, &buf)
	a, _ := setupProbe(t, db, audit.DataAuditConfig{})

	res := db.Model(&ProbeProduct{}).Where("price >= ?", 30).Update("price", 1)
	if res.Error != nil || res.RowsAffected != 2 {
		t.Fatalf("bulk update: err=%v rows=%d", res.Error, res.RowsAffected)
	}
	logs, _ := a.Query(context.Background(), audit.DataFilter{EntityType: "probe_products"})
	assertNoBogusIDs(t, logs)
	if len(logs) != 2 {
		t.Fatalf("bulk update entries: got %d, want 2: %+v", len(logs), logs)
	}
	wantOld := map[string]float64{"2": 30, "3": 50}
	for _, l := range logs {
		if l.Action != audit.ActionUpdate {
			t.Errorf("action: got %s, want update", l.Action)
		}
		w, ok := wantOld[l.EntityID]
		if !ok {
			t.Errorf("unexpected entity_id %q", l.EntityID)
			continue
		}
		delete(wantOld, l.EntityID)
		old, nw := decode(t, l.OldValues), decode(t, l.NewValues)
		if old["price"] != w {
			t.Errorf("id %s old price: got %v, want %v", l.EntityID, old["price"], w)
		}
		if nw["price"] != float64(1) {
			t.Errorf("id %s new price: got %v, want 1", l.EntityID, nw["price"])
		}
	}
}

// TestBulkDeleteWhere_PerRowEntries pins Defect 2 for hard and soft deletes.
func TestBulkDeleteWhere_PerRowEntries(t *testing.T) {
	var buf bytes.Buffer
	db := openLoggedDB(t, &buf)
	a, _ := setupProbe(t, db, audit.DataAuditConfig{})

	res := db.Where("price >= ?", 30).Delete(&ProbeProduct{})
	if res.Error != nil || res.RowsAffected != 2 {
		t.Fatalf("bulk delete: err=%v rows=%d", res.Error, res.RowsAffected)
	}
	logs, _ := a.Query(context.Background(), audit.DataFilter{EntityType: "probe_products"})
	assertNoBogusIDs(t, logs)
	if len(logs) != 2 {
		t.Fatalf("bulk delete entries: got %d, want 2: %+v", len(logs), logs)
	}
	ids := map[string]bool{}
	for _, l := range logs {
		ids[l.EntityID] = true
		if l.Action != audit.ActionDelete {
			t.Errorf("action: got %s, want delete", l.Action)
		}
		if old := decode(t, l.OldValues); old["price"] == nil || old["name"] == nil {
			t.Errorf("id %s old_values missing snapshot: %v", l.EntityID, old)
		}
		if nw := decode(t, l.NewValues); nw != nil {
			t.Errorf("id %s new_values: got %v, want nil", l.EntityID, nw)
		}
	}
	if !ids["2"] || !ids["3"] {
		t.Errorf("entity ids: got %v, want 2 and 3", ids)
	}

	// Soft-delete flavour.
	softs := []ProbeSoft{{Name: "x", Price: 10}, {Name: "y", Price: 30}, {Name: "z", Price: 50}}
	if err := db.Create(&softs).Error; err != nil {
		t.Fatalf("seed softs: %v", err)
	}
	if err := db.Where("price >= ?", 30).Delete(&ProbeSoft{}).Error; err != nil {
		t.Fatalf("bulk soft delete: %v", err)
	}
	slogs, _ := a.Query(context.Background(), audit.DataFilter{EntityType: "probe_softs", Action: audit.ActionSoftDelete})
	assertNoBogusIDs(t, slogs)
	if len(slogs) != 2 {
		t.Fatalf("bulk soft delete entries: got %d, want 2: %+v", len(slogs), slogs)
	}
	for _, l := range slogs {
		if old := decode(t, l.OldValues); old["price"] == nil {
			t.Errorf("soft id %s old_values missing snapshot: %v", l.EntityID, old)
		}
		if nw := decode(t, l.NewValues); nw["deleted_at"] == nil {
			t.Errorf("soft id %s new_values missing deleted_at: %v", l.EntityID, nw)
		}
	}
}

// TestBulkWriteWithoutSnapshot_EmitsNothing: with SkipOldValues there is no
// snapshot and the model has no PK, so nothing identifiable can be audited —
// the adapter must not invent an entity_id "0" entry.
func TestBulkWriteWithoutSnapshot_EmitsNothing(t *testing.T) {
	var buf bytes.Buffer
	db := openLoggedDB(t, &buf)
	a, _ := setupProbe(t, db, audit.DataAuditConfig{SkipOldValues: true})

	if err := db.Model(&ProbeProduct{}).Where("price >= ?", 30).Update("price", 1).Error; err != nil {
		t.Fatalf("bulk update: %v", err)
	}
	if err := db.Where("price >= ?", 1).Delete(&ProbeProduct{}).Error; err != nil {
		t.Fatalf("bulk delete: %v", err)
	}
	logs, _ := a.Query(context.Background(), audit.DataFilter{EntityType: "probe_products"})
	if len(logs) != 0 {
		t.Errorf("entries without snapshot: got %d, want 0: %+v", len(logs), logs)
	}
}

// TestPerPKFlowsUnchanged guards the classic single-row flows: each must still
// produce exactly one entry with the right old/new values.
func TestPerPKFlowsUnchanged(t *testing.T) {
	var buf bytes.Buffer
	db := openLoggedDB(t, &buf)
	a, rows := setupProbe(t, db, audit.DataAuditConfig{})
	ctx := context.Background()

	one := func(action, id string) audit.AuditLog {
		t.Helper()
		logs, _ := a.Query(ctx, audit.DataFilter{Action: action, EntityID: id})
		if len(logs) != 1 {
			t.Fatalf("%s id=%s: got %d entries, want 1: %+v", action, id, len(logs), logs)
		}
		return logs[0]
	}

	// Single update.
	p1 := rows[0]
	if err := db.Model(&p1).Update("price", 11).Error; err != nil {
		t.Fatalf("update: %v", err)
	}
	l := one(audit.ActionUpdate, "1")
	if o, n := decode(t, l.OldValues), decode(t, l.NewValues); o["price"] != float64(10) || n["price"] != float64(11) {
		t.Errorf("update old/new: %v -> %v", o, n)
	}

	// Save.
	p2 := rows[1]
	p2.Name = "b-renamed"
	if err := db.Save(&p2).Error; err != nil {
		t.Fatalf("save: %v", err)
	}
	l = one(audit.ActionUpdate, "2")
	if o, n := decode(t, l.OldValues), decode(t, l.NewValues); o["name"] != "b" || n["name"] != "b-renamed" {
		t.Errorf("save old/new: %v -> %v", o, n)
	}

	// Hard delete.
	p3 := rows[2]
	if err := db.Delete(&p3).Error; err != nil {
		t.Fatalf("delete: %v", err)
	}
	l = one(audit.ActionDelete, "3")
	if o, n := decode(t, l.OldValues), decode(t, l.NewValues); o["price"] != float64(50) || n != nil {
		t.Errorf("delete old/new: %v -> %v", o, n)
	}

	// Soft delete.
	s := ProbeSoft{Name: "s", Price: 5}
	if err := db.Create(&s).Error; err != nil {
		t.Fatalf("create soft: %v", err)
	}
	if err := db.Delete(&s).Error; err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	logs, _ := a.Query(ctx, audit.DataFilter{EntityType: "probe_softs", Action: audit.ActionSoftDelete})
	if len(logs) != 1 || logs[0].EntityID != strconv.Itoa(int(s.ID)) {
		t.Fatalf("soft delete entries: %+v", logs)
	}
	if o, n := decode(t, logs[0].OldValues), decode(t, logs[0].NewValues); o["price"] != float64(5) || n["deleted_at"] == nil {
		t.Errorf("soft delete old/new: %v -> %v", o, n)
	}

	all, _ := a.Query(ctx, audit.DataFilter{EntityType: "probe_products"})
	assertNoBogusIDs(t, all)
	if len(all) != 3 {
		t.Errorf("probe_products entries: got %d, want 3", len(all))
	}
}

// TestGlobalUpdateSnapshotsEveryRow: with AllowGlobalUpdate a WHERE-less write
// genuinely touches every row, so the full-table snapshot is the correct
// pre-image and each row gets its own entry.
func TestGlobalUpdateSnapshotsEveryRow(t *testing.T) {
	var buf bytes.Buffer
	db := openLoggedDB(t, &buf)
	a, _ := setupProbe(t, db, audit.DataAuditConfig{})

	err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).
		Model(&ProbeProduct{}).Update("price", 4).Error
	if err != nil {
		t.Fatalf("global update: %v", err)
	}
	logs, _ := a.Query(context.Background(), audit.DataFilter{EntityType: "probe_products"})
	assertNoBogusIDs(t, logs)
	if len(logs) != 3 {
		t.Errorf("global update entries: got %d, want 3: %+v", len(logs), logs)
	}
}
