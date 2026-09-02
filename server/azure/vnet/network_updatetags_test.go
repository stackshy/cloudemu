package vnet_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
)

// TestSDKApplicationSecurityGroupUpdateTags drives the real armnetwork
// ApplicationSecurityGroupsClient.UpdateTags (a synchronous PATCH): the supplied
// tag is merged into the existing set, the ASG's other fields survive, the full
// resource comes back, and UpdateTags on a missing ASG is a 404.
func TestSDKApplicationSecurityGroupUpdateTags(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	client, err := armnetwork.NewApplicationSecurityGroupsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	pollDone(t, mustBeginASG(t, ctx, client, "rg-1", "asg-tag", armnetwork.ApplicationSecurityGroup{
		Location: to.Ptr("westus2"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
	}))

	updated, err := client.UpdateTags(ctx, "rg-1", "asg-tag", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}

	// Merge: the new tag is added and the create-time tag is preserved.
	assertTag(t, updated.Tags, "env", "prod")
	assertTag(t, updated.Tags, "team", "net")

	// Full resource: name, location and provisioningState come back on the PATCH.
	if updated.Name == nil || *updated.Name != "asg-tag" {
		t.Errorf("name=%v want asg-tag", updated.Name)
	}

	if updated.Location == nil || *updated.Location != "westus2" {
		t.Errorf("location=%v want westus2 (property must survive UpdateTags)", updated.Location)
	}

	if updated.Properties == nil || updated.Properties.ProvisioningState == nil ||
		*updated.Properties.ProvisioningState != armnetwork.ProvisioningStateSucceeded {
		t.Errorf("provisioningState=%v want Succeeded", updated.Properties)
	}

	// A follow-up GET reflects the merged set (the merge was persisted, not echoed).
	got, err := client.Get(ctx, "rg-1", "asg-tag", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertTag(t, got.Tags, "env", "prod")
	assertTag(t, got.Tags, "team", "net")

	_, err = client.UpdateTags(ctx, "rg-1", "missing", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	assertNotFound(t, err, "ASG UpdateTags on missing")
}

// TestSDKPublicIPPrefixUpdateTags drives PublicIPPrefixesClient.UpdateTags (a
// synchronous PATCH): the tag merges while the prefix's properties (prefixLength,
// synthesized ipPrefix, sku) are left intact, and UpdateTags on a missing prefix
// is a 404.
func TestSDKPublicIPPrefixUpdateTags(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	client, err := armnetwork.NewPublicIPPrefixesClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	const prefixLength = int32(28)

	created := pollDone(t, mustBeginPrefix(t, ctx, client, "rg-1", "pfx-tag", armnetwork.PublicIPPrefix{
		Location: to.Ptr("eastus"),
		SKU: &armnetwork.PublicIPPrefixSKU{
			Name: to.Ptr(armnetwork.PublicIPPrefixSKUNameStandard),
			Tier: to.Ptr(armnetwork.PublicIPPrefixSKUTierRegional),
		},
		Tags: map[string]*string{"env": to.Ptr("prod")},
		Properties: &armnetwork.PublicIPPrefixPropertiesFormat{
			PrefixLength: to.Ptr(prefixLength),
		},
	}))

	wantIPPrefix := *created.Properties.IPPrefix

	updated, err := client.UpdateTags(ctx, "rg-1", "pfx-tag", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateTags: %v", err)
	}

	assertTag(t, updated.Tags, "env", "prod")
	assertTag(t, updated.Tags, "team", "net")

	// Properties untouched by an UpdateTags PATCH.
	if updated.Properties == nil || updated.Properties.PrefixLength == nil ||
		*updated.Properties.PrefixLength != prefixLength {
		t.Errorf("prefixLength=%v want %d (property must survive UpdateTags)", updated.Properties, prefixLength)
	}

	if updated.Properties.IPPrefix == nil || *updated.Properties.IPPrefix != wantIPPrefix {
		t.Errorf("ipPrefix=%v want %s (unchanged)", updated.Properties.IPPrefix, wantIPPrefix)
	}

	if updated.SKU == nil || updated.SKU.Name == nil ||
		*updated.SKU.Name != armnetwork.PublicIPPrefixSKUNameStandard {
		t.Errorf("sku=%v want Standard (must survive UpdateTags)", updated.SKU)
	}

	_, err = client.UpdateTags(ctx, "rg-1", "missing", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	assertNotFound(t, err, "public IP prefix UpdateTags on missing")
}

// TestSDKNetworkGatewaysUpdateTags drives UpdateTags across all three gateway
// families: VirtualNetworkGatewaysClient.BeginUpdateTags (LRO),
// LocalNetworkGatewaysClient.UpdateTags (sync) and
// VirtualNetworkGatewayConnectionsClient.BeginUpdateTags (LRO). Each merges a tag
// while its properties survive; UpdateTags on a missing resource is a 404.
func TestSDKNetworkGatewaysUpdateTags(t *testing.T) {
	ts := newVNetServer(t)
	ctx := context.Background()
	opts := clientOpts(ts)

	const rg = "rg-gwtag"

	subnetID := seedGatewayPrerequisites(t, ctx, opts, rg)
	pipID := "/subscriptions/sub-1/resourceGroups/" + rg + "/providers/Microsoft.Network/publicIPAddresses/gw-pip"

	vngID := patchVNGatewayTags(t, ctx, opts, rg, subnetID, pipID)
	lngID := patchLNGatewayTags(t, ctx, opts, rg)
	patchConnectionTags(t, ctx, opts, rg, vngID, lngID)
}

// patchVNGatewayTags creates a virtual network gateway with a tag, merges a
// second via BeginUpdateTags, asserts both tags plus a preserved property, checks
// the 404 path, and returns the gateway id.
func patchVNGatewayTags(
	t *testing.T, ctx context.Context, opts *arm.ClientOptions, rg, subnetID, pipID string,
) string {
	t.Helper()

	client, err := armnetwork.NewVirtualNetworkGatewaysClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	create, err := client.BeginCreateOrUpdate(ctx, rg, "vng-tag", armnetwork.VirtualNetworkGateway{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armnetwork.VirtualNetworkGatewayPropertiesFormat{
			GatewayType: to.Ptr(armnetwork.VirtualNetworkGatewayTypeVPN),
			VPNType:     to.Ptr(armnetwork.VPNTypeRouteBased),
			IPConfigurations: []*armnetwork.VirtualNetworkGatewayIPConfiguration{{
				Name: to.Ptr("gwipconfig"),
				Properties: &armnetwork.VirtualNetworkGatewayIPConfigurationPropertiesFormat{
					PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodDynamic),
					Subnet:                    &armnetwork.SubResource{ID: to.Ptr(subnetID)},
					PublicIPAddress:           &armnetwork.SubResource{ID: to.Ptr(pipID)},
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("vng BeginCreateOrUpdate: %v", err)
	}

	created := pollDone(t, create)

	tagsP, err := client.BeginUpdateTags(ctx, rg, "vng-tag", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	if err != nil {
		t.Fatalf("vng BeginUpdateTags: %v", err)
	}

	updated := pollDone(t, tagsP)

	assertTag(t, updated.Tags, "env", "prod")
	assertTag(t, updated.Tags, "team", "net")

	if updated.Properties == nil || updated.Properties.GatewayType == nil ||
		*updated.Properties.GatewayType != armnetwork.VirtualNetworkGatewayTypeVPN {
		t.Errorf("gatewayType=%v want Vpn (property must survive UpdateTags)", updated.Properties)
	}

	_, err = client.BeginUpdateTags(ctx, rg, "missing", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	assertNotFound(t, err, "vng UpdateTags on missing")

	return *created.ID
}

// patchLNGatewayTags creates a local network gateway with a tag, merges a second
// via the synchronous UpdateTags, asserts both tags plus a preserved property,
// checks the 404 path, and returns the gateway id.
func patchLNGatewayTags(t *testing.T, ctx context.Context, opts *arm.ClientOptions, rg string) string {
	t.Helper()

	client, err := armnetwork.NewLocalNetworkGatewaysClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	create, err := client.BeginCreateOrUpdate(ctx, rg, "lng-tag", armnetwork.LocalNetworkGateway{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armnetwork.LocalNetworkGatewayPropertiesFormat{
			GatewayIPAddress: to.Ptr("203.0.113.10"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("lng BeginCreateOrUpdate: %v", err)
	}

	created := pollDone(t, create)

	updated, err := client.UpdateTags(ctx, rg, "lng-tag", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	if err != nil {
		t.Fatalf("lng UpdateTags: %v", err)
	}

	assertTag(t, updated.Tags, "env", "prod")
	assertTag(t, updated.Tags, "team", "net")

	if updated.Properties == nil || updated.Properties.GatewayIPAddress == nil ||
		*updated.Properties.GatewayIPAddress != "203.0.113.10" {
		t.Errorf("gatewayIpAddress=%v want 203.0.113.10 (property must survive UpdateTags)", updated.Properties)
	}

	_, err = client.UpdateTags(ctx, rg, "missing", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	assertNotFound(t, err, "lng UpdateTags on missing")

	return *created.ID
}

// patchConnectionTags creates a connection between the two gateways with a tag,
// merges a second via BeginUpdateTags, asserts both tags plus a preserved
// property, and checks the 404 path.
func patchConnectionTags(t *testing.T, ctx context.Context, opts *arm.ClientOptions, rg, vngID, lngID string) {
	t.Helper()

	client, err := armnetwork.NewVirtualNetworkGatewayConnectionsClient("sub-1", fakeCred{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	create, err := client.BeginCreateOrUpdate(ctx, rg, "conn-tag", armnetwork.VirtualNetworkGatewayConnection{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
		Properties: &armnetwork.VirtualNetworkGatewayConnectionPropertiesFormat{
			ConnectionType:         to.Ptr(armnetwork.VirtualNetworkGatewayConnectionTypeIPsec),
			VirtualNetworkGateway1: &armnetwork.VirtualNetworkGateway{ID: to.Ptr(vngID)},
			LocalNetworkGateway2:   &armnetwork.LocalNetworkGateway{ID: to.Ptr(lngID)},
			SharedKey:              to.Ptr("s3cr3t-psk"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("conn BeginCreateOrUpdate: %v", err)
	}

	pollDone(t, create)

	tagsP, err := client.BeginUpdateTags(ctx, rg, "conn-tag", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	if err != nil {
		t.Fatalf("conn BeginUpdateTags: %v", err)
	}

	updated := pollDone(t, tagsP)

	assertTag(t, updated.Tags, "env", "prod")
	assertTag(t, updated.Tags, "team", "net")

	if updated.Properties == nil || updated.Properties.ConnectionType == nil ||
		*updated.Properties.ConnectionType != armnetwork.VirtualNetworkGatewayConnectionTypeIPsec {
		t.Errorf("connectionType=%v want IPsec (property must survive UpdateTags)", updated.Properties)
	}

	_, err = client.BeginUpdateTags(ctx, rg, "missing", armnetwork.TagsObject{
		Tags: map[string]*string{"team": to.Ptr("net")},
	}, nil)
	assertNotFound(t, err, "conn UpdateTags on missing")
}

// mustBeginPrefix starts a public IP prefix create, failing the test on a
// dispatch error before the poll.
func mustBeginPrefix(
	t *testing.T, ctx context.Context, client *armnetwork.PublicIPPrefixesClient,
	rg, name string, body armnetwork.PublicIPPrefix,
) *runtime.Poller[armnetwork.PublicIPPrefixesClientCreateOrUpdateResponse] {
	t.Helper()

	p, err := client.BeginCreateOrUpdate(ctx, rg, name, body, nil)
	if err != nil {
		t.Fatalf("prefix BeginCreateOrUpdate %s/%s: %v", rg, name, err)
	}

	return p
}

// assertTag fails the test unless tags[key] is present and equal to want.
func assertTag(t *testing.T, tags map[string]*string, key, want string) {
	t.Helper()

	got, ok := tags[key]
	if !ok || got == nil {
		t.Errorf("tags[%s] missing, want %q (tags=%v)", key, want, derefTags(tags))
		return
	}

	if *got != want {
		t.Errorf("tags[%s]=%q want %q", key, *got, want)
	}
}

// derefTags renders a tag map for error messages.
func derefTags(in map[string]*string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if v != nil {
			out[k] = *v
		}
	}

	return out
}
