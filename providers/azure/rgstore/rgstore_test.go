package rgstore

import (
	"context"
	"testing"
)

func TestStoreIsCaseInsensitiveAndCopies(t *testing.T) {
	m := New(nil)

	if m.Put("SUB", "MyRG", map[string]any{"name": "MyRG", "tags": map[string]any{"env": "dev"}}) {
		t.Fatal("first Put reported an existing group")
	}

	if !m.Put("sub", "myrg", map[string]any{"name": "MyRG", "tags": map[string]any{"env": "prod"}}) {
		t.Fatal("second Put did not report the existing group")
	}

	m.Put("sub", "alpha", map[string]any{"name": "alpha"})
	m.Put("other", "beta", map[string]any{"name": "beta"})

	got, ok := m.Get("Sub", "MYRG")
	if !ok {
		t.Fatal("Get missed a case-different lookup")
	}

	got["tags"].(map[string]any)["env"] = "mutated"

	again, _ := m.Get("sub", "myrg")
	if env := again["tags"].(map[string]any)["env"]; env != "prod" {
		t.Fatalf("stored tags env = %v, want prod (Get must return a copy)", env)
	}

	list := m.List("SUB")
	if len(list) != 2 || list[0]["name"] != "alpha" || list[1]["name"] != "MyRG" {
		t.Fatalf("List(sub) = %v, want [alpha MyRG]", list)
	}

	if !m.Delete("sub", "MYRG") || m.Exists("sub", "myrg") || m.Delete("sub", "myrg") {
		t.Fatal("Delete did not remove the group exactly once")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := New(nil)
	src.Put("sub", "rg1", map[string]any{"name": "rg1", "location": "westeurope"})

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	dst := New(nil)
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	got, ok := dst.Get("sub", "rg1")
	if !ok || got["location"] != "westeurope" {
		t.Fatalf("restored group = %v, %v", got, ok)
	}
}
