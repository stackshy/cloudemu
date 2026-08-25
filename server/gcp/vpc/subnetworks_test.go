package vpc_test

import (
	"context"
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
