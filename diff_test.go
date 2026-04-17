package audit

import (
	"reflect"
	"testing"
)

func TestDiffResult_Create(t *testing.T) {
	new := map[string]any{"name": "A", "password": "secret"}
	oldOut, newOut := diffResult(ActionCreate, nil, new, []string{"password"})
	if oldOut != nil {
		t.Fatalf("old should be nil on create, got %v", oldOut)
	}
	want := map[string]any{"name": "A"}
	if !reflect.DeepEqual(newOut, want) {
		t.Fatalf("new = %v, want %v", newOut, want)
	}
}

func TestDiffResult_Update_OnlyChanged(t *testing.T) {
	old := map[string]any{"name": "A", "price": 100, "password": "x"}
	new := map[string]any{"name": "A", "price": 150, "password": "y"}
	oldOut, newOut := diffResult(ActionUpdate, old, new, []string{"password"})
	wantOld := map[string]any{"price": 100}
	wantNew := map[string]any{"price": 150}
	if !reflect.DeepEqual(oldOut, wantOld) {
		t.Fatalf("old = %v, want %v", oldOut, wantOld)
	}
	if !reflect.DeepEqual(newOut, wantNew) {
		t.Fatalf("new = %v, want %v", newOut, wantNew)
	}
}

func TestDiffResult_Update_NoChange(t *testing.T) {
	old := map[string]any{"name": "A"}
	new := map[string]any{"name": "A"}
	oldOut, newOut := diffResult(ActionUpdate, old, new, nil)
	if oldOut != nil || newOut != nil {
		t.Fatalf("expected both nil, got %v / %v", oldOut, newOut)
	}
}

func TestDiffResult_Delete(t *testing.T) {
	old := map[string]any{"name": "A", "password": "x"}
	oldOut, newOut := diffResult(ActionDelete, old, nil, []string{"password"})
	want := map[string]any{"name": "A"}
	if !reflect.DeepEqual(oldOut, want) {
		t.Fatalf("old = %v, want %v", oldOut, want)
	}
	if newOut != nil {
		t.Fatalf("new should be nil on delete")
	}
}

func TestDiffResult_SoftDelete(t *testing.T) {
	old := map[string]any{"name": "A", "price": 100, "password": "x"}
	new := map[string]any{"deleted_at": "2026-04-16T00:00:00Z"}
	oldOut, newOut := diffResult(ActionSoftDelete, old, new, []string{"password"})
	wantOld := map[string]any{"name": "A", "price": 100}
	wantNew := map[string]any{"deleted_at": "2026-04-16T00:00:00Z"}
	if !reflect.DeepEqual(oldOut, wantOld) {
		t.Fatalf("old = %v, want %v", oldOut, wantOld)
	}
	if !reflect.DeepEqual(newOut, wantNew) {
		t.Fatalf("new = %v, want %v", newOut, wantNew)
	}
}
