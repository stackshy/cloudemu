package gcs

import (
	"context"
	"testing"
)

// TestMetadataIsolation locks the boundary-copy behavior: mutating the
// metadata map a caller passed to PutObject, or the map returned from
// HeadObject, must not change what the store holds.
func TestMetadataIsolation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if err := m.CreateBucket(ctx, "iso"); err != nil {
		t.Fatal(err)
	}

	meta := map[string]string{"owner": "team-a"}
	if err := m.PutObject(ctx, "iso", "k", []byte("v"), "text/plain", meta); err != nil {
		t.Fatal(err)
	}

	meta["owner"] = "attacker"
	meta["extra"] = "leaked"

	info, err := m.HeadObject(ctx, "iso", "k")
	if err != nil {
		t.Fatal(err)
	}
	if info.Metadata["owner"] != "team-a" || len(info.Metadata) != 1 {
		t.Fatalf("stored metadata corrupted by caller mutation: %v", info.Metadata)
	}

	info.Metadata["owner"] = "mutated"
	again, err := m.HeadObject(ctx, "iso", "k")
	if err != nil {
		t.Fatal(err)
	}
	if again.Metadata["owner"] != "team-a" {
		t.Fatalf("stored metadata corrupted via returned map: %v", again.Metadata)
	}
}
