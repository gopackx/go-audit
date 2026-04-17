package ent

import (
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
