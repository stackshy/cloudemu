package postgresflex_test

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newSDKClient(t *testing.T) *armpostgresqlflexibleservers.ServersClient {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{PostgresFlex: cloudP.PostgresFlex})

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

	c, err := armpostgresqlflexibleservers.NewServersClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func TestSDKPostgresFlexServerLifecycle(t *testing.T) {
	servers := newSDKClient(t)
	ctx := context.Background()

	poller, err := servers.BeginCreate(ctx, "rg-1", "srv1", armpostgresqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
		SKU: &armpostgresqlflexibleservers.SKU{
			Name: to.Ptr("Standard_B1ms"),
			Tier: to.Ptr(armpostgresqlflexibleservers.SKUTierBurstable),
		},
		Properties: &armpostgresqlflexibleservers.ServerProperties{
			AdministratorLogin:         to.Ptr("pgadmin"),
			AdministratorLoginPassword: to.Ptr("Sup3rs3cret!"),
			Version:                    to.Ptr(armpostgresqlflexibleservers.ServerVersionFourteen),
			Storage: &armpostgresqlflexibleservers.Storage{
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

	if got.Server.Properties.State == nil || *got.Server.Properties.State != armpostgresqlflexibleservers.ServerStateReady {
		t.Fatalf("expected state Ready, got %v", got.Server.Properties.State)
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

func TestSDKPostgresFlexUpdateAndLifecycle(t *testing.T) {
	servers := newSDKClient(t)
	ctx := context.Background()

	createPoller, err := servers.BeginCreate(ctx, "rg-1", "srv2", armpostgresqlflexibleservers.Server{
		Location: to.Ptr("eastus"),
		SKU:      &armpostgresqlflexibleservers.SKU{Name: to.Ptr("Standard_B1ms")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err := createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	// PATCH (resize storage + bump SKU).
	patchPoller, err := servers.BeginUpdate(ctx, "rg-1", "srv2", armpostgresqlflexibleservers.ServerForUpdate{
		SKU: &armpostgresqlflexibleservers.SKU{Name: to.Ptr("Standard_D2s_v3")},
		Properties: &armpostgresqlflexibleservers.ServerPropertiesForUpdate{
			Storage: &armpostgresqlflexibleservers.Storage{StorageSizeGB: to.Ptr(int32(256))},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	if _, err := patchPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Update PollUntilDone: %v", err)
	}

	got, err := servers.Get(ctx, "rg-1", "srv2", nil)
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if got.Server.SKU == nil || *got.Server.SKU.Name != "Standard_D2s_v3" {
		t.Fatalf("expected SKU Standard_D2s_v3 after patch, got %+v", got.Server.SKU)
	}

	if got.Server.Properties == nil ||
		got.Server.Properties.Storage == nil ||
		got.Server.Properties.Storage.StorageSizeGB == nil ||
		*got.Server.Properties.Storage.StorageSizeGB != 256 {
		t.Fatalf("expected storage 256 after patch")
	}

	// Stop.
	stopPoller, err := servers.BeginStop(ctx, "rg-1", "srv2", nil)
	if err != nil {
		t.Fatalf("BeginStop: %v", err)
	}

	if _, err := stopPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Stop PollUntilDone: %v", err)
	}

	got, err = servers.Get(ctx, "rg-1", "srv2", nil)
	if err != nil {
		t.Fatalf("Get after stop: %v", err)
	}

	if got.Server.Properties == nil || got.Server.Properties.State == nil ||
		*got.Server.Properties.State != armpostgresqlflexibleservers.ServerStateStopped {
		t.Fatalf("expected state Stopped, got %v", got.Server.Properties.State)
	}

	// Start.
	startPoller, err := servers.BeginStart(ctx, "rg-1", "srv2", nil)
	if err != nil {
		t.Fatalf("BeginStart: %v", err)
	}

	if _, err := startPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Start PollUntilDone: %v", err)
	}

	// Restart.
	restartPoller, err := servers.BeginRestart(ctx, "rg-1", "srv2", nil)
	if err != nil {
		t.Fatalf("BeginRestart: %v", err)
	}

	if _, err := restartPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Restart PollUntilDone: %v", err)
	}

	got, err = servers.Get(ctx, "rg-1", "srv2", nil)
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}

	if got.Server.Properties == nil || got.Server.Properties.State == nil ||
		*got.Server.Properties.State != armpostgresqlflexibleservers.ServerStateReady {
		t.Fatalf("expected state Ready after restart, got %v", got.Server.Properties.State)
	}
}
