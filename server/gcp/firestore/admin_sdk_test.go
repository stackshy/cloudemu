package firestore_test

import (
	"context"
	"net/http/httptest"
	"testing"

	gcpfirestore "cloud.google.com/go/firestore"
	admin "cloud.google.com/go/firestore/apiv1/admin"
	"cloud.google.com/go/firestore/apiv1/admin/adminpb"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// newAdminTestServer starts a full GCP server with the Firestore driver (so both
// the admin and data-plane handlers are registered) and returns an admin REST
// client pointed at it.
func newAdminTestServer(t *testing.T) (*httptest.Server, *admin.FirestoreAdminClient) {
	t.Helper()

	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Firestore: cloudP.Firestore})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	client, err := admin.NewFirestoreAdminRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewFirestoreAdminRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return ts, client
}

// TestSDKFirestoreAdminDatabaseLifecycle exercises the projects.databases surface
// end-to-end with the real firestore admin GAPIC client: create (LRO -> done),
// get, list, patch, and delete (with the delete-protection guard).
func TestSDKFirestoreAdminDatabaseLifecycle(t *testing.T) {
	_, client := newAdminTestServer(t)
	ctx := context.Background()

	op, err := client.CreateDatabase(ctx, &adminpb.CreateDatabaseRequest{
		Parent:     "projects/p1",
		DatabaseId: "db1",
		Database: &adminpb.Database{
			Type:       adminpb.Database_FIRESTORE_NATIVE,
			LocationId: "us-central1",
		},
	})
	if err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	db, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("CreateDatabase Wait: %v", err)
	}

	if db.GetName() != "projects/p1/databases/db1" {
		t.Errorf("name=%q", db.GetName())
	}

	if db.GetType() != adminpb.Database_FIRESTORE_NATIVE {
		t.Errorf("type=%v want FIRESTORE_NATIVE", db.GetType())
	}

	if db.GetLocationId() != "us-central1" {
		t.Errorf("locationId=%q", db.GetLocationId())
	}

	// Default concurrency + delete protection.
	if db.GetConcurrencyMode() != adminpb.Database_OPTIMISTIC {
		t.Errorf("concurrencyMode=%v", db.GetConcurrencyMode())
	}

	if db.GetDeleteProtectionState() != adminpb.Database_DELETE_PROTECTION_DISABLED {
		t.Errorf("deleteProtectionState=%v", db.GetDeleteProtectionState())
	}

	// Get.
	got, err := client.GetDatabase(ctx, &adminpb.GetDatabaseRequest{Name: "projects/p1/databases/db1"})
	if err != nil {
		t.Fatalf("GetDatabase: %v", err)
	}

	if got.GetUid() == "" || got.GetUid() != db.GetUid() {
		t.Errorf("uid mismatch: get=%q create=%q", got.GetUid(), db.GetUid())
	}

	// List.
	list, err := client.ListDatabases(ctx, &adminpb.ListDatabasesRequest{Parent: "projects/p1"})
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}

	if len(list.GetDatabases()) != 1 || list.GetDatabases()[0].GetName() != "projects/p1/databases/db1" {
		t.Fatalf("list=%v", list.GetDatabases())
	}

	// Patch: enable delete protection.
	patchDeleteProtection(t, client, adminpb.Database_DELETE_PROTECTION_ENABLED)

	// Delete is now blocked.
	if _, err := client.DeleteDatabase(ctx, &adminpb.DeleteDatabaseRequest{
		Name: "projects/p1/databases/db1",
	}); err == nil {
		t.Fatal("DeleteDatabase should be blocked by delete protection")
	}

	// Disable protection, then delete succeeds.
	patchDeleteProtection(t, client, adminpb.Database_DELETE_PROTECTION_DISABLED)

	delOp, err := client.DeleteDatabase(ctx, &adminpb.DeleteDatabaseRequest{Name: "projects/p1/databases/db1"})
	if err != nil {
		t.Fatalf("DeleteDatabase: %v", err)
	}

	if _, err := delOp.Wait(ctx); err != nil {
		t.Fatalf("DeleteDatabase Wait: %v", err)
	}

	if _, err := client.GetDatabase(ctx, &adminpb.GetDatabaseRequest{Name: "projects/p1/databases/db1"}); err == nil {
		t.Fatal("GetDatabase after delete should 404")
	}
}

// patchDeleteProtection updates db1's delete-protection state via UpdateDatabase.
func patchDeleteProtection(t *testing.T, client *admin.FirestoreAdminClient, state adminpb.Database_DeleteProtectionState) {
	t.Helper()

	ctx := context.Background()

	op, err := client.UpdateDatabase(ctx, &adminpb.UpdateDatabaseRequest{
		Database: &adminpb.Database{
			Name:                  "projects/p1/databases/db1",
			DeleteProtectionState: state,
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"delete_protection_state"}},
	})
	if err != nil {
		t.Fatalf("UpdateDatabase: %v", err)
	}

	updated, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("UpdateDatabase Wait: %v", err)
	}

	if updated.GetDeleteProtectionState() != state {
		t.Errorf("deleteProtectionState=%v want %v", updated.GetDeleteProtectionState(), state)
	}
}

// TestSDKFirestoreAdminAndDataPlaneCoexist proves the routing disambiguation:
// admin database ops and data-plane document ops both work on the SAME server.
func TestSDKFirestoreAdminAndDataPlaneCoexist(t *testing.T) {
	cloudP := cloudemu.NewGCP()

	ctx := context.Background()
	// Data-plane driver needs the table pre-declared (mirrors the doc-id key).
	if err := cloudP.Firestore.CreateTable(ctx, dbdriver.TableConfig{Name: "cities", PartitionKey: "\x00id"}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	srv := gcpserver.New(gcpserver.Drivers{Firestore: cloudP.Firestore})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// Admin: create a database.
	adminClient, err := admin.NewFirestoreAdminRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("admin client: %v", err)
	}

	t.Cleanup(func() { _ = adminClient.Close() })

	op, err := adminClient.CreateDatabase(ctx, &adminpb.CreateDatabaseRequest{
		Parent: "projects/p1", DatabaseId: "db1",
		Database: &adminpb.Database{Type: adminpb.Database_FIRESTORE_NATIVE, LocationId: "us-central1"},
	})
	if err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if _, err := op.Wait(ctx); err != nil {
		t.Fatalf("CreateDatabase Wait: %v", err)
	}

	// Data plane: create + read a document on the same server.
	docClient, err := gcpfirestore.NewRESTClient(ctx, "p1",
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("doc client: %v", err)
	}

	t.Cleanup(func() { _ = docClient.Close() })

	docRef := docClient.Collection("cities").Doc("SF")
	if _, err := docRef.Set(ctx, map[string]any{"name": "San Francisco"}); err != nil {
		t.Fatalf("doc Set: %v", err)
	}

	snap, err := docRef.Get(ctx)
	if err != nil {
		t.Fatalf("doc Get: %v", err)
	}

	if snap.Data()["name"] != "San Francisco" {
		t.Errorf("doc name=%v", snap.Data()["name"])
	}

	// And the admin database is still gettable (data-plane traffic didn't shadow it).
	if _, err := adminClient.GetDatabase(ctx, &adminpb.GetDatabaseRequest{Name: "projects/p1/databases/db1"}); err != nil {
		t.Fatalf("GetDatabase after doc ops: %v", err)
	}
}

// TestSDKFirestoreAdminIndexLifecycle exercises databases.indexes create (LRO) ->
// get/list/delete with the real admin client.
func TestSDKFirestoreAdminIndexLifecycle(t *testing.T) {
	_, client := newAdminTestServer(t)
	ctx := context.Background()

	// A database must exist for the index create.
	dbOp, err := client.CreateDatabase(ctx, &adminpb.CreateDatabaseRequest{
		Parent: "projects/p1", DatabaseId: "db1",
		Database: &adminpb.Database{Type: adminpb.Database_FIRESTORE_NATIVE, LocationId: "us-central1"},
	})
	if err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if _, err := dbOp.Wait(ctx); err != nil {
		t.Fatalf("CreateDatabase Wait: %v", err)
	}

	idxOp, err := client.CreateIndex(ctx, &adminpb.CreateIndexRequest{
		Parent: "projects/p1/databases/db1/collectionGroups/cities",
		Index: &adminpb.Index{
			QueryScope: adminpb.Index_COLLECTION,
			Fields: []*adminpb.Index_IndexField{
				{FieldPath: "name", ValueMode: &adminpb.Index_IndexField_Order_{Order: adminpb.Index_IndexField_ASCENDING}},
				{FieldPath: "pop", ValueMode: &adminpb.Index_IndexField_Order_{Order: adminpb.Index_IndexField_DESCENDING}},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}

	idx, err := idxOp.Wait(ctx)
	if err != nil {
		t.Fatalf("CreateIndex Wait: %v", err)
	}

	if idx.GetState() != adminpb.Index_READY {
		t.Errorf("index state=%v want READY", idx.GetState())
	}

	if len(idx.GetFields()) != 2 {
		t.Fatalf("index fields=%d want 2", len(idx.GetFields()))
	}

	// Get.
	got, err := client.GetIndex(ctx, &adminpb.GetIndexRequest{Name: idx.GetName()})
	if err != nil {
		t.Fatalf("GetIndex: %v", err)
	}

	if got.GetName() != idx.GetName() {
		t.Errorf("get name=%q want %q", got.GetName(), idx.GetName())
	}

	// List.
	it := client.ListIndexes(ctx, &adminpb.ListIndexesRequest{
		Parent: "projects/p1/databases/db1/collectionGroups/cities",
	})

	first, err := it.Next()
	if err != nil {
		t.Fatalf("ListIndexes Next: %v", err)
	}

	if first.GetName() != idx.GetName() {
		t.Errorf("list name=%q want %q", first.GetName(), idx.GetName())
	}

	// Delete.
	if err := client.DeleteIndex(ctx, &adminpb.DeleteIndexRequest{Name: idx.GetName()}); err != nil {
		t.Fatalf("DeleteIndex: %v", err)
	}

	if _, err := client.GetIndex(ctx, &adminpb.GetIndexRequest{Name: idx.GetName()}); err == nil {
		t.Fatal("GetIndex after delete should 404")
	}
}
