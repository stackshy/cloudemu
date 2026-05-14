package mysqlflex_test

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysqlflexibleservers"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newSDKClient(t *testing.T) *armmysqlflexibleservers.ServersClient {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{MySQLFlex: cloudP.MySQLFlex})

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

	cf, err := armmysqlflexibleservers.NewClientFactory("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	return cf.NewServersClient()
}

func TestSDKMySQLFlexLifecycle(t *testing.T) {
	servers := newSDKClient(t)
	ctx := context.Background()

	poller, err := servers.BeginCreate(ctx, "rg-1", "srv1", armmysqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
		SKU: &armmysqlflexibleservers.SKU{
			Name: to.Ptr("Standard_D2ds_v4"),
			Tier: to.Ptr(armmysqlflexibleservers.SKUTierGeneralPurpose),
		},
		Properties: &armmysqlflexibleservers.ServerProperties{
			AdministratorLogin:         to.Ptr("admin"),
			AdministratorLoginPassword: to.Ptr("Sup3rs3cret!"),
			Version:                    to.Ptr(armmysqlflexibleservers.ServerVersionEight021),
			Storage: &armmysqlflexibleservers.Storage{
				StorageSizeGB: to.Ptr(int32(64)),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
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

	if got.Server.Properties.State == nil || *got.Server.Properties.State != armmysqlflexibleservers.ServerStateReady {
		t.Fatalf("expected state Ready, got %v", got.Server.Properties.State)
	}

	if got.Server.SKU == nil || got.Server.SKU.Name == nil || *got.Server.SKU.Name != "Standard_D2ds_v4" {
		t.Fatalf("expected SKU Standard_D2ds_v4, got %v", got.Server.SKU)
	}

	// PATCH (resize SKU + storage).
	patchPoller, err := servers.BeginUpdate(ctx, "rg-1", "srv1", armmysqlflexibleservers.ServerForUpdate{
		SKU: &armmysqlflexibleservers.SKU{Name: to.Ptr("Standard_D4ds_v4")},
		Properties: &armmysqlflexibleservers.ServerPropertiesForUpdate{
			Storage: &armmysqlflexibleservers.Storage{
				StorageSizeGB: to.Ptr(int32(128)),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	if _, err := patchPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Update PollUntilDone: %v", err)
	}

	got, err = servers.Get(ctx, "rg-1", "srv1", nil)
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if got.Server.SKU == nil || *got.Server.SKU.Name != "Standard_D4ds_v4" {
		t.Fatalf("expected sku Standard_D4ds_v4 after patch")
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

func TestSDKMySQLFlexStartStopRestart(t *testing.T) {
	servers := newSDKClient(t)
	ctx := context.Background()

	createPoller, err := servers.BeginCreate(ctx, "rg-1", "srv1", armmysqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Create PollUntilDone: %v", err)
	}

	// Stop.
	stopPoller, err := servers.BeginStop(ctx, "rg-1", "srv1", nil)
	if err != nil {
		t.Fatalf("BeginStop: %v", err)
	}

	if _, err := stopPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Stop PollUntilDone: %v", err)
	}

	got, err := servers.Get(ctx, "rg-1", "srv1", nil)
	if err != nil {
		t.Fatalf("Get after stop: %v", err)
	}

	if got.Server.Properties == nil || got.Server.Properties.State == nil ||
		*got.Server.Properties.State != armmysqlflexibleservers.ServerStateStopped {
		t.Fatalf("expected state Stopped, got %v", got.Server.Properties.State)
	}

	// Start.
	startPoller, err := servers.BeginStart(ctx, "rg-1", "srv1", nil)
	if err != nil {
		t.Fatalf("BeginStart: %v", err)
	}

	if _, err := startPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Start PollUntilDone: %v", err)
	}

	// Restart.
	restartPoller, err := servers.BeginRestart(ctx, "rg-1", "srv1",
		armmysqlflexibleservers.ServerRestartParameter{}, nil)
	if err != nil {
		t.Fatalf("BeginRestart: %v", err)
	}

	if _, err := restartPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Restart PollUntilDone: %v", err)
	}

	got, err = servers.Get(ctx, "rg-1", "srv1", nil)
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}

	if got.Server.Properties == nil || got.Server.Properties.State == nil ||
		*got.Server.Properties.State != armmysqlflexibleservers.ServerStateReady {
		t.Fatalf("expected state Ready after restart, got %v", got.Server.Properties.State)
	}
}
