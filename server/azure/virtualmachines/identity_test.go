package virtualmachines_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKVMIdentitySystemAssigned verifies a real armcompute client sees a
// system-assigned identity attached at create time: a PUT VM with
// identity.type=SystemAssigned is accepted (already true before this fix),
// and — the bug — a subsequent GET must actually echo it back, with a
// synthesized principalId/tenantId, rather than losing the identity block
// entirely.
func TestSDKVMIdentitySystemAssigned(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSDKClient(t, ts)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "identity-vm", armcompute.VirtualMachine{
		Location: to.Ptr("eastus"),
		Identity: &armcompute.VirtualMachineIdentity{
			Type: to.Ptr(armcompute.ResourceIdentityTypeSystemAssigned),
		},
		Properties: minimalVMProperties(),
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := pollUntilDone(ctx, poller)
	if err != nil {
		t.Fatalf("CreateOrUpdate poll: %v", err)
	}

	assertSystemAssignedIdentity(t, "create response", created.Identity)

	got, err := client.Get(ctx, "rg-1", "identity-vm", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertSystemAssignedIdentity(t, "GET", got.Identity)

	// The identity must also be stable across a second create (idempotent PUT).
	poller2, err := client.BeginCreateOrUpdate(ctx, "rg-1", "identity-vm", armcompute.VirtualMachine{
		Location: to.Ptr("eastus"),
		Identity: &armcompute.VirtualMachineIdentity{
			Type: to.Ptr(armcompute.ResourceIdentityTypeSystemAssigned),
		},
		Properties: minimalVMProperties(),
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate (idempotent PUT): %v", err)
	}

	updated, err := pollUntilDone(ctx, poller2)
	if err != nil {
		t.Fatalf("CreateOrUpdate (idempotent PUT) poll: %v", err)
	}

	if updated.Identity == nil || got.Identity == nil ||
		*updated.Identity.PrincipalID != *got.Identity.PrincipalID {
		t.Errorf("principalId changed across idempotent PUT: got %v, want stable %v",
			updated.Identity, got.Identity)
	}

	// Listing at the resource group must also echo the identity.
	pager := client.NewListPager("rg-1", nil)

	var listed *armcompute.VirtualMachine

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		for _, vm := range page.Value {
			if vm.Name != nil && *vm.Name == "identity-vm" {
				listed = vm
			}
		}
	}

	if listed == nil {
		t.Fatal("List did not return identity-vm")
	}

	assertSystemAssignedIdentity(t, "List", listed.Identity)
}

// TestSDKVMIdentityUserAssigned verifies a VM created with a user-assigned
// identity gets the userAssignedIdentities map echoed back on GET, with a
// synthesized principalId/clientId per entry — the identity type mixes with
// SystemAssigned too.
func TestSDKVMIdentityUserAssigned(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSDKClient(t, ts)
	ctx := context.Background()

	const uaiID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.ManagedIdentity/" +
		"userAssignedIdentities/my-identity"

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "uai-vm", armcompute.VirtualMachine{
		Location: to.Ptr("eastus"),
		Identity: &armcompute.VirtualMachineIdentity{
			Type: to.Ptr(armcompute.ResourceIdentityTypeSystemAssignedUserAssigned),
			UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
				uaiID: {},
			},
		},
		Properties: minimalVMProperties(),
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := pollUntilDone(ctx, poller); err != nil {
		t.Fatalf("CreateOrUpdate poll: %v", err)
	}

	got, err := client.Get(ctx, "rg-1", "uai-vm", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Identity == nil {
		t.Fatal("Get: identity is nil, want SystemAssigned,UserAssigned block")
	}

	if got.Identity.PrincipalID == nil || *got.Identity.PrincipalID == "" {
		t.Error("Get: system-assigned principalId not synthesized")
	}

	if got.Identity.TenantID == nil || *got.Identity.TenantID == "" {
		t.Error("Get: system-assigned tenantId not synthesized")
	}

	uai, ok := got.Identity.UserAssignedIdentities[uaiID]
	if !ok || uai == nil {
		t.Fatalf("Get: userAssignedIdentities missing entry %q: %v", uaiID, got.Identity.UserAssignedIdentities)
	}

	if uai.PrincipalID == nil || *uai.PrincipalID == "" {
		t.Error("Get: user-assigned identity principalId not synthesized")
	}

	if uai.ClientID == nil || *uai.ClientID == "" {
		t.Error("Get: user-assigned identity clientId not synthesized")
	}
}

// TestSDKVMIdentityOmittedOnCreate verifies a VM created with no identity
// block at all reports no identity on GET (the pre-fix baseline behavior),
// rather than a spuriously populated one.
func TestSDKVMIdentityOmittedOnCreate(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloudP.VirtualMachines})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newSDKClient(t, ts)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "no-identity-vm", armcompute.VirtualMachine{
		Location:   to.Ptr("eastus"),
		Properties: minimalVMProperties(),
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := pollUntilDone(ctx, poller); err != nil {
		t.Fatalf("CreateOrUpdate poll: %v", err)
	}

	got, err := client.Get(ctx, "rg-1", "no-identity-vm", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Identity != nil {
		t.Errorf("Get: identity=%+v, want nil for a VM created without one", got.Identity)
	}
}

func assertSystemAssignedIdentity(t *testing.T, step string, id *armcompute.VirtualMachineIdentity) {
	t.Helper()

	if id == nil {
		t.Fatalf("%s: identity is nil, want SystemAssigned block", step)
	}

	if id.Type == nil || *id.Type != armcompute.ResourceIdentityTypeSystemAssigned {
		t.Errorf("%s: identity.type=%v, want SystemAssigned", step, id.Type)
	}

	if id.PrincipalID == nil || *id.PrincipalID == "" {
		t.Errorf("%s: principalId not synthesized", step)
	}

	if id.TenantID == nil || *id.TenantID == "" {
		t.Errorf("%s: tenantId not synthesized", step)
	}
}

func minimalVMProperties() *armcompute.VirtualMachineProperties {
	return &armcompute.VirtualMachineProperties{
		HardwareProfile: &armcompute.HardwareProfile{
			VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
		},
		StorageProfile: &armcompute.StorageProfile{
			ImageReference: &armcompute.ImageReference{
				Publisher: to.Ptr("Canonical"),
				Offer:     to.Ptr("UbuntuServer"),
				SKU:       to.Ptr("22.04-LTS"),
				Version:   to.Ptr("latest"),
			},
		},
		OSProfile: &armcompute.OSProfile{
			ComputerName:  to.Ptr("identity-vm"),
			AdminUsername: to.Ptr("azureuser"),
		},
	}
}
