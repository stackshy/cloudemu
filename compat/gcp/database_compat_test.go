package gcp

import (
	"context"
	"errors"
	"fmt"
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
