package acr_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func newACRARMFactory(t *testing.T) *armcontainerregistry.ClientFactory {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{ACR: cloudP.ACR})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armcontainerregistry.NewClientFactory("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewClientFactory: %v", err)
	}

	return cf
}

func createRegistry(t *testing.T, client *armcontainerregistry.RegistriesClient, rg, name string) armcontainerregistry.Registry {
	t.Helper()

	poller, err := client.BeginCreate(context.Background(), rg, name, armcontainerregistry.Registry{
		Location: to.Ptr("eastus"),
		SKU:      &armcontainerregistry.SKU{Name: to.Ptr(armcontainerregistry.SKUNameStandard)},
		Identity: &armcontainerregistry.IdentityProperties{
			Type: to.Ptr(armcontainerregistry.ResourceIdentityTypeSystemAssigned),
		},
		Properties: &armcontainerregistry.RegistryProperties{AdminUserEnabled: to.Ptr(true)},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	resp, err := poller.PollUntilDone(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create PollUntilDone: %v", err)
	}

	return resp.Registry
}

func TestSDKACRRegistryLifecycle(t *testing.T) {
	cf := newACRARMFactory(t)
	client := cf.NewRegistriesClient()
	ctx := context.Background()

	reg := createRegistry(t, client, "rg-1", "myreg")

	if reg.Properties == nil || reg.Properties.LoginServer == nil || *reg.Properties.LoginServer != "myreg.azurecr.io" {
		t.Fatalf("got loginServer %v, want myreg.azurecr.io", reg.Properties)
	}

	if reg.Properties.AdminUserEnabled == nil || !*reg.Properties.AdminUserEnabled {
		t.Fatal("expected adminUserEnabled true")
	}

	if reg.Identity == nil || reg.Identity.PrincipalID == nil || *reg.Identity.PrincipalID == "" {
		t.Fatal("expected system-assigned identity with principalId")
	}

	if reg.Identity.TenantID == nil || *reg.Identity.TenantID == "" {
		t.Fatal("expected identity tenantId")
	}

	got, err := client.Get(ctx, "rg-1", "myreg", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if *got.SKU.Tier != armcontainerregistry.SKUTierStandard {
		t.Fatalf("got tier %v, want Standard", *got.SKU.Tier)
	}

	// List within resource group.
	rgPager := client.NewListByResourceGroupPager("rg-1", nil)

	rgPage, err := rgPager.NextPage(ctx)
	if err != nil {
		t.Fatalf("ListByResourceGroup: %v", err)
	}

	if len(rgPage.Value) != 1 {
		t.Fatalf("got %d registries in rg, want 1", len(rgPage.Value))
	}

	// Subscription-wide list.
	subPager := client.NewListPager(nil)

	subPage, err := subPager.NextPage(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(subPage.Value) != 1 {
		t.Fatalf("got %d registries in subscription, want 1", len(subPage.Value))
	}

	// Delete.
	delPoller, err := client.BeginDelete(ctx, "rg-1", "myreg", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}

	if _, err := client.Get(ctx, "rg-1", "myreg", nil); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

// TestSDKACRRegistryUpdatePreservesAdminUser drives the real armcontainerregistry
// SDK: a BeginUpdate that sets only publicNetworkAccess must preserve the
// adminUserEnabled=true established at create time.
func TestSDKACRRegistryUpdatePreservesAdminUser(t *testing.T) {
	cf := newACRARMFactory(t)
	client := cf.NewRegistriesClient()
	ctx := context.Background()

	createRegistry(t, client, "rg-1", "netreg")

	poller, err := client.BeginUpdate(ctx, "rg-1", "netreg", armcontainerregistry.RegistryUpdateParameters{
		Properties: &armcontainerregistry.RegistryPropertiesUpdateParameters{
			PublicNetworkAccess: to.Ptr(armcontainerregistry.PublicNetworkAccessDisabled),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Update PollUntilDone: %v", err)
	}

	got, err := client.Get(ctx, "rg-1", "netreg", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.AdminUserEnabled == nil || !*got.Properties.AdminUserEnabled {
		t.Fatalf("PATCH reset adminUserEnabled; want true, got %v", got.Properties.AdminUserEnabled)
	}
}

// TestSDKACRDeleteMissingIsIdempotent drives the real SDK: deleting a
// never-created registry/webhook/replication completes without error (204).
func TestSDKACRDeleteMissingIsIdempotent(t *testing.T) {
	cf := newACRARMFactory(t)
	ctx := context.Background()

	regClient := cf.NewRegistriesClient()

	delPoller, err := regClient.BeginDelete(ctx, "rg-1", "ghost", nil)
	if err != nil {
		t.Fatalf("registry BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("registry delete of missing resource: %v", err)
	}

	createRegistry(t, regClient, "rg-1", "idemreg")

	whPoller, err := cf.NewWebhooksClient().BeginDelete(ctx, "rg-1", "idemreg", "ghost", nil)
	if err != nil {
		t.Fatalf("webhook BeginDelete: %v", err)
	}

	if _, err := whPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("webhook delete of missing resource: %v", err)
	}

	repPoller, err := cf.NewReplicationsClient().BeginDelete(ctx, "rg-1", "idemreg", "ghost", nil)
	if err != nil {
		t.Fatalf("replication BeginDelete: %v", err)
	}

	if _, err := repPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("replication delete of missing resource: %v", err)
	}
}

func TestSDKACRRegistryCredentials(t *testing.T) {
	cf := newACRARMFactory(t)
	client := cf.NewRegistriesClient()
	ctx := context.Background()

	createRegistry(t, client, "rg-1", "credreg")

	creds, err := client.ListCredentials(ctx, "rg-1", "credreg", nil)
	if err != nil {
		t.Fatalf("ListCredentials: %v", err)
	}

	if creds.Username == nil || *creds.Username != "credreg" {
		t.Fatalf("got username %v, want credreg", creds.Username)
	}

	if len(creds.Passwords) != 2 {
		t.Fatalf("got %d passwords, want 2", len(creds.Passwords))
	}

	firstPassword := *creds.Passwords[0].Value

	regen, err := client.RegenerateCredential(ctx, "rg-1", "credreg", armcontainerregistry.RegenerateCredentialParameters{
		Name: to.Ptr(armcontainerregistry.PasswordNamePassword),
	}, nil)
	if err != nil {
		t.Fatalf("RegenerateCredential: %v", err)
	}

	if *regen.Passwords[0].Value == firstPassword {
		t.Fatal("expected password to change after regenerate")
	}

	// Usages.
	usages, err := client.ListUsages(ctx, "rg-1", "credreg", nil)
	if err != nil {
		t.Fatalf("ListUsages: %v", err)
	}

	if len(usages.Value) == 0 {
		t.Fatal("expected non-empty usages")
	}
}

func TestSDKACRWebhookAndReplication(t *testing.T) {
	cf := newACRARMFactory(t)
	regClient := cf.NewRegistriesClient()
	ctx := context.Background()

	createRegistry(t, regClient, "rg-1", "hookreg")

	whClient := cf.NewWebhooksClient()

	whPoller, err := whClient.BeginCreate(ctx, "rg-1", "hookreg", "wh1", armcontainerregistry.WebhookCreateParameters{
		Location: to.Ptr("eastus"),
		Properties: &armcontainerregistry.WebhookPropertiesCreateParameters{
			ServiceURI: to.Ptr("https://example.com/hook"),
			Actions:    []*armcontainerregistry.WebhookAction{to.Ptr(armcontainerregistry.WebhookActionPush)},
			Status:     to.Ptr(armcontainerregistry.WebhookStatusEnabled),
		},
	}, nil)
	if err != nil {
		t.Fatalf("Webhook BeginCreate: %v", err)
	}

	whResp, err := whPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("Webhook PollUntilDone: %v", err)
	}

	if whResp.Name == nil || *whResp.Name != "wh1" {
		t.Fatalf("got webhook name %v, want wh1", whResp.Name)
	}

	if _, err := whClient.Get(ctx, "rg-1", "hookreg", "wh1", nil); err != nil {
		t.Fatalf("Webhook Get: %v", err)
	}

	whPage, err := whClient.NewListPager("rg-1", "hookreg", nil).NextPage(ctx)
	if err != nil {
		t.Fatalf("Webhook List: %v", err)
	}

	if len(whPage.Value) != 1 {
		t.Fatalf("got %d webhooks, want 1", len(whPage.Value))
	}

	whDel, err := whClient.BeginDelete(ctx, "rg-1", "hookreg", "wh1", nil)
	if err != nil {
		t.Fatalf("Webhook BeginDelete: %v", err)
	}

	if _, err := whDel.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Webhook delete poll: %v", err)
	}

	// Replication.
	repClient := cf.NewReplicationsClient()

	repPoller, err := repClient.BeginCreate(ctx, "rg-1", "hookreg", "westus", armcontainerregistry.Replication{
		Location: to.Ptr("westus"),
	}, nil)
	if err != nil {
		t.Fatalf("Replication BeginCreate: %v", err)
	}

	repResp, err := repPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("Replication PollUntilDone: %v", err)
	}

	if repResp.Name == nil || *repResp.Name != "westus" {
		t.Fatalf("got replication name %v, want westus", repResp.Name)
	}

	repPage, err := repClient.NewListPager("rg-1", "hookreg", nil).NextPage(ctx)
	if err != nil {
		t.Fatalf("Replication List: %v", err)
	}

	if len(repPage.Value) != 1 {
		t.Fatalf("got %d replications, want 1", len(repPage.Value))
	}
}
