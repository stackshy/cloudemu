package firestore_test

import (
	"context"
	"net/http/httptest"
	"testing"

	gcpfirestore "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu"
	dbdriver "github.com/stackshy/cloudemu/database/driver"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

const testProject = "p1"

// TestSDKFirestoreRoundTrip drives Firestore document operations with the
// real cloud.google.com/go/firestore client (REST mode) against our handler.
func TestSDKFirestoreRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewGCP()

	// Create the table the handler writes into. Firestore's logical model
	// has dynamic collection names, but our underlying driver wants tables
	// pre-declared.
	ctx := context.Background()
	_ = cloudP.Firestore.CreateTable(ctx, dbdriver.TableConfig{
		Name: "users", PartitionKey: "id",
	})

	srv := gcpserver.New(gcpserver.Drivers{Firestore: cloudP.Firestore})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client, err := gcpfirestore.NewRESTClient(ctx, testProject,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	coll := client.Collection("users")

	// Set (create) a document.
	docRef := coll.Doc("u1")
	if _, err := docRef.Set(ctx, map[string]any{
		"name": "Alice",
		"age":  30,
		"active": true,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get the document back.
	snap, err := docRef.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	got := snap.Data()

	if got["name"] != "Alice" {
		t.Errorf("name=%v want Alice", got["name"])
	}

	// Firestore returns int64 for integer values.
	if got["age"] != int64(30) {
		t.Errorf("age=%v want 30", got["age"])
	}

	if got["active"] != true {
		t.Errorf("active=%v want true", got["active"])
	}

	// List documents in the collection.
	it := coll.Documents(ctx)

	seen := map[string]bool{}
	for {
		s, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			t.Fatalf("iterator: %v", err)
		}

		seen[s.Ref.ID] = true
	}

	if !seen["u1"] {
		t.Errorf("u1 not in list: %v", seen)
	}

	// Update the document.
	if _, err := docRef.Set(ctx, map[string]any{
		"name": "Alice Updated",
		"age":  31,
	}); err != nil {
		t.Fatalf("Set (update): %v", err)
	}

	snap2, err := docRef.Get(ctx)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}

	if snap2.Data()["name"] != "Alice Updated" {
		t.Errorf("after update: name=%v", snap2.Data()["name"])
	}

	// Delete the document.
	if _, err := docRef.Delete(ctx); err != nil {
		t.Errorf("Delete: %v", err)
	}
}
