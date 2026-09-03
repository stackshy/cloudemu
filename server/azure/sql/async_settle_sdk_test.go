package sql_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	sqlprovider "github.com/stackshy/cloudemu/v2/providers/azure/sql"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// newAsyncFactory stands up the Azure SQL wire handler over a provider with
// AsyncSettle enabled and a FakeClock, so a real armsql SDK client observes the
// transient database status (Creating / Scaling) before the settle window
// elapses. The FakeClock is returned so the test can advance it past the window.
func newAsyncFactory(t *testing.T) (*armsql.ClientFactory, *config.FakeClock) {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("eastus"),
		config.WithAsyncSettle(),
	)

	srv := azureserver.New(azureserver.Drivers{SQL: sqlprovider.New(opts)})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	armOpts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armsql.NewClientFactory("sub-1", fakeCred{}, armOpts)
	if err != nil {
		t.Fatal(err)
	}

	return cf, fc
}

// TestSDKAzureSQLDatabaseAsyncSettle drives the real armsql SDK against the wire
// handler and asserts a database's status surfaces the real Azure SQL
// transitions: Creating → Online on create, Scaling → Online on update.
func TestSDKAzureSQLDatabaseAsyncSettle(t *testing.T) {
	cf, fc := newAsyncFactory(t)
	servers := cf.NewServersClient()
	dbs := cf.NewDatabasesClient()
	ctx := context.Background()

	srvPoller, err := servers.BeginCreateOrUpdate(ctx, "rg-1", "srv1", armsql.Server{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("Server BeginCreateOrUpdate: %v", err)
	}

	if _, err := srvPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Server PollUntilDone: %v", err)
	}

	// Create the database: the create response already reports Creating.
	dbPoller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "appdb", armsql.Database{
		Location: to.Ptr("eastus"),
		SKU:      &armsql.SKU{Name: to.Ptr("S0")},
	}, nil)
	if err != nil {
		t.Fatalf("DB BeginCreateOrUpdate: %v", err)
	}

	dbResp, err := dbPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("DB PollUntilDone: %v", err)
	}

	if got := dbStatusOf(t, dbResp.Database); got != armsql.DatabaseStatusCreating {
		t.Fatalf("create response status = %q, want Creating", got)
	}

	// A Get before the window elapses still reports Creating.
	got, err := dbs.Get(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if s := dbStatusOf(t, got.Database); s != armsql.DatabaseStatusCreating {
		t.Fatalf("get before settle status = %q, want Creating", s)
	}

	// Advance past the settle window: the database is now Online.
	fc.Advance(settle.DefaultAzureDBSettle)

	got, err = dbs.Get(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("Get after settle: %v", err)
	}

	if s := dbStatusOf(t, got.Database); s != armsql.DatabaseStatusOnline {
		t.Fatalf("get after settle status = %q, want Online", s)
	}

	// Update (SKU change): the database briefly reports Scaling, then Online.
	patchPoller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "appdb", armsql.Database{
		SKU: &armsql.SKU{Name: to.Ptr("S2")},
	}, nil)
	if err != nil {
		t.Fatalf("Update BeginCreateOrUpdate: %v", err)
	}

	if _, err := patchPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Update PollUntilDone: %v", err)
	}

	got, err = dbs.Get(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("Get during update: %v", err)
	}

	if s := dbStatusOf(t, got.Database); s != armsql.DatabaseStatusScaling {
		t.Fatalf("get during update status = %q, want Scaling", s)
	}

	fc.Advance(settle.DefaultAzureDBSettle)

	got, err = dbs.Get(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("Get after update settle: %v", err)
	}

	if s := dbStatusOf(t, got.Database); s != armsql.DatabaseStatusOnline {
		t.Fatalf("get after update settle status = %q, want Online", s)
	}
}

// TestSDKAzureSQLDatabaseDefaultOff confirms the default (AsyncSettle unset)
// path reports the terminal Online status synchronously via the real SDK.
func TestSDKAzureSQLDatabaseDefaultOff(t *testing.T) {
	servers, dbs := newSDKClients(t) // default provider, no AsyncSettle
	ctx := context.Background()

	srvPoller, err := servers.BeginCreateOrUpdate(ctx, "rg-1", "srv1", armsql.Server{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("Server BeginCreateOrUpdate: %v", err)
	}

	if _, err := srvPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Server PollUntilDone: %v", err)
	}

	dbPoller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "appdb", armsql.Database{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("DB BeginCreateOrUpdate: %v", err)
	}

	dbResp, err := dbPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("DB PollUntilDone: %v", err)
	}

	if got := dbStatusOf(t, dbResp.Database); got != armsql.DatabaseStatusOnline {
		t.Fatalf("create response status = %q, want Online (settle off)", got)
	}
}

// dbStatusOf extracts the database status, failing the test when it is absent.
func dbStatusOf(t *testing.T, db armsql.Database) armsql.DatabaseStatus {
	t.Helper()

	if db.Properties == nil || db.Properties.Status == nil {
		t.Fatalf("database status missing")
	}

	return *db.Properties.Status
}
