package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/gopackx/go-audit/dialect"
	"github.com/gopackx/go-audit/migrate"
	"github.com/gopackx/go-audit/store"
)

// New constructs an Auditor backed by database/sql. The caller supplies an
// open *sql.DB — go-audit never opens its own connection.
func New(db *sql.DB, cfg Config) (Auditor, error) {
	d := dialect.For(cfg.Dialect)
	if d == nil {
		return nil, fmt.Errorf("audit: unknown dialect %q", cfg.Dialect)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s := store.NewSQLStore(db, d)
	return newAuditor(s, d, cfg), nil
}

// NewWithStore builds an Auditor against any Store implementation (useful for
// tests with the in-memory store).
func NewWithStore(s Store, d Dialect, cfg Config) (Auditor, error) {
	if d == nil {
		return nil, errors.New("audit: dialect is required")
	}
	if s == nil {
		return nil, errors.New("audit: store is required")
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return newAuditor(s, d, cfg), nil
}

func newAuditor(s Store, d Dialect, cfg Config) *auditor {
	a := &auditor{store: s, dialect: d, cfg: cfg}
	a.api = &apiAuditor{a: a}
	return a
}

type auditor struct {
	store   Store
	dialect Dialect
	cfg     Config
	api     *apiAuditor
}

func (a *auditor) Config() Config  { return a.cfg }
func (a *auditor) API() APIAuditor { return a.api }

func (a *auditor) RecordDataChange(ctx context.Context, entry DataEntry) error {
	if !a.cfg.DataAudit.Enabled {
		return nil
	}
	for _, e := range a.cfg.DataAudit.ExcludeEntities {
		if entry.EntityType == e {
			return nil
		}
	}

	oldOut, newOut := diffResult(entry.Action, entry.OldValues, entry.NewValues, a.cfg.DataAudit.ExcludeFields)

	// For updates: if nothing changed after exclusion, skip persisting.
	if entry.Action == ActionUpdate && oldOut == nil && newOut == nil {
		return nil
	}

	oldJSON, err := jsonMarshal(oldOut)
	if err != nil {
		return err
	}
	newJSON, err := jsonMarshal(newOut)
	if err != nil {
		return err
	}
	metaJSON, err := jsonMarshal(entry.Metadata)
	if err != nil {
		return err
	}

	userID, userType := a.resolveUser(ctx)
	txID := entry.TransactionID
	if txID == "" {
		txID = TransactionIDFromContext(ctx)
	}

	row := AuditLog{
		EntityType:    entry.EntityType,
		EntityID:      entry.EntityID,
		Action:        entry.Action,
		OldValues:     oldJSON,
		NewValues:     newJSON,
		UserID:        userID,
		UserType:      userType,
		Metadata:      metaJSON,
		TransactionID: txID,
		CreatedAt:     time.Now().UTC(),
	}
	return a.handleError(a.cfg.DataAudit.OnError,
		a.store.SaveDataLog(ctx, a.cfg.DataAudit.Table, row),
		"save data log")
}

func (a *auditor) Query(ctx context.Context, filter DataFilter) ([]AuditLog, error) {
	return a.store.QueryDataLogs(ctx, a.cfg.DataAudit.Table, filter)
}

func (a *auditor) QueryByTransaction(ctx context.Context, txID string) (*TransactionLog, error) {
	if txID == "" {
		return nil, errors.New("audit: transaction id required")
	}
	out := &TransactionLog{TransactionID: txID}
	if a.cfg.DataAudit.Enabled {
		logs, err := a.store.QueryDataLogs(ctx, a.cfg.DataAudit.Table, DataFilter{TransactionID: txID})
		if err != nil {
			return nil, err
		}
		out.DataLogs = logs
	}
	if a.cfg.APIAudit.Enabled {
		logs, err := a.store.QueryAPILogs(ctx, a.cfg.APIAudit.Table, APIFilter{TransactionID: txID})
		if err != nil {
			return nil, err
		}
		out.APILogs = logs
	}
	return out, nil
}

func (a *auditor) AutoMigrate(ctx context.Context) error {
	return migrate.Migrate(ctx, a.store, a.dialect, migrate.Config{
		DataTable:   a.cfg.DataAudit.Table,
		APITable:    a.cfg.APIAudit.Table,
		DataEnabled: a.cfg.DataAudit.Enabled,
		APIEnabled:  a.cfg.APIAudit.Enabled,
	})
}

func (a *auditor) Purge(ctx context.Context, before time.Time) (PurgeResult, error) {
	var out PurgeResult
	if a.cfg.DataAudit.Enabled {
		n, err := a.store.Purge(ctx, a.cfg.DataAudit.Table, before)
		if err != nil {
			return out, fmt.Errorf("purge data logs: %w", err)
		}
		out.DataLogs = n
	}
	if a.cfg.APIAudit.Enabled {
		n, err := a.store.Purge(ctx, a.cfg.APIAudit.Table, before)
		if err != nil {
			return out, fmt.Errorf("purge api logs: %w", err)
		}
		out.APILogs = n
	}
	return out, nil
}

func (a *auditor) Snapshot(ctx context.Context, entityType, entityID string, at time.Time) (map[string]any, error) {
	if entityType == "" || entityID == "" {
		return nil, errors.New("audit: entity_type and entity_id are required")
	}
	if at.IsZero() {
		return nil, errors.New("audit: snapshot time is required")
	}

	// Fetch all logs for this entity up to `at`, newest first.
	logs, err := a.store.QueryDataLogs(ctx, a.cfg.DataAudit.Table, DataFilter{
		EntityType: entityType,
		EntityID:   entityID,
		DateTo:     at,
	})
	if err != nil {
		return nil, fmt.Errorf("audit: snapshot query: %w", err)
	}
	if len(logs) == 0 {
		return nil, nil
	}

	// Reverse to chronological order (oldest first) for replay.
	for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
		logs[i], logs[j] = logs[j], logs[i]
	}

	return replayLogs(logs)
}

func (a *auditor) Restore(ctx context.Context, entityType, entityID string, at time.Time) (*RestoreResult, error) {
	targetState, err := a.Snapshot(ctx, entityType, entityID, at)
	if err != nil {
		return nil, err
	}

	// Get current state (latest snapshot = now).
	currentState, err := a.Snapshot(ctx, entityType, entityID, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("audit: restore current snapshot: %w", err)
	}

	// Record the restore as an audit entry.
	entry := DataEntry{
		EntityType: entityType,
		EntityID:   entityID,
		Action:     ActionRestore,
		OldValues:  currentState,
		NewValues:  targetState,
	}
	if err := a.RecordDataChange(ctx, entry); err != nil {
		return nil, fmt.Errorf("audit: record restore: %w", err)
	}

	return &RestoreResult{
		EntityType: entityType,
		EntityID:   entityID,
		Values:     targetState,
		WasDeleted: targetState == nil,
	}, nil
}

// replayLogs applies audit log entries in chronological order to reconstruct
// entity state. Returns nil if the entity was deleted/soft-deleted.
func replayLogs(logs []AuditLog) (map[string]any, error) {
	var state map[string]any

	for _, l := range logs {
		switch l.Action {
		case ActionCreate, ActionRestore:
			state = map[string]any{}
			if len(l.NewValues) > 0 {
				if err := json.Unmarshal(l.NewValues, &state); err != nil {
					return nil, fmt.Errorf("audit: unmarshal new_values (log %d): %w", l.ID, err)
				}
			}

		case ActionUpdate:
			if state == nil {
				state = map[string]any{}
			}
			// Apply changed fields from new_values.
			if len(l.NewValues) > 0 {
				var nv map[string]any
				if err := json.Unmarshal(l.NewValues, &nv); err != nil {
					return nil, fmt.Errorf("audit: unmarshal new_values (log %d): %w", l.ID, err)
				}
				for k, v := range nv {
					state[k] = v
				}
			}
			// Remove fields that existed in old but not in new (field was deleted).
			if len(l.OldValues) > 0 && len(l.NewValues) > 0 {
				var ov map[string]any
				if err := json.Unmarshal(l.OldValues, &ov); err != nil {
					return nil, fmt.Errorf("audit: unmarshal old_values (log %d): %w", l.ID, err)
				}
				var nv map[string]any
				// NewValues already parsed above, but we need the keys.
				_ = json.Unmarshal(l.NewValues, &nv)
				for k := range ov {
					if _, exists := nv[k]; !exists {
						delete(state, k)
					}
				}
			}

		case ActionDelete, ActionSoftDelete:
			state = nil
		}
	}

	return state, nil
}

func (a *auditor) resolveUser(ctx context.Context) (string, string) {
	if a.cfg.UserFunc == nil {
		return "", ""
	}
	return a.cfg.UserFunc(ctx)
}

// handleError applies the configured ErrorMode. For ErrorFailSilent the error
// is logged and swallowed; otherwise it is returned verbatim.
func (a *auditor) handleError(mode ErrorMode, err error, what string) error {
	if err == nil {
		return nil
	}
	if mode == ErrorFailSilent {
		logf := a.cfg.Logger
		if logf == nil {
			logf = log.Printf
		}
		logf("go-audit: %s failed: %v", what, err)
		return nil
	}
	return err
}
