package databricks_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"
)

// createWorkspaceWithProps PUTs a workspace carrying the given properties and
// returns the created resource via the real armdatabricks SDK.
func createWorkspaceWithProps(
	t *testing.T, client *armdatabricks.WorkspacesClient, props *armdatabricks.WorkspaceProperties,
) armdatabricks.Workspace {
	t.Helper()

	props.ManagedResourceGroupID = to.Ptr(managed)

	poller, err := client.BeginCreateOrUpdate(context.Background(), testRG, testWS, armdatabricks.Workspace{
		Location:   to.Ptr("eastus"),
		SKU:        &armdatabricks.SKU{Name: to.Ptr("premium")},
		Tags:       map[string]*string{"env": to.Ptr("test")},
		Properties: props,
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	res, err := poller.PollUntilDone(context.Background(), nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	if res.Properties == nil || *res.Properties.ProvisioningState != armdatabricks.ProvisioningStateSucceeded {
		t.Fatalf("expected Succeeded provisioning state, got %+v", res.Properties)
	}

	return res.Workspace
}

// TestSDKWorkspaceVNetInjectionRoundTrip proves a VNet-injected workspace's
// custom network parameters survive the create->GET round-trip under
// properties.parameters with the {value:...} wrapper.
func TestSDKWorkspaceVNetInjectionRoundTrip(t *testing.T) {
	client := newWorkspacesClient(t)
	ctx := context.Background()

	const (
		vnetID  = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/my-vnet"
		privSub = "private-subnet"
		pubSub  = "public-subnet"
	)

	createWorkspaceWithProps(t, client, &armdatabricks.WorkspaceProperties{
		Parameters: &armdatabricks.WorkspaceCustomParameters{
			CustomVirtualNetworkID:  &armdatabricks.WorkspaceCustomStringParameter{Value: to.Ptr(vnetID)},
			CustomPrivateSubnetName: &armdatabricks.WorkspaceCustomStringParameter{Value: to.Ptr(privSub)},
			CustomPublicSubnetName:  &armdatabricks.WorkspaceCustomStringParameter{Value: to.Ptr(pubSub)},
			EnableNoPublicIP:        &armdatabricks.WorkspaceCustomBooleanParameter{Value: to.Ptr(true)},
		},
	})

	got, err := client.Get(ctx, testRG, testWS, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	p := got.Properties.Parameters
	if p == nil {
		t.Fatal("expected properties.parameters to be reflected on GET")
	}

	requireStringParam(t, "customVirtualNetworkId", p.CustomVirtualNetworkID, vnetID)
	requireStringParam(t, "customPrivateSubnetName", p.CustomPrivateSubnetName, privSub)
	requireStringParam(t, "customPublicSubnetName", p.CustomPublicSubnetName, pubSub)

	if p.EnableNoPublicIP == nil || p.EnableNoPublicIP.Value == nil || !*p.EnableNoPublicIP.Value {
		t.Fatalf("expected enableNoPublicIp value=true, got %+v", p.EnableNoPublicIP)
	}
}

// TestSDKWorkspaceCMKEncryptionRoundTrip proves CMK encryption parameters
// survive the create->GET round-trip.
func TestSDKWorkspaceCMKEncryptionRoundTrip(t *testing.T) {
	client := newWorkspacesClient(t)
	ctx := context.Background()

	const (
		keyName  = "my-cmk-key"
		vaultURI = "https://my-vault.vault.azure.net/"
		keyVer   = "abc123def456"
	)

	createWorkspaceWithProps(t, client, &armdatabricks.WorkspaceProperties{
		Parameters: &armdatabricks.WorkspaceCustomParameters{
			PrepareEncryption:               &armdatabricks.WorkspaceCustomBooleanParameter{Value: to.Ptr(true)},
			RequireInfrastructureEncryption: &armdatabricks.WorkspaceCustomBooleanParameter{Value: to.Ptr(true)},
			Encryption: &armdatabricks.WorkspaceEncryptionParameter{
				Value: &armdatabricks.Encryption{
					KeySource:   to.Ptr(armdatabricks.KeySourceMicrosoftKeyvault),
					KeyName:     to.Ptr(keyName),
					KeyVaultURI: to.Ptr(vaultURI),
					KeyVersion:  to.Ptr(keyVer),
				},
			},
		},
	})

	got, err := client.Get(ctx, testRG, testWS, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	p := got.Properties.Parameters
	if p == nil {
		t.Fatal("expected properties.parameters to be reflected on GET")
	}

	if p.PrepareEncryption == nil || p.PrepareEncryption.Value == nil || !*p.PrepareEncryption.Value {
		t.Fatalf("expected prepareEncryption value=true, got %+v", p.PrepareEncryption)
	}

	if p.RequireInfrastructureEncryption == nil || p.RequireInfrastructureEncryption.Value == nil ||
		!*p.RequireInfrastructureEncryption.Value {
		t.Fatalf("expected requireInfrastructureEncryption value=true, got %+v", p.RequireInfrastructureEncryption)
	}

	if p.Encryption == nil || p.Encryption.Value == nil {
		t.Fatal("expected encryption parameter to be reflected on GET")
	}

	enc := p.Encryption.Value
	if enc.KeySource == nil || *enc.KeySource != armdatabricks.KeySourceMicrosoftKeyvault {
		t.Fatalf("keySource: got %v, want Microsoft.Keyvault", enc.KeySource)
	}

	if enc.KeyName == nil || *enc.KeyName != keyName {
		t.Fatalf("keyName: got %v, want %q", enc.KeyName, keyName)
	}

	if enc.KeyVaultURI == nil || *enc.KeyVaultURI != vaultURI {
		t.Fatalf("keyVaultUri: got %v, want %q", enc.KeyVaultURI, vaultURI)
	}

	if enc.KeyVersion == nil || *enc.KeyVersion != keyVer {
		t.Fatalf("keyVersion: got %v, want %q", enc.KeyVersion, keyVer)
	}
}

// TestSDKWorkspaceNetworkAccessRoundTrip proves publicNetworkAccess and
// requiredNsgRules survive the create->GET round-trip.
func TestSDKWorkspaceNetworkAccessRoundTrip(t *testing.T) {
	client := newWorkspacesClient(t)
	ctx := context.Background()

	createWorkspaceWithProps(t, client, &armdatabricks.WorkspaceProperties{
		PublicNetworkAccess: to.Ptr(armdatabricks.PublicNetworkAccessDisabled),
		RequiredNsgRules:    to.Ptr(armdatabricks.RequiredNsgRulesNoAzureDatabricksRules),
	})

	got, err := client.Get(ctx, testRG, testWS, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties.PublicNetworkAccess == nil ||
		*got.Properties.PublicNetworkAccess != armdatabricks.PublicNetworkAccessDisabled {
		t.Fatalf("publicNetworkAccess: got %v, want Disabled", got.Properties.PublicNetworkAccess)
	}

	if got.Properties.RequiredNsgRules == nil ||
		*got.Properties.RequiredNsgRules != armdatabricks.RequiredNsgRulesNoAzureDatabricksRules {
		t.Fatalf("requiredNsgRules: got %v, want NoAzureDatabricksRules", got.Properties.RequiredNsgRules)
	}
}

// TestSDKWorkspaceParametersPreservedOnPatch proves a tags-only PATCH does not
// wipe the previously-set parameters/encryption/network properties.
func TestSDKWorkspaceParametersPreservedOnPatch(t *testing.T) {
	client := newWorkspacesClient(t)
	ctx := context.Background()

	const vnetID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/my-vnet"

	createWorkspaceWithProps(t, client, &armdatabricks.WorkspaceProperties{
		PublicNetworkAccess: to.Ptr(armdatabricks.PublicNetworkAccessDisabled),
		Parameters: &armdatabricks.WorkspaceCustomParameters{
			CustomVirtualNetworkID: &armdatabricks.WorkspaceCustomStringParameter{Value: to.Ptr(vnetID)},
			EnableNoPublicIP:       &armdatabricks.WorkspaceCustomBooleanParameter{Value: to.Ptr(true)},
			Encryption: &armdatabricks.WorkspaceEncryptionParameter{
				Value: &armdatabricks.Encryption{
					KeySource: to.Ptr(armdatabricks.KeySourceMicrosoftKeyvault),
					KeyName:   to.Ptr("cmk"),
				},
			},
		},
	})

	patchPoller, err := client.BeginUpdate(ctx, testRG, testWS, armdatabricks.WorkspaceUpdate{
		Tags: map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	if _, err = patchPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("update PollUntilDone: %v", err)
	}

	got, err := client.Get(ctx, testRG, testWS, nil)
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "prod" {
		t.Fatalf("PATCH did not apply env=prod: %v", got.Tags)
	}

	if got.Properties.PublicNetworkAccess == nil ||
		*got.Properties.PublicNetworkAccess != armdatabricks.PublicNetworkAccessDisabled {
		t.Fatalf("PATCH wiped publicNetworkAccess: got %v", got.Properties.PublicNetworkAccess)
	}

	p := got.Properties.Parameters
	if p == nil {
		t.Fatal("PATCH wiped properties.parameters entirely")
	}

	requireStringParam(t, "customVirtualNetworkId", p.CustomVirtualNetworkID, vnetID)

	if p.EnableNoPublicIP == nil || p.EnableNoPublicIP.Value == nil || !*p.EnableNoPublicIP.Value {
		t.Fatalf("PATCH wiped enableNoPublicIp: %+v", p.EnableNoPublicIP)
	}

	if p.Encryption == nil || p.Encryption.Value == nil || p.Encryption.Value.KeyName == nil ||
		*p.Encryption.Value.KeyName != "cmk" {
		t.Fatalf("PATCH wiped encryption parameter: %+v", p.Encryption)
	}
}

// TestSDKWorkspaceIdentitySynthesisIntact proves modeling the extended
// properties does not regress the synthesized managedResourceGroupId /
// workspaceUrl / workspaceId set on a plain workspace with no extra properties.
func TestSDKWorkspaceIdentitySynthesisIntact(t *testing.T) {
	client := newWorkspacesClient(t)

	ws := createWorkspaceWithProps(t, client, &armdatabricks.WorkspaceProperties{})

	if ws.Properties.ManagedResourceGroupID == nil || *ws.Properties.ManagedResourceGroupID != managed {
		t.Fatalf("managedResourceGroupId: got %v, want %q", ws.Properties.ManagedResourceGroupID, managed)
	}

	if ws.Properties.WorkspaceURL == nil || *ws.Properties.WorkspaceURL == "" {
		t.Fatal("expected a synthesized workspaceUrl")
	}

	if ws.Properties.WorkspaceID == nil || *ws.Properties.WorkspaceID == "" {
		t.Fatal("expected a synthesized workspaceId")
	}

	// A plain workspace must not carry any spurious parameters block.
	if ws.Properties.Parameters != nil {
		t.Fatalf("expected no parameters for a plain workspace, got %+v", ws.Properties.Parameters)
	}
}

func requireStringParam(t *testing.T, name string, got *armdatabricks.WorkspaceCustomStringParameter, want string) {
	t.Helper()

	if got == nil || got.Value == nil {
		t.Fatalf("%s: expected value %q, got nil", name, want)
	}

	if *got.Value != want {
		t.Fatalf("%s: got %q, want %q", name, *got.Value, want)
	}
}
