package databricks_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"
)

const vnetPeeringResourceType = "Microsoft.Databricks/workspaces/virtualNetworkPeerings"

// newVNetPeeringClient builds a real armdatabricks VNetPeering client pointed at
// the in-memory emulator.
func newVNetPeeringClient(t *testing.T, opts *arm.ClientOptions, sub string) *armdatabricks.VNetPeeringClient {
	t.Helper()

	client, err := armdatabricks.NewVNetPeeringClient(sub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new vnet peering client: %v", err)
	}

	return client
}

// createPeering performs a BeginCreateOrUpdate + PollUntilDone against the
// emulator, echoing back the standard peering config used by the tests.
func createPeering(
	t *testing.T, client *armdatabricks.VNetPeeringClient, name, remoteVNetID string,
) armdatabricks.VirtualNetworkPeering {
	t.Helper()

	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, testRG, testWS, name, armdatabricks.VirtualNetworkPeering{
		Properties: &armdatabricks.VirtualNetworkPeeringPropertiesFormat{
			AllowVirtualNetworkAccess: to.Ptr(true),
			AllowForwardedTraffic:     to.Ptr(true),
			RemoteVirtualNetwork: &armdatabricks.VirtualNetworkPeeringPropertiesFormatRemoteVirtualNetwork{
				ID: to.Ptr(remoteVNetID),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("create PollUntilDone: %v", err)
	}

	return res.VirtualNetworkPeering
}

func TestSDKVNetPeeringLifecycle(t *testing.T) {
	opts, sub := newARMOptions(t)
	seedWorkspace(t, opts, testRG, testWS)

	client := newVNetPeeringClient(t, opts, sub)
	ctx := context.Background()

	const (
		peeringName  = "peer-1"
		remoteVNetID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/remote"
	)

	created := createPeering(t, client, peeringName, remoteVNetID)

	if created.Name == nil || *created.Name != peeringName {
		t.Fatalf("got name %v, want %q", created.Name, peeringName)
	}

	if created.Type == nil || *created.Type != vnetPeeringResourceType {
		t.Fatalf("got type %v, want %q", created.Type, vnetPeeringResourceType)
	}

	if created.Properties == nil {
		t.Fatal("expected peering properties on create")
	}

	if created.Properties.PeeringState == nil ||
		*created.Properties.PeeringState != armdatabricks.PeeringStateConnected {
		t.Fatalf("got peering state %v, want Connected", created.Properties.PeeringState)
	}

	if created.Properties.ProvisioningState == nil ||
		*created.Properties.ProvisioningState != armdatabricks.PeeringProvisioningStateSucceeded {
		t.Fatalf("got provisioning state %v, want Succeeded", created.Properties.ProvisioningState)
	}

	if created.Properties.AllowVirtualNetworkAccess == nil || !*created.Properties.AllowVirtualNetworkAccess {
		t.Fatalf("expected AllowVirtualNetworkAccess true, got %v", created.Properties.AllowVirtualNetworkAccess)
	}

	if created.Properties.RemoteVirtualNetwork == nil ||
		created.Properties.RemoteVirtualNetwork.ID == nil ||
		*created.Properties.RemoteVirtualNetwork.ID != remoteVNetID {
		t.Fatalf("got remote vnet %v, want %q", created.Properties.RemoteVirtualNetwork, remoteVNetID)
	}

	got, err := client.Get(ctx, testRG, testWS, peeringName, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name == nil || *got.Name != peeringName {
		t.Fatalf("Get got name %v, want %q", got.Name, peeringName)
	}

	if got.Properties == nil ||
		got.Properties.AllowForwardedTraffic == nil ||
		!*got.Properties.AllowForwardedTraffic {
		t.Fatalf("expected AllowForwardedTraffic true echoed back, got %+v", got.Properties)
	}

	pager := client.NewListByWorkspacePager(testRG, testWS, nil)

	page, err := pager.NextPage(ctx)
	if err != nil {
		t.Fatalf("ListByWorkspace: %v", err)
	}

	if len(page.Value) != 1 {
		t.Fatalf("got %d peerings, want 1", len(page.Value))
	}

	delPoller, err := client.BeginDelete(ctx, testRG, testWS, peeringName, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err = delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete PollUntilDone: %v", err)
	}

	if _, err = client.Get(ctx, testRG, testWS, peeringName, nil); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestSDKVNetPeeringCreateMissingWorkspace(t *testing.T) {
	opts, sub := newARMOptions(t)

	// No workspace is seeded: the parent does not exist.
	client := newVNetPeeringClient(t, opts, sub)

	poller, err := client.BeginCreateOrUpdate(context.Background(), testRG, testWS, "peer-1",
		armdatabricks.VirtualNetworkPeering{
			Properties: &armdatabricks.VirtualNetworkPeeringPropertiesFormat{
				AllowVirtualNetworkAccess: to.Ptr(true),
				RemoteVirtualNetwork: &armdatabricks.VirtualNetworkPeeringPropertiesFormatRemoteVirtualNetwork{
					ID: to.Ptr("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/remote"),
				},
			},
		}, nil)
	if err != nil {
		// A synchronous failure is an acceptable outcome too.
		return
	}

	if _, err = poller.PollUntilDone(context.Background(), nil); err == nil {
		t.Fatal("expected error creating peering against a missing workspace")
	}
}

func TestSDKVNetPeeringGetMissing(t *testing.T) {
	opts, sub := newARMOptions(t)
	seedWorkspace(t, opts, testRG, testWS)

	client := newVNetPeeringClient(t, opts, sub)

	if _, err := client.Get(context.Background(), testRG, testWS, "does-not-exist", nil); err == nil {
		t.Fatal("expected error for missing peering")
	}
}

func TestSDKVNetPeeringListMultiple(t *testing.T) {
	opts, sub := newARMOptions(t)
	seedWorkspace(t, opts, testRG, testWS)

	client := newVNetPeeringClient(t, opts, sub)
	ctx := context.Background()

	createPeering(t, client, "peer-a",
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/remote-a")
	createPeering(t, client, "peer-b",
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/remote-b")

	pager := client.NewListByWorkspacePager(testRG, testWS, nil)

	var count int

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListByWorkspace: %v", err)
		}

		count += len(page.Value)
	}

	if count != 2 {
		t.Fatalf("got %d peerings, want 2", count)
	}
}
