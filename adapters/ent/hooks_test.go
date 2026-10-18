package ent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/gopackx/go-audit"

	"entgo.io/ent"
)

func TestMapAction(t *testing.T) {
	cases := []struct {
		op   ent.Op
		want string
	}{
		{ent.OpCreate, audit.ActionCreate},
		{ent.OpUpdate, audit.ActionUpdate},
		{ent.OpUpdateOne, audit.ActionUpdate},
		{ent.OpDelete, audit.ActionDelete},
		{ent.OpDeleteOne, audit.ActionDelete},
	}
	for _, c := range cases {
		if got := mapAction(c.op); got != c.want {
			t.Errorf("mapAction(%v) = %q; want %q", c.op, got, c.want)
		}
	}
}

type fakeEntity struct {
	ID   int
	Name string
}

func TestIDFromResult(t *testing.T) {
	v := &fakeEntity{ID: 42, Name: "x"}
	id, ok := idFromResult(v)
	if !ok {
		t.Fatalf("expected ID extraction to succeed")
	}
	if id.(int) != 42 {
		t.Fatalf("want id=42, got %v", id)
	}

	// Nil value must not panic.
	if _, ok := idFromResult(nil); ok {
		t.Fatalf("idFromResult(nil) should fail")
	}

	// Non-struct (int) — no ID field.
	if _, ok := idFromResult(ent.Value(5)); ok {
		t.Fatalf("idFromResult(int) should fail")
	}
}

func TestStringifyID(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{42, "42"},
		{int64(1234567890), "1234567890"},
		{"u_abc", "u_abc"},
	}
	for _, c := range cases {
		if got := stringifyID(c.in); got != c.want {
			t.Errorf("stringifyID(%v) = %q; want %q", c.in, got, c.want)
		}
	}
}

// errStore wraps a real Store but always fails on SaveDataLog, simulating an
// audit-store outage. It counts attempts so bulk tests can prove every row
// was still offered to the store.
type errStore struct {
	audit.Store
	calls int
}

var errStoreDown = errors.New("audit store down")

func (s *errStore) SaveDataLog(context.Context, string, audit.AuditLog) error {
	s.calls++
	return errStoreDown
}

func newFailingAuditor(t *testing.T, mode audit.ErrorMode, logf func(string, ...any)) (audit.Auditor, *errStore) {
	t.Helper()
	st := &errStore{Store: audit.NewMemoryStore()}
	a, err := audit.NewWithStore(st, audit.DialectFor(audit.SQLite), audit.Config{
		Dialect:   audit.SQLite,
		UserFunc:  func(context.Context) (string, string) { return "tester", "user" },
		Logger:    logf,
		DataAudit: audit.DataAuditConfig{Enabled: true, OnError: mode},
	})
	if err != nil {
		t.Fatalf("NewWithStore: %v", err)
	}
	return a, st
}

// fakeMutation is a minimal ent.Mutation standing in for a generated one. It
// also exposes the ID() / IDs() helpers the adapter discovers by reflection.
type fakeMutation struct {
	op     ent.Op
	typ    string
	fields map[string]any
	id     int
	ids    []int
}

func (m *fakeMutation) Op() ent.Op      { return m.op }
func (m *fakeMutation) Type() string    { return m.typ }
func (m *fakeMutation) ID() (int, bool) { return m.id, m.id != 0 }
func (m *fakeMutation) IDs(context.Context) ([]int, error) {
	return m.ids, nil
}
func (m *fakeMutation) Fields() []string {
	out := make([]string, 0, len(m.fields))
	for k := range m.fields {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func (m *fakeMutation) Field(name string) (ent.Value, bool) {
	v, ok := m.fields[name]
	return v, ok
}
func (m *fakeMutation) SetField(name string, v ent.Value) error { m.fields[name] = v; return nil }
func (*fakeMutation) AddedFields() []string                     { return nil }
func (*fakeMutation) AddedField(string) (ent.Value, bool)       { return nil, false }
func (*fakeMutation) AddField(string, ent.Value) error          { return nil }
func (*fakeMutation) ClearedFields() []string                   { return nil }
func (*fakeMutation) FieldCleared(string) bool                  { return false }
func (*fakeMutation) ClearField(string) error                   { return nil }
func (*fakeMutation) ResetField(string) error                   { return nil }
func (*fakeMutation) AddedEdges() []string                      { return nil }
func (*fakeMutation) AddedIDs(string) []ent.Value               { return nil }
func (*fakeMutation) RemovedEdges() []string                    { return nil }
func (*fakeMutation) RemovedIDs(string) []ent.Value             { return nil }
func (*fakeMutation) ClearedEdges() []string                    { return nil }
func (*fakeMutation) EdgeCleared(string) bool                   { return false }
func (*fakeMutation) ClearEdge(string) error                    { return nil }
func (*fakeMutation) ResetEdge(string) error                    { return nil }
func (*fakeMutation) OldField(context.Context, string) (ent.Value, error) {
	return nil, errors.New("fakeMutation: no old values")
}

var _ ent.Mutation = (*fakeMutation)(nil)

// Under ErrorFailLoud the hook must return the store error from Mutate — after
// the mutation ran — so the caller sees it, like GORM's db.AddError.
func TestHook_ErrorFailLoud_SurfacesStoreError(t *testing.T) {
	ctx := context.Background()
	var logged []string
	a, st := newFailingAuditor(t, audit.ErrorFailLoud, func(f string, args ...any) {
		logged = append(logged, fmt.Sprintf(f, args...))
	})

	nextCalled := false
	next := ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
		nextCalled = true
		return &fakeEntity{ID: 7, Name: "ann"}, nil
	})
	m := &fakeMutation{op: ent.OpCreate, typ: "User", fields: map[string]any{"name": "ann"}}

	res, err := Hook(a)(next).Mutate(ctx, m)
	if !nextCalled {
		t.Fatalf("mutation must still execute")
	}
	if err == nil {
		t.Fatalf("expected store error to surface under ErrorFailLoud")
	}
	if !errors.Is(err, errStoreDown) {
		t.Fatalf("error should wrap the store error, got %v", err)
	}
	if !strings.HasPrefix(err.Error(), "go-audit: record create: ") {
		t.Fatalf("unexpected error text %q", err)
	}
	// The applied mutation's result is returned alongside the error.
	if e, ok := res.(*fakeEntity); !ok || e.ID != 7 {
		t.Fatalf("mutation result should be returned with the error, got %#v", res)
	}
	if st.calls != 1 {
		t.Fatalf("store should have been attempted once, got %d", st.calls)
	}
	if len(logged) != 0 {
		t.Fatalf("ErrorFailLoud must not log via Config.Logger, got %v", logged)
	}
}

// Bulk mutations keep recording every row even after one fails, and the
// returned error joins all failures.
func TestHook_ErrorFailLoud_BulkRecordsAllRowsThenFails(t *testing.T) {
	ctx := context.Background()
	a, st := newFailingAuditor(t, audit.ErrorFailLoud, nil)

	next := ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) { return 3, nil })
	m := &fakeMutation{op: ent.OpDelete, typ: "User", fields: map[string]any{}, ids: []int{1, 2, 3}}

	res, err := Hook(a)(next).Mutate(ctx, m)
	if res != 3 {
		t.Fatalf("mutation result should be returned, got %v", res)
	}
	if !errors.Is(err, errStoreDown) {
		t.Fatalf("error should wrap the store error, got %v", err)
	}
	if !strings.HasPrefix(err.Error(), "go-audit: record delete: ") {
		t.Fatalf("unexpected error text %q", err)
	}
	if st.calls != 3 {
		t.Fatalf("all 3 rows should have been offered to the store, got %d", st.calls)
	}
}

// Under ErrorFailSilent the auditor logs and returns nil, so the mutation
// completes normally.
func TestHook_ErrorFailSilent_SwallowsAndLogs(t *testing.T) {
	ctx := context.Background()
	var logged []string
	a, _ := newFailingAuditor(t, audit.ErrorFailSilent, func(f string, args ...any) {
		logged = append(logged, fmt.Sprintf(f, args...))
	})

	next := ent.MutateFunc(func(context.Context, ent.Mutation) (ent.Value, error) {
		return &fakeEntity{ID: 7, Name: "ann"}, nil
	})
	m := &fakeMutation{op: ent.OpCreate, typ: "User", fields: map[string]any{"name": "ann"}}

	if _, err := Hook(a)(next).Mutate(ctx, m); err != nil {
		t.Fatalf("ErrorFailSilent must swallow the store error, got %v", err)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "save data log") {
		t.Fatalf("auditor should have logged the swallowed error once, got %v", logged)
	}
}
