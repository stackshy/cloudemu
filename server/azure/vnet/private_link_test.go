package vnet_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSDKPrivateLinkRoundTrip drives the real armnetwork PrivateEndpointsClient
// and PrivateLinkServicesClient through their Begin* pollers end-to-end: it
// creates a private link service (the provider side) and a private endpoint (the
// consumer side) referencing it, then asserts get/list round-trip them and
// delete makes a subsequent get 404. The key regression it guards is the create
// LRO: the pollers must complete rather than hang (every op used to fall through
// to the vnet 501 default).
func TestSDKPrivateLinkRoundTrip(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	const rg = "rg-pl"

	plsClient, err := armnetwork.NewPrivateLinkServicesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	subnetID := "/subscriptions/sub-1/resourceGroups/" + rg +
		"/providers/Microsoft.Network/virtualNetworks/vnet-pl/subnets/svc"
	frontendID := "/subscriptions/sub-1/resourceGroups/" + rg +
		"/providers/Microsoft.Network/loadBalancers/lb-pl/frontendIPConfigurations/fe"

	plsCreate, err := plsClient.BeginCreateOrUpdate(ctx, rg, "pls-1", armnetwork.PrivateLinkService{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.PrivateLinkServiceProperties{
			EnableProxyProtocol: to.Ptr(true),
			LoadBalancerFrontendIPConfigurations: []*armnetwork.FrontendIPConfiguration{
				{ID: to.Ptr(frontendID)},
			},
			IPConfigurations: []*armnetwork.PrivateLinkServiceIPConfiguration{
				{
					Name: to.Ptr("ipcfg"),
					Properties: &armnetwork.PrivateLinkServiceIPConfigurationProperties{
						Primary:                   to.Ptr(true),
						PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodDynamic),
						Subnet:                    &armnetwork.Subnet{ID: to.Ptr(subnetID)},
					},
				},
			},
			Visibility:   &armnetwork.PrivateLinkServicePropertiesVisibility{Subscriptions: []*string{to.Ptr("sub-1")}},
			AutoApproval: &armnetwork.PrivateLinkServicePropertiesAutoApproval{Subscriptions: []*string{to.Ptr("sub-1")}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("pls BeginCreateOrUpdate: %v", err)
	}

	pls := pollDone(t, plsCreate)

	if pls.Name == nil || *pls.Name != "pls-1" {
		t.Fatalf("pls name=%v want pls-1", pls.Name)
	}

	if pls.Properties == nil || pls.Properties.ProvisioningState == nil ||
		*pls.Properties.ProvisioningState != armnetwork.ProvisioningStateSucceeded {
		t.Errorf("pls provisioningState=%v want Succeeded", pls.Properties)
	}

	if pls.Properties.EnableProxyProtocol == nil || !*pls.Properties.EnableProxyProtocol {
		t.Errorf("pls enableProxyProtocol=%v want true", pls.Properties.EnableProxyProtocol)
	}

	if len(pls.Properties.IPConfigurations) != 1 ||
		pls.Properties.IPConfigurations[0].Properties.Subnet == nil ||
		*pls.Properties.IPConfigurations[0].Properties.Subnet.ID != subnetID {
		t.Errorf("pls ipConfigurations=%+v want subnet %s", pls.Properties.IPConfigurations, subnetID)
	}

	plsID := *pls.ID

	// Private endpoint (the consumer side) referencing the service above.
	peClient, err := armnetwork.NewPrivateEndpointsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	peCreate, err := peClient.BeginCreateOrUpdate(ctx, rg, "pe-1", armnetwork.PrivateEndpoint{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.PrivateEndpointProperties{
			Subnet: &armnetwork.Subnet{ID: to.Ptr(subnetID)},
			PrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{
				{
					Name: to.Ptr("conn"),
					Properties: &armnetwork.PrivateLinkServiceConnectionProperties{
						PrivateLinkServiceID: to.Ptr(plsID),
						GroupIDs:             []*string{to.Ptr("blob")},
						RequestMessage:       to.Ptr("please approve"),
					},
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("pe BeginCreateOrUpdate: %v", err)
	}

	pe := pollDone(t, peCreate)

	if pe.Properties == nil || pe.Properties.Subnet == nil || pe.Properties.Subnet.ID == nil ||
		*pe.Properties.Subnet.ID != subnetID {
		t.Errorf("pe subnet=%v want %s", pe.Properties, subnetID)
	}

	if len(pe.Properties.PrivateLinkServiceConnections) != 1 {
		t.Fatalf("pe connections=%+v want 1", pe.Properties.PrivateLinkServiceConnections)
	}

	conn := pe.Properties.PrivateLinkServiceConnections[0]
	if conn.Properties == nil || conn.Properties.PrivateLinkServiceID == nil ||
		*conn.Properties.PrivateLinkServiceID != plsID {
		t.Errorf("pe connection privateLinkServiceId=%v want %s", conn.Properties, plsID)
	}

	// The emulator auto-approves a new connection.
	if conn.Properties.PrivateLinkServiceConnectionState == nil ||
		conn.Properties.PrivateLinkServiceConnectionState.Status == nil ||
		*conn.Properties.PrivateLinkServiceConnectionState.Status != "Approved" {
		t.Errorf("pe connection state=%v want Approved", conn.Properties.PrivateLinkServiceConnectionState)
	}

	assertPrivateLinkGetList(t, ctx, peClient, plsClient, rg)
	assertPrivateLinkDelete(t, ctx, peClient, plsClient, rg)
}

// assertPrivateLinkGetList checks Get and the list pagers return each resource.
func assertPrivateLinkGetList(
	t *testing.T, ctx context.Context,
	pe *armnetwork.PrivateEndpointsClient,
	pls *armnetwork.PrivateLinkServicesClient,
	rg string,
) {
	t.Helper()

	if _, err := pe.Get(ctx, rg, "pe-1", nil); err != nil {
		t.Errorf("pe Get: %v", err)
	}

	if _, err := pls.Get(ctx, rg, "pls-1", nil); err != nil {
		t.Errorf("pls Get: %v", err)
	}

	pePager := pe.NewListPager(rg, nil)
	if got := drainNames(t, ctx, pePager.More, func() ([]*string, error) {
		p, err := pePager.NextPage(ctx)
		return peNames(p.Value), err
	}); len(got) != 1 || *got[0] != "pe-1" {
		t.Errorf("pe list=%v want [pe-1]", derefAll(got))
	}

	plsPager := pls.NewListPager(rg, nil)
	if got := drainNames(t, ctx, plsPager.More, func() ([]*string, error) {
		p, err := plsPager.NextPage(ctx)
		return plsNames(p.Value), err
	}); len(got) != 1 || *got[0] != "pls-1" {
		t.Errorf("pls list=%v want [pls-1]", derefAll(got))
	}
}

// assertPrivateLinkDelete deletes both resources and confirms a follow-up Get 404s.
func assertPrivateLinkDelete(
	t *testing.T, ctx context.Context,
	pe *armnetwork.PrivateEndpointsClient,
	pls *armnetwork.PrivateLinkServicesClient,
	rg string,
) {
	t.Helper()

	peDelete, err := pe.BeginDelete(ctx, rg, "pe-1", nil)
	if err != nil {
		t.Fatalf("pe BeginDelete: %v", err)
	}

	pollDone(t, peDelete)

	plsDelete, err := pls.BeginDelete(ctx, rg, "pls-1", nil)
	if err != nil {
		t.Fatalf("pls BeginDelete: %v", err)
	}

	pollDone(t, plsDelete)

	_, err = pe.Get(ctx, rg, "pe-1", nil)
	assertNotFound(t, err, "pe Get after delete")

	_, err = pls.Get(ctx, rg, "pls-1", nil)
	assertNotFound(t, err, "pls Get after delete")
}

func peNames(in []*armnetwork.PrivateEndpoint) []*string {
	out := make([]*string, 0, len(in))
	for _, p := range in {
		out = append(out, p.Name)
	}

	return out
}

func plsNames(in []*armnetwork.PrivateLinkService) []*string {
	out := make([]*string, 0, len(in))
	for _, p := range in {
		out = append(out, p.Name)
	}

	return out
}
