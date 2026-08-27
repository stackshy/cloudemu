// Nested field-path updateMask behavior, driven by the real
// cloud.google.com/go/firestore SDK (REST client) against the emulator's GCP
// HTTP server. A dotted field path (e.g. "profile.age") addresses a nested
// field; before the fix the emulator treated it as a flat top-level key, so the
// nested write was silently dropped (data loss). These tests assert real
// Firestore semantics: the nested leaf is written, siblings are preserved, and a
// masked nested path absent from the write deletes only that leaf.
package firestore_test

import (
	"testing"

	gcpfirestore "cloud.google.com/go/firestore"
)

// nestedMap pulls out a nested map[string]any field from a snapshot payload.
func nestedMap(t *testing.T, data map[string]any, key string) map[string]any {
	t.Helper()

	m, ok := data[key].(map[string]any)
	if !ok {
		t.Fatalf("field %q is not a nested map: %#v", key, data[key])
	}

	return m
}

// TestDatabaseNestedFieldPathUpdate asserts that Update with a dotted field
// path writes the nested leaf, leaves sibling nested fields untouched, and that
// updating one nested key does not disturb another top-level nested map.
func TestDatabaseNestedFieldPathUpdate(t *testing.T) {
	ctx, client, _ := newDBClient(t, "people")

	doc := client.Collection("people").Doc("u1")

	if _, err := doc.Set(ctx, map[string]any{
		"name": "root",
		"profile": map[string]any{
			"age":  int64(30),
			"name": "alice",
		},
		"settings": map[string]any{
			"theme": "dark",
		},
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Dotted field path addresses profile.age (the data-loss repro).
	if _, err := doc.Update(ctx, []gcpfirestore.Update{{Path: "profile.age", Value: int64(31)}}); err != nil {
		t.Fatalf("Update nested: %v", err)
	}

	snap, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	data := snap.Data()

	profile := nestedMap(t, data, "profile")
	if profile["age"] != int64(31) {
		t.Errorf("profile.age=%v want 31 (nested write was dropped)", profile["age"])
	}

	// Sibling nested field untouched.
	if profile["name"] != "alice" {
		t.Errorf("profile.name=%v want alice (sibling nested field must survive)", profile["name"])
	}

	// Untouched top-level nested map preserved.
	settings := nestedMap(t, data, "settings")
	if settings["theme"] != "dark" {
		t.Errorf("settings.theme=%v want dark (unmasked field must survive)", settings["theme"])
	}

	// Top-level field preserved.
	if data["name"] != "root" {
		t.Errorf("name=%v want root", data["name"])
	}
}

// TestDatabaseNestedFieldPathCreatesIntermediate asserts a dotted path writes
// through into a newly created nested map when the intermediate is absent.
func TestDatabaseNestedFieldPathCreatesIntermediate(t *testing.T) {
	ctx, client, _ := newDBClient(t, "people")

	doc := client.Collection("people").Doc("u2")

	if _, err := doc.Set(ctx, map[string]any{"name": "root"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// address.city addresses a nested leaf under a not-yet-existing map.
	if _, err := doc.Update(ctx, []gcpfirestore.Update{
		{Path: "address.city", Value: "Berlin"},
	}); err != nil {
		t.Fatalf("Update nested (new intermediate): %v", err)
	}

	snap, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	data := snap.Data()

	addr := nestedMap(t, data, "address")
	if addr["city"] != "Berlin" {
		t.Errorf("address.city=%v want Berlin (intermediate map must be created)", addr["city"])
	}

	if data["name"] != "root" {
		t.Errorf("name=%v want root", data["name"])
	}
}

// TestDatabaseNestedFieldPathDelete asserts that deleting a nested field via a
// dotted path removes only that leaf and preserves its siblings.
func TestDatabaseNestedFieldPathDelete(t *testing.T) {
	ctx, client, _ := newDBClient(t, "people")

	doc := client.Collection("people").Doc("u3")

	if _, err := doc.Set(ctx, map[string]any{
		"profile": map[string]any{
			"age":  int64(30),
			"name": "alice",
		},
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Delete just profile.age; the mask carries "profile.age" but the write
	// omits the value, which must delete only that nested leaf.
	if _, err := doc.Update(ctx, []gcpfirestore.Update{
		{Path: "profile.age", Value: gcpfirestore.Delete},
	}); err != nil {
		t.Fatalf("Update (nested delete): %v", err)
	}

	snap, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	profile := nestedMap(t, snap.Data(), "profile")

	if _, present := profile["age"]; present {
		t.Errorf("profile.age still present after nested delete: %#v", profile)
	}

	if profile["name"] != "alice" {
		t.Errorf("profile.name=%v want alice (sibling must survive nested delete)", profile["name"])
	}
}

// TestDatabaseTopLevelUpdateStillWorks guards the pre-existing #639 behavior:
// a single-segment masked update writes the field and preserves unmasked ones.
func TestDatabaseTopLevelUpdateStillWorks(t *testing.T) {
	ctx, client, _ := newDBClient(t, "people")

	doc := client.Collection("people").Doc("u4")

	if _, err := doc.Set(ctx, map[string]any{"keep": "original", "change": "old"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if _, err := doc.Update(ctx, []gcpfirestore.Update{{Path: "change", Value: "new"}}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	snap, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	data := snap.Data()

	if data["change"] != "new" {
		t.Errorf("change=%v want new", data["change"])
	}

	if data["keep"] != "original" {
		t.Errorf("keep=%v want original (unmasked top-level field must survive)", data["keep"])
	}
}
