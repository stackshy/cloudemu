package vpc_test

import (
	"context"
	"net/http/httptest"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

// TestSDKSubnetworkRegionScoped covers the finding that subnetwork get/delete
// ignored the region: two subnets of the same name in different regions
// resolved to the first found (wrong CIDR / wrong region on delete).
func TestSDKSubnetworkRegionScoped(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	netClient, err := gcpcompute.NewNetworksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewNetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = netClient.Close() })

	netOp, err := netClient.Insert(ctx, &computepb.InsertNetworkRequest{
		Project: testProject,
		NetworkResource: &computepb.Network{
			Name:                  ptrStr("vpc-a"),
			AutoCreateSubnetworks: func() *bool { b := false; return &b }(),
		},
	})
	if err != nil {
		t.Fatalf("network Insert: %v", err)
	}

	if err := netOp.Wait(ctx); err != nil {
		t.Fatalf("network Insert wait: %v", err)
	}

	subClient, err := gcpcompute.NewSubnetworksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewSubnetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = subClient.Close() })

	insertSubnet(t, ctx, subClient, "us-central1", "dup-sub", "10.1.0.0/16")
	insertSubnet(t, ctx, subClient, "us-east1", "dup-sub", "10.2.0.0/16")

	got, err := subClient.Get(ctx, &computepb.GetSubnetworkRequest{
		Project: testProject, Region: "us-east1", Subnetwork: "dup-sub",
	})
	if err != nil {
		t.Fatalf("Get us-east1: %v", err)
	}

	// Finding: must resolve the us-east1 subnet (10.2.0.0/16), not first-found.
	if got.GetIpCidrRange() != "10.2.0.0/16" {
		t.Errorf("ipCidrRange=%s want 10.2.0.0/16 (region ignored)", got.GetIpCidrRange())
	}

	// Finding: gatewayAddress/purpose/stackType/fingerprint/creationTimestamp
	// must be populated.
	if got.GetGatewayAddress() != "10.2.0.1" {
		t.Errorf("gatewayAddress=%s want 10.2.0.1", got.GetGatewayAddress())
	}

	if got.GetPurpose() != "PRIVATE" || got.GetStackType() != "IPV4_ONLY" {
		t.Errorf("purpose=%s stackType=%s want PRIVATE/IPV4_ONLY", got.GetPurpose(), got.GetStackType())
	}

	if got.GetFingerprint() == "" {
		t.Error("fingerprint empty, want a value (needed to patch)")
	}

	if got.GetCreationTimestamp() == "" {
		t.Error("creationTimestamp empty, want RFC3339")
	}
}

// TestSDKSubnetworkSecondaryRangesAndDescription covers the HIGH finding that a
// subnetwork's description and secondaryIpRanges (GKE VPC-native / Terraform
// secondary_ip_range) were dropped on insert and read back empty.
func TestSDKSubnetworkSecondaryRangesAndDescription(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	netClient, subClient := newNetAndSubnetClients(t, ctx, ts, "vpc-secondary")

	op, err := subClient.Insert(ctx, &computepb.InsertSubnetworkRequest{
		Project: testProject,
		Region:  testRegion,
		SubnetworkResource: &computepb.Subnetwork{
			Name:        ptrStr("sub-sec"),
			Network:     ptrStr("projects/" + testProject + "/global/networks/vpc-secondary"),
			IpCidrRange: ptrStr("10.0.0.0/24"),
			Description: ptrStr("primary subnet"),
			SecondaryIpRanges: []*computepb.SubnetworkSecondaryRange{
				{RangeName: ptrStr("pods"), IpCidrRange: ptrStr("10.1.0.0/16")},
				{RangeName: ptrStr("services"), IpCidrRange: ptrStr("10.2.0.0/20")},
			},
		},
	})
	if err != nil {
		t.Fatalf("subnet Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("subnet Insert wait: %v", err)
	}

	_ = netClient

	got, err := subClient.Get(ctx, &computepb.GetSubnetworkRequest{
		Project: testProject, Region: testRegion, Subnetwork: "sub-sec",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetDescription() != "primary subnet" {
		t.Errorf("description=%q want %q", got.GetDescription(), "primary subnet")
	}

	sec := got.GetSecondaryIpRanges()
	if len(sec) != 2 {
		t.Fatalf("secondaryIpRanges len=%d want 2: %+v", len(sec), sec)
	}

	if sec[0].GetRangeName() != "pods" || sec[0].GetIpCidrRange() != "10.1.0.0/16" {
		t.Errorf("secondary[0]=%s/%s want pods/10.1.0.0/16", sec[0].GetRangeName(), sec[0].GetIpCidrRange())
	}

	if sec[1].GetRangeName() != "services" || sec[1].GetIpCidrRange() != "10.2.0.0/20" {
		t.Errorf("secondary[1]=%s/%s want services/10.2.0.0/20", sec[1].GetRangeName(), sec[1].GetIpCidrRange())
	}
}

// TestSDKSubnetworkExpandIpCidrRange covers the MED finding that
// subnetworks.expandIpCidrRange was unrouted (405). A superset widens the
// subnet; a subset is rejected.
func TestSDKSubnetworkExpandIpCidrRange(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	_, subClient := newNetAndSubnetClients(t, ctx, ts, "vpc-expand")

	insertSubnet2(t, ctx, subClient, testRegion, "sub-exp", "vpc-expand", "10.0.0.0/24")

	op, err := subClient.ExpandIpCidrRange(ctx, &computepb.ExpandIpCidrRangeSubnetworkRequest{
		Project: testProject, Region: testRegion, Subnetwork: "sub-exp",
		SubnetworksExpandIpCidrRangeRequestResource: &computepb.SubnetworksExpandIpCidrRangeRequest{
			IpCidrRange: ptrStr("10.0.0.0/20"),
		},
	})
	if err != nil {
		t.Fatalf("ExpandIpCidrRange superset: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("ExpandIpCidrRange wait: %v", err)
	}

	got, err := subClient.Get(ctx, &computepb.GetSubnetworkRequest{
		Project: testProject, Region: testRegion, Subnetwork: "sub-exp",
	})
	if err != nil {
		t.Fatalf("Get after expand: %v", err)
	}

	if got.GetIpCidrRange() != "10.0.0.0/20" {
		t.Errorf("ipCidrRange=%s want 10.0.0.0/20 (not widened)", got.GetIpCidrRange())
	}

	// A subset must be rejected — expandIpCidrRange only grows the range.
	_, err = subClient.ExpandIpCidrRange(ctx, &computepb.ExpandIpCidrRangeSubnetworkRequest{
		Project: testProject, Region: testRegion, Subnetwork: "sub-exp",
		SubnetworksExpandIpCidrRangeRequestResource: &computepb.SubnetworksExpandIpCidrRangeRequest{
			IpCidrRange: ptrStr("10.0.0.0/28"),
		},
	})
	if err == nil {
		t.Error("ExpandIpCidrRange subset succeeded, want error")
	}
}

// newNetAndSubnetClients creates a custom-mode network and returns Networks +
// Subnetworks clients for it.
func newNetAndSubnetClients(t *testing.T, ctx context.Context, ts *httptest.Server, netName string,
) (*gcpcompute.NetworksClient, *gcpcompute.SubnetworksClient) {
	t.Helper()

	netClient, err := gcpcompute.NewNetworksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewNetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = netClient.Close() })

	netOp, err := netClient.Insert(ctx, &computepb.InsertNetworkRequest{
		Project: testProject,
		NetworkResource: &computepb.Network{
			Name:                  ptrStr(netName),
			AutoCreateSubnetworks: func() *bool { b := false; return &b }(),
		},
	})
	if err != nil {
		t.Fatalf("network Insert: %v", err)
	}

	if err := netOp.Wait(ctx); err != nil {
		t.Fatalf("network Insert wait: %v", err)
	}

	subClient, err := gcpcompute.NewSubnetworksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewSubnetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = subClient.Close() })

	return netClient, subClient
}

// insertSubnet2 inserts a subnet referencing the named network.
func insertSubnet2(t *testing.T, ctx context.Context, c *gcpcompute.SubnetworksClient, region, name, netName, cidr string) {
	t.Helper()

	op, err := c.Insert(ctx, &computepb.InsertSubnetworkRequest{
		Project: testProject,
		Region:  region,
		SubnetworkResource: &computepb.Subnetwork{
			Name:        ptrStr(name),
			Network:     ptrStr("projects/" + testProject + "/global/networks/" + netName),
			IpCidrRange: ptrStr(cidr),
		},
	})
	if err != nil {
		t.Fatalf("subnet Insert %s/%s: %v", region, name, err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("subnet Insert wait %s/%s: %v", region, name, err)
	}
}

func insertSubnet(t *testing.T, ctx context.Context, c *gcpcompute.SubnetworksClient, region, name, cidr string) {
	t.Helper()

	op, err := c.Insert(ctx, &computepb.InsertSubnetworkRequest{
		Project: testProject,
		Region:  region,
		SubnetworkResource: &computepb.Subnetwork{
			Name:        ptrStr(name),
			Network:     ptrStr("projects/" + testProject + "/global/networks/vpc-a"),
			IpCidrRange: ptrStr(cidr),
		},
	})
	if err != nil {
		t.Fatalf("subnet Insert %s/%s: %v", region, name, err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("subnet Insert wait %s/%s: %v", region, name, err)
	}
}
