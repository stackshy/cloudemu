package gcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"

	gcpfirestore "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// TestGCPdatabaseCompat drives a Firestore document lifecycle through the real
// cloud.google.com/go/firestore client (REST mode) against CloudEmu's wire
// server. Firestore documents map onto the portable "database" driver, so the
// operation names match DynamoDB's in docs/coverage/coverage.json: the SDK
// Set maps to PutItem, Get to GetItem, Delete to DeleteItem, and a collection
// query to Scan.
func TestGCPdatabaseCompat(t *testing.T) {
	provider := cloudemu.NewGCP()

	// Firestore's logical model has dynamic collection names, but the portable
	// database driver wants tables declared up front. The handler auto-creates
	// on write, but declaring it here keeps the collection deterministic.
	ctx := context.Background()
	if err := provider.Firestore.CreateTable(ctx, dbdriver.TableConfig{
		Name: collection, PartitionKey: "id",
	}); err != nil {
		t.Fatalf("seed collection: %v", err)
	}

	sess := compat.BootGCP(t, gcpserver.Drivers{Firestore: provider.Firestore})

	client, err := gcpfirestore.NewRESTClient(ctx, compat.GCPProject,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("firestore REST client: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	const svc = "database"

	coll := client.Collection(collection)
	docRef := coll.Doc(docID)

	sess.Op(svc, "PutItem", func() error {
		_, err := docRef.Set(ctx, map[string]any{
			"name":   "Alice",
			"age":    wantAge,
			"active": true,
		})

		return err
	})

	sess.Op(svc, "GetItem", func() error {
		snap, err := docRef.Get(ctx)
		if err != nil {
			return err
		}

		got := snap.Data()
		if got["name"] != "Alice" {
			return fmt.Errorf("name=%v want Alice", got["name"])
		}

		if got["age"] != int64(wantAge) {
			return fmt.Errorf("age=%v want %d", got["age"], wantAge)
		}

		return nil
	})

	sess.Op(svc, "Scan", func() error {
		it := coll.Documents(ctx)

		seen := false

		for {
			s, err := it.Next()
			if errors.Is(err, iterator.Done) {
				break
			}

			if err != nil {
				return err
			}

			if s.Ref.ID == docID {
				seen = true
			}
		}

		if !seen {
			return fmt.Errorf("document %q not returned by scan", docID)
		}

		return nil
	})

	sess.Op(svc, "DeleteItem", func() error {
		if _, err := docRef.Delete(ctx); err != nil {
			return err
		}

		if _, err := docRef.Get(ctx); err == nil {
			return errors.New("document still readable after delete")
		}

		return nil
	})
}

const (
	collection = "users"
	docID      = "u1"
	wantAge    = 30
)

// TestFirestoreListCollectionIDsScoped proves listCollectionIds is scoped to the
// requested parent: a root call returns only the immediate top-level collection
// ids and a per-document call returns only that document's direct subcollection
// ids — each a single id segment, never a full nested path, and never another
// document's subcollections. Pre-fix, both returned every driver table name
// verbatim (root yielded "cities/SF/landmarks"; a document call leaked unrelated
// collections).
func TestFirestoreListCollectionIDsScoped(t *testing.T) {
	provider := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{Firestore: provider.Firestore})
	ctx := context.Background()

	client, err := gcpfirestore.NewRESTClient(ctx, compat.GCPProject,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("firestore REST client: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	// Two top-level collections (cities, users) and a subcollection (landmarks)
	// under the cities/SF document.
	writes := []struct {
		ref  *gcpfirestore.DocumentRef
		data map[string]any
	}{
		{client.Collection("cities").Doc("SF"), map[string]any{"name": "San Francisco"}},
		{client.Collection("cities").Doc("SF").Collection("landmarks").Doc("GG"), map[string]any{"name": "Golden Gate"}},
		{client.Collection("users").Doc("alice"), map[string]any{"name": "Alice"}},
	}
	for _, wr := range writes {
		if _, serr := wr.ref.Set(ctx, wr.data); serr != nil {
			t.Fatalf("seed %s: %v", wr.ref.Path, serr)
		}
	}

	rootIDs := collectCollectionIDs(t, client.Collections(ctx))
	if !equalStrings(rootIDs, []string{"cities", "users"}) {
		t.Fatalf("root listCollectionIds = %v, want [cities users]", rootIDs)
	}

	docIDs := collectCollectionIDs(t, client.Collection("cities").Doc("SF").Collections(ctx))
	if !equalStrings(docIDs, []string{"landmarks"}) {
		t.Fatalf("cities/SF listCollectionIds = %v, want [landmarks]", docIDs)
	}
}

// collectCollectionIDs drains a CollectionIterator into a sorted slice of ids.
func collectCollectionIDs(t *testing.T, it *gcpfirestore.CollectionIterator) []string {
	t.Helper()

	var ids []string

	for {
		ref, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			t.Fatalf("collection iterator: %v", err)
		}

		ids = append(ids, ref.ID)
	}

	sort.Strings(ids)

	return ids
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
