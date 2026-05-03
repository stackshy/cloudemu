package azuresql_test

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

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newSDKClients(t *testing.T) (*armsql.ServersClient, *armsql.DatabasesClient) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{SQL: cloudP.SQL})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: ts.URL,
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armsql.NewClientFactory("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	return cf.NewServersClient(), cf.NewDatabasesClient()
}

func TestSDKAzureSQLServerLifecycle(t *testing.T) {
	servers, _ := newSDKClients(t)
	ctx := context.Background()

	poller, err := servers.BeginCreateOrUpdate(ctx, "rg-1", "srv1", armsql.Server{
		Location: to.Ptr("eastus"),
		Properties: &armsql.ServerProperties{
			AdministratorLogin:         to.Ptr("admin"),
			AdministratorLoginPassword: to.Ptr("Sup3rs3cret!"),
			Version:                    to.Ptr("12.0"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	if resp.Server.Name == nil || *resp.Server.Name != "srv1" {
		t.Fatalf("got name %v, want srv1", resp.Server.Name)
	}

	got, err := servers.Get(ctx, "rg-1", "srv1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Server.Properties == nil ||
		got.Server.Properties.FullyQualifiedDomainName == nil ||
		*got.Server.Properties.FullyQualifiedDomainName == "" {
		t.Fatal("expected FullyQualifiedDomainName set")
	}

	// List under the resource group.
	pager := servers.NewListByResourceGroupPager("rg-1", nil)

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d servers, want 1", len(page.Value))
	}

	delPoller, err := servers.BeginDelete(ctx, "rg-1", "srv1", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}

	if _, err := servers.Get(ctx, "rg-1", "srv1", nil); err == nil {
		t.Fatal("expected NotFound after Delete")
	}
}

func TestSDKAzureSQLDatabaseLifecycle(t *testing.T) {
	servers, dbs := newSDKClients(t)
	ctx := context.Background()

	// Need a server first.
	srvPoller, err := servers.BeginCreateOrUpdate(ctx, "rg-1", "srv1", armsql.Server{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("Server BeginCreateOrUpdate: %v", err)
	}

	if _, err := srvPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Server PollUntilDone: %v", err)
	}

	// Create database.
	dbPoller, err := dbs.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "appdb", armsql.Database{
		Location: to.Ptr("eastus"),
		SKU:      &armsql.SKU{Name: to.Ptr("S0")},
		Properties: &armsql.DatabaseProperties{
			MaxSizeBytes: to.Ptr(int64(50) * (1 << 30)),
		},
	}, nil)
	if err != nil {
		t.Fatalf("DB BeginCreateOrUpdate: %v", err)
	}

	dbResp, err := dbPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("DB PollUntilDone: %v", err)
	}

	if dbResp.Database.Name == nil || *dbResp.Database.Name != "appdb" {
		t.Fatalf("got db name %v, want appdb", dbResp.Database.Name)
	}

	if dbResp.Database.SKU == nil || *dbResp.Database.SKU.Name != "S0" {
		t.Fatal("expected SKU=S0 on response")
	}

	// Get.
	got, err := dbs.Get(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Database.Properties == nil || got.Database.Properties.Status == nil {
		t.Fatal("expected Status set")
	}

	if *got.Database.Properties.Status != "Online" {
		t.Fatalf("expected Online, got %q", *got.Database.Properties.Status)
	}

	// PATCH (resize).
	patchPoller, err := dbs.BeginUpdate(ctx, "rg-1", "srv1", "appdb", armsql.DatabaseUpdate{
		SKU: &armsql.SKU{Name: to.Ptr("S2")},
		Properties: &armsql.DatabaseUpdateProperties{
			MaxSizeBytes: to.Ptr(int64(100) * (1 << 30)),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	if _, err := patchPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Update PollUntilDone: %v", err)
	}

	got, err = dbs.Get(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if got.Database.SKU == nil || *got.Database.SKU.Name != "S2" {
		t.Fatalf("expected sku S2 after patch")
	}

	// List.
	pager := dbs.NewListByServerPager("rg-1", "srv1", nil)

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("DB List: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d databases, want 1", len(page.Value))
	}

	// Delete.
	delPoller, err := dbs.BeginDelete(ctx, "rg-1", "srv1", "appdb", nil)
	if err != nil {
		t.Fatalf("DB BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("DB delete poll: %v", err)
	}

	if _, err := dbs.Get(ctx, "rg-1", "srv1", "appdb", nil); err == nil {
		t.Fatal("expected NotFound after DB delete")
	}
}
