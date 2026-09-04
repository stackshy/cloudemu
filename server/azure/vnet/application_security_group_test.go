package vnet_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSDKApplicationSecurityGroupRoundTrip drives the real armnetwork
// ApplicationSecurityGroupsClient through create / get / list / delete. ASGs are
// sync-200 (no BeginX data plane): the poller must complete on the terminal 200.
func TestSDKApplicationSecurityGroupRoundTrip(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	client, err := armnetwork.NewApplicationSecurityGroupsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	created := pollDone(t, mustBeginASG(t, ctx, client, "rg-1", "asg-web", armnetwork.ApplicationSecurityGroup{
		Location: to.Ptr("westus2"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
	}))

	if created.Name == nil || *created.Name != "asg-web" {
		t.Fatalf("name=%v want asg-web", created.Name)
	}

	if created.Properties == nil || created.Properties.ProvisioningState == nil ||
		*created.Properties.ProvisioningState != armnetwork.ProvisioningStateSucceeded {
		t.Fatalf("provisioningState=%v want Succeeded", created.Properties)
	}

	got, err := client.Get(ctx, "rg-1", "asg-web", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Location == nil || *got.Location != "westus2" {
		t.Fatalf("location=%v want westus2", got.Location)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "prod" {
		t.Fatalf("tags[env]=%v want prod", got.Tags["env"])
	}

	// A same-named ASG in a second resource group must not leak into rg-1's list.
	pollDone(t, mustBeginASG(t, ctx, client, "rg-2", "asg-web", armnetwork.ApplicationSecurityGroup{
		Location: to.Ptr("eastus"),
	}))

	count := 0

	pager := client.NewListPager("rg-1", nil)
	for pager.More() {
		page, pErr := pager.NextPage(ctx)
		if pErr != nil {
			t.Fatalf("list rg-1: %v", pErr)
		}

		count += len(page.Value)
	}

	if count != 1 {
		t.Errorf("List(rg-1) returned %d ASGs, want 1 (rg-2's must not leak in)", count)
	}

	dp, err := client.BeginDelete(ctx, "rg-1", "asg-web", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	pollDone(t, dp)

	_, err = client.Get(ctx, "rg-1", "asg-web", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}

// TestSDKNICReferencesApplicationSecurityGroup guards that an ASG id referenced
// from a NIC ipConfiguration's applicationSecurityGroups round-trips on GET —
// the additive threading through buildIPConfigs / toNICResponse.
func TestSDKNICReferencesApplicationSecurityGroup(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	asgs, err := armnetwork.NewApplicationSecurityGroupsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	pollDone(t, mustBeginASG(t, ctx, asgs, "rg-1", "asg-app", armnetwork.ApplicationSecurityGroup{
		Location: to.Ptr("eastus"),
	}))

	asgID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/applicationSecurityGroups/asg-app"

	nics, err := armnetwork.NewInterfacesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	np, err := nics.BeginCreateOrUpdate(ctx, "rg-1", "nic-asg", armnetwork.Interface{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.InterfacePropertiesFormat{
			IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
				Name: to.Ptr("ipconfig1"),
				Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
					ApplicationSecurityGroups: []*armnetwork.ApplicationSecurityGroup{{ID: to.Ptr(asgID)}},
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create nic: %v", err)
	}

	pollDone(t, np)

	got, err := nics.Get(ctx, "rg-1", "nic-asg", nil)
	if err != nil {
		t.Fatalf("Get nic: %v", err)
	}

	if got.Properties == nil || len(got.Properties.IPConfigurations) != 1 {
		t.Fatalf("ipConfigurations=%+v want 1", got.Properties)
	}

	refs := got.Properties.IPConfigurations[0].Properties.ApplicationSecurityGroups
	if len(refs) != 1 || refs[0].ID == nil || *refs[0].ID != asgID {
		t.Fatalf("ipConfig applicationSecurityGroups=%v want [%s]", refs, asgID)
	}
}

func mustBeginASG(
	t *testing.T, ctx context.Context, client *armnetwork.ApplicationSecurityGroupsClient,
	rg, name string, body armnetwork.ApplicationSecurityGroup,
) *runtime.Poller[armnetwork.ApplicationSecurityGroupsClientCreateOrUpdateResponse] {
	t.Helper()

	p, err := client.BeginCreateOrUpdate(ctx, rg, name, body, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate %s/%s: %v", rg, name, err)
	}

	return p
}
