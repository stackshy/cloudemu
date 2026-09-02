package sql_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

// seedTDEDatabase creates a server and database so the TDE sub-resource has a
// parent, returning the factory's clients.
func seedTDEDatabase(t *testing.T) *armsql.ClientFactory {
	t.Helper()

	cf := newFactory(t)
	ctx := context.Background()

	srvPoller, err := cf.NewServersClient().BeginCreateOrUpdate(ctx, "rg-1", "srv1", armsql.Server{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("Server BeginCreateOrUpdate: %v", err)
	}

	if _, err := srvPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Server PollUntilDone: %v", err)
	}

	dbPoller, err := cf.NewDatabasesClient().BeginCreateOrUpdate(ctx, "rg-1", "srv1", "appdb", armsql.Database{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("DB BeginCreateOrUpdate: %v", err)
	}

	if _, err := dbPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("DB PollUntilDone: %v", err)
	}

	return cf
}

func TestSDKAzureSQLTransparentDataEncryption(t *testing.T) {
	cf := seedTDEDatabase(t)
	tde := cf.NewTransparentDataEncryptionsClient()
	ctx := context.Background()

	// A freshly created database reports TDE Enabled without any explicit PUT.
	got, err := tde.Get(ctx, "rg-1", "srv1", "appdb", armsql.TransparentDataEncryptionNameCurrent, nil)
	if err != nil {
		t.Fatalf("Get (auto-materialized): %v", err)
	}

	if got.Properties == nil || got.Properties.State == nil {
		t.Fatal("expected TDE state set")
	}

	if *got.Properties.State != armsql.TransparentDataEncryptionStateEnabled {
		t.Fatalf("got state %q, want Enabled", *got.Properties.State)
	}

	if got.Name == nil || *got.Name != "current" {
		t.Fatalf("got name %v, want current", got.Name)
	}

	// CreateOrUpdate is synchronous (no LRO): a single response, not a poller.
	up, err := tde.CreateOrUpdate(ctx, "rg-1", "srv1", "appdb",
		armsql.TransparentDataEncryptionNameCurrent,
		armsql.LogicalDatabaseTransparentDataEncryption{
			Properties: &armsql.TransparentDataEncryptionProperties{
				State: to.Ptr(armsql.TransparentDataEncryptionStateEnabled),
			},
		}, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}

	if up.Properties == nil || *up.Properties.State != armsql.TransparentDataEncryptionStateEnabled {
		t.Fatal("expected Enabled on CreateOrUpdate response")
	}

	// The set state round-trips through a Get.
	got, err = tde.Get(ctx, "rg-1", "srv1", "appdb", armsql.TransparentDataEncryptionNameCurrent, nil)
	if err != nil {
		t.Fatalf("Get after set: %v", err)
	}

	if *got.Properties.State != armsql.TransparentDataEncryptionStateEnabled {
		t.Fatalf("got state %q after set, want Enabled", *got.Properties.State)
	}

	// ListByDatabase returns the single "current" record.
	pager := tde.NewListByDatabasePager("rg-1", "srv1", "appdb", nil)

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("ListByDatabase: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d TDE records, want 1", len(page.Value))
	}

	if page.Value[0].Name == nil || *page.Value[0].Name != "current" {
		t.Fatalf("got list name %v, want current", page.Value[0].Name)
	}
}

// TestSDKAzureSQLTDESurvivesDatabaseUpdate is the S1 regression: a database
// whose TDE was set to Disabled must keep that state across an unrelated
// property update. A delete+recreate upsert dropped the TDE record and
// re-materialized it as Enabled; the in-place update leaves it untouched.
func TestSDKAzureSQLTDESurvivesDatabaseUpdate(t *testing.T) {
	cf := seedTDEDatabase(t)
	ctx := context.Background()
	tde := cf.NewTransparentDataEncryptionsClient()
	dbs := cf.NewDatabasesClient()

	// Disable TDE via the transparentDataEncryption/current sub-resource.
	if _, err := tde.CreateOrUpdate(ctx, "rg-1", "srv1", "appdb",
		armsql.TransparentDataEncryptionNameCurrent,
		armsql.LogicalDatabaseTransparentDataEncryption{
			Properties: &armsql.TransparentDataEncryptionProperties{
				State: to.Ptr(armsql.TransparentDataEncryptionStateDisabled),
			},
		}, nil); err != nil {
		t.Fatalf("disable TDE: %v", err)
	}

	// PATCH an unrelated property (tags) on the database.
	patchPoller, err := dbs.BeginUpdate(ctx, "rg-1", "srv1", "appdb", armsql.DatabaseUpdate{
		Tags: map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	if _, err := patchPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("BeginUpdate PollUntilDone: %v", err)
	}

	// TDE must still be Disabled — not silently reset to Enabled.
	got, err := tde.Get(ctx, "rg-1", "srv1", "appdb", armsql.TransparentDataEncryptionNameCurrent, nil)
	if err != nil {
		t.Fatalf("Get after PATCH: %v", err)
	}

	if got.Properties == nil || got.Properties.State == nil {
		t.Fatal("expected TDE state set after PATCH")
	}

	if *got.Properties.State != armsql.TransparentDataEncryptionStateDisabled {
		t.Fatalf("got state %q after unrelated PATCH, want Disabled", *got.Properties.State)
	}

	// A PUT (CreateOrUpdate) against the existing database must also preserve it.
	putPoller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "appdb", armsql.Database{
		Location: to.Ptr("eastus"),
		SKU:      &armsql.SKU{Name: to.Ptr("GP_Gen5_4"), Tier: to.Ptr("GeneralPurpose")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate (PUT): %v", err)
	}

	if _, err := putPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PUT PollUntilDone: %v", err)
	}

	got, err = tde.Get(ctx, "rg-1", "srv1", "appdb", armsql.TransparentDataEncryptionNameCurrent, nil)
	if err != nil {
		t.Fatalf("Get after PUT: %v", err)
	}

	if *got.Properties.State != armsql.TransparentDataEncryptionStateDisabled {
		t.Fatalf("got state %q after PUT, want Disabled", *got.Properties.State)
	}
}
