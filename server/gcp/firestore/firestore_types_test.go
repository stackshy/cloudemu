package firestore_test

import (
	"testing"
	"time"

	gcpfirestore "cloud.google.com/go/firestore"
)

// TestFirestoreValueTypeFidelity drives the real Firestore SDK to prove that
// timestamp, bytes, reference, and integer-valued double fields round-trip with
// their Go type preserved (GCP audit fidelity findings), rather than decaying to
// string / nil / int64.
func TestFirestoreValueTypeFidelity(t *testing.T) {
	ctx, client, _ := newDBClient(t, "types")

	coll := client.Collection("types")
	doc := coll.Doc("d1")
	target := coll.Doc("target")

	ts := time.Date(2021, 5, 4, 3, 2, 1, 0, time.UTC)

	if _, err := doc.Set(ctx, map[string]any{
		"createdAt": ts,
		"blob":      []byte("hello"),
		"ref":       target,
		"score":     30.0, // integer-valued double
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	snap, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	got := snap.Data()

	if gt, ok := got["createdAt"].(time.Time); !ok || !gt.Equal(ts) {
		t.Errorf("createdAt=%v (%T) want time.Time %v", got["createdAt"], got["createdAt"], ts)
	}

	if gb, ok := got["blob"].([]byte); !ok || string(gb) != "hello" {
		t.Errorf("blob=%v (%T) want []byte(hello)", got["blob"], got["blob"])
	}

	if gr, ok := got["ref"].(*gcpfirestore.DocumentRef); !ok || gr.ID != "target" {
		t.Errorf("ref=%v (%T) want *DocumentRef id=target", got["ref"], got["ref"])
	}

	if gs, ok := got["score"].(float64); !ok || gs != 30.0 {
		t.Errorf("score=%v (%T) want float64(30)", got["score"], got["score"])
	}
}

// TestFirestoreStableCommitTimestamps proves that createTime/updateTime are
// stable commit timestamps: two reads of an unchanged document agree, and an
// update preserves createTime (GCP audit finding: timestamps regenerated on
// every read).
func TestFirestoreStableCommitTimestamps(t *testing.T) {
	ctx, client, _ := newDBClient(t, "stamps")

	doc := client.Collection("stamps").Doc("d1")

	if _, err := doc.Set(ctx, map[string]any{"v": 1}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	s1, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get 1: %v", err)
	}

	s2, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get 2: %v", err)
	}

	if !s1.CreateTime.Equal(s2.CreateTime) {
		t.Errorf("createTime unstable across reads: %v vs %v", s1.CreateTime, s2.CreateTime)
	}

	if !s1.UpdateTime.Equal(s2.UpdateTime) {
		t.Errorf("updateTime unstable across reads: %v vs %v", s1.UpdateTime, s2.UpdateTime)
	}

	// An update must preserve the original createTime.
	if _, err := doc.Set(ctx, map[string]any{"v": 2}); err != nil {
		t.Fatalf("Set (update): %v", err)
	}

	s3, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("Get 3: %v", err)
	}

	if !s3.CreateTime.Equal(s1.CreateTime) {
		t.Errorf("createTime not preserved after update: %v vs %v", s3.CreateTime, s1.CreateTime)
	}
}
