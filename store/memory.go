package store

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopackx/go-audit/entry"
)

// NewMemoryStore returns an in-memory Store, intended for tests.
func NewMemoryStore() Store {
	return &memStore{}
}

type memStore struct {
	mu     sync.Mutex
	nextID atomic.Uint64
	data   map[string][]entry.AuditLog
	api    map[string][]entry.AuditAPILog
}

func (m *memStore) Exec(ctx context.Context, stmt string) error { return nil }

func (m *memStore) SaveDataLog(ctx context.Context, table string, log entry.AuditLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string][]entry.AuditLog{}
	}
	log.ID = m.nextID.Add(1)
	m.data[table] = append(m.data[table], log)
	return nil
}

func (m *memStore) SaveAPILog(ctx context.Context, table string, log entry.AuditAPILog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.api == nil {
		m.api = map[string][]entry.AuditAPILog{}
	}
	log.ID = m.nextID.Add(1)
	m.api[table] = append(m.api[table], log)
	return nil
}

func (m *memStore) QueryDataLogs(ctx context.Context, table string, f entry.DataFilter) ([]entry.AuditLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []entry.AuditLog
	skipped := 0
	for i := len(m.data[table]) - 1; i >= 0; i-- {
		l := m.data[table][i]
		if f.EntityType != "" && l.EntityType != f.EntityType {
			continue
		}
		if f.EntityID != "" && l.EntityID != f.EntityID {
			continue
		}
		if f.Action != "" && l.Action != f.Action {
			continue
		}
		if f.UserID != "" && l.UserID != f.UserID {
			continue
		}
		if f.TransactionID != "" && l.TransactionID != f.TransactionID {
			continue
		}
		if !f.DateFrom.IsZero() && l.CreatedAt.Before(f.DateFrom) {
			continue
		}
		if !f.DateTo.IsZero() && l.CreatedAt.After(f.DateTo) {
			continue
		}
		if f.Offset > 0 && skipped < f.Offset {
			skipped++
			continue
		}
		out = append(out, l)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

func (m *memStore) Purge(ctx context.Context, table string, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var removed int64
	if rows, ok := m.data[table]; ok {
		kept := rows[:0]
		for _, r := range rows {
			if r.CreatedAt.Before(before) {
				removed++
				continue
			}
			kept = append(kept, r)
		}
		m.data[table] = kept
	}
	if rows, ok := m.api[table]; ok {
		kept := rows[:0]
		for _, r := range rows {
			if r.CreatedAt.Before(before) {
				removed++
				continue
			}
			kept = append(kept, r)
		}
		m.api[table] = kept
	}
	return removed, nil
}

func (m *memStore) QueryAPILogs(ctx context.Context, table string, f entry.APIFilter) ([]entry.AuditAPILog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []entry.AuditAPILog
	skipped := 0
	for i := len(m.api[table]) - 1; i >= 0; i-- {
		l := m.api[table][i]
		if f.Service != "" && l.Service != f.Service {
			continue
		}
		if f.StatusCode != 0 && l.StatusCode != f.StatusCode {
			continue
		}
		if f.UserID != "" && l.UserID != f.UserID {
			continue
		}
		if f.TransactionID != "" && l.TransactionID != f.TransactionID {
			continue
		}
		if !f.DateFrom.IsZero() && l.CreatedAt.Before(f.DateFrom) {
			continue
		}
		if !f.DateTo.IsZero() && l.CreatedAt.After(f.DateTo) {
			continue
		}
		if f.Offset > 0 && skipped < f.Offset {
			skipped++
			continue
		}
		out = append(out, l)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}
