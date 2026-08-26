package vpc_test

import (
	"context"
	"net/http"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

func ptrBool(b bool) *bool { return &b }

func newSubClient(t *testing.T, url string, hc *http.Client) *gcpcompute.SubnetworksClient {
	t.Helper()

	c, err := gcpcompute.NewSubnetworksRESTClient(context.Background(),
		option.WithEndpoint(url), option.WithoutAuthentication(), option.WithHTTPClient(hc))
	if err != nil {
		t.Fatalf("NewSubnetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func newFwClient(t *testing.T, url string, hc *http.Client) *gcpcompute.FirewallsClient {
	t.Helper()

	c, err := gcpcompute.NewFirewallsRESTClient(context.Background(),
		option.WithEndpoint(url), option.WithoutAuthentication(), option.WithHTTPClient(hc))
	if err != nil {
		t.Fatalf("NewFirewallsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func insertNet(t *testing.T, ctx context.Context, c *gcpcompute.NetworksClient, name string) {
	t.Helper()
	mustInsertNetwork(t, ctx, c, &computepb.Network{
		Name:                  ptrStr(name),
		AutoCreateSubnetworks: ptrBool(false),
	})
}

// TestSDKDuplicateSubnetworkConflict covers BUG1: a second subnetwork insert
// with the same name+region must 409, not silently shadow the first.
func TestSDKDuplicateSubnetworkConflict(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	netClient := newNetClient(t, ts.URL, ts.Client())
	insertNet(t, ctx, netClient, "vpc-a")

	subClient := newSubClient(t, ts.URL, ts.Client())
	insertSubnet(t, ctx, subClient, testRegion, "dup", "10.9.0.0/16")

	_, err := subClient.Insert(ctx, &computepb.InsertSubnetworkRequest{
		Project: testProject,
		Region:  testRegion,
		SubnetworkResource: &computepb.Subnetwork{
			Name:        ptrStr("dup"),
			Network:     ptrStr("projects/" + testProject + "/global/networks/vpc-a"),
			IpCidrRange: ptrStr("10.10.0.0/16"),
		},
	})
	if err == nil {
		t.Fatal("duplicate subnetwork Insert succeeded, want 409")
	}
}

// TestSDKDuplicateFirewallConflict covers BUG2: a duplicate firewall insert
// must 409.
func TestSDKDuplicateFirewallConflict(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	client := newFwClient(t, ts.URL, ts.Client())

	insertFw := func() error {
		op, err := client.Insert(ctx, &computepb.InsertFirewallRequest{
			Project: testProject,
			FirewallResource: &computepb.Firewall{
				Name:    ptrStr("fw-dup"),
				Allowed: []*computepb.Allowed{{IPProtocol: ptrStr("tcp"), Ports: []string{"22"}}},
			},
		})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	}

	if err := insertFw(); err != nil {
		t.Fatalf("first firewall Insert: %v", err)
	}

	if err := insertFw(); err == nil {
		t.Fatal("duplicate firewall Insert succeeded, want 409")
	}
}

// TestSDKNetworkPatch covers BUG3: PATCH network updates routingMode and the
// change is reflected on GET.
func TestSDKNetworkPatch(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	client := newNetClient(t, ts.URL, ts.Client())
	insertNet(t, ctx, client, "net-p")

	op, err := client.Patch(ctx, &computepb.PatchNetworkRequest{
		Project: testProject,
		Network: "net-p",
		NetworkResource: &computepb.Network{
			RoutingConfig: &computepb.NetworkRoutingConfig{RoutingMode: ptrStr("GLOBAL")},
		},
	})
	if err != nil {
		t.Fatalf("network Patch: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("network Patch wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetNetworkRequest{Project: testProject, Network: "net-p"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetRoutingConfig().GetRoutingMode() != "GLOBAL" {
		t.Errorf("routingMode=%q want GLOBAL", got.GetRoutingConfig().GetRoutingMode())
	}
}

// TestSDKSubnetworkPatch covers BUG3: PATCH subnetwork with a valid fingerprint
// applies privateIpGoogleAccess; a stale fingerprint is rejected 412.
func TestSDKSubnetworkPatch(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	netClient := newNetClient(t, ts.URL, ts.Client())
	insertNet(t, ctx, netClient, "vpc-a")

	subClient := newSubClient(t, ts.URL, ts.Client())
	insertSubnet(t, ctx, subClient, testRegion, "sub-p", "10.20.0.0/16")

	got, err := subClient.Get(ctx, &computepb.GetSubnetworkRequest{
		Project: testProject, Region: testRegion, Subnetwork: "sub-p",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	fp := got.GetFingerprint()
	if fp == "" {
		t.Fatal("fingerprint empty, cannot patch")
	}

	// Stale fingerprint must be rejected (412 conditionNotMet).
	_, err = subClient.Patch(ctx, &computepb.PatchSubnetworkRequest{
		Project: testProject, Region: testRegion, Subnetwork: "sub-p",
		SubnetworkResource: &computepb.Subnetwork{
			Fingerprint:           ptrStr("c3RhbGVmcA=="),
			PrivateIpGoogleAccess: ptrBool(true),
		},
	})
	if err == nil {
		t.Fatal("patch with stale fingerprint succeeded, want 412")
	}

	// Valid fingerprint applies the change.
	op, err := subClient.Patch(ctx, &computepb.PatchSubnetworkRequest{
		Project: testProject, Region: testRegion, Subnetwork: "sub-p",
		SubnetworkResource: &computepb.Subnetwork{
			Fingerprint:           ptrStr(fp),
			PrivateIpGoogleAccess: ptrBool(true),
		},
	})
	if err != nil {
		t.Fatalf("subnet Patch: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("subnet Patch wait: %v", err)
	}

	after, err := subClient.Get(ctx, &computepb.GetSubnetworkRequest{
		Project: testProject, Region: testRegion, Subnetwork: "sub-p",
	})
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	if !after.GetPrivateIpGoogleAccess() {
		t.Error("privateIpGoogleAccess not applied by patch")
	}
}

// TestSDKFirewallPatch covers BUG3: PATCH firewall updates the allowed rules
// and preserves the rest of the spec.
func TestSDKFirewallPatch(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	client := newFwClient(t, ts.URL, ts.Client())

	insertOp, err := client.Insert(ctx, &computepb.InsertFirewallRequest{
		Project: testProject,
		FirewallResource: &computepb.Firewall{
			Name:         ptrStr("fw-patch"),
			Allowed:      []*computepb.Allowed{{IPProtocol: ptrStr("tcp"), Ports: []string{"80"}}},
			SourceRanges: []string{"10.0.0.0/8"},
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	patchOp, err := client.Patch(ctx, &computepb.PatchFirewallRequest{
		Project: testProject, Firewall: "fw-patch",
		FirewallResource: &computepb.Firewall{
			Allowed: []*computepb.Allowed{{IPProtocol: ptrStr("tcp"), Ports: []string{"443"}}},
		},
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if err := patchOp.Wait(ctx); err != nil {
		t.Fatalf("Patch wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetFirewallRequest{Project: testProject, Firewall: "fw-patch"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	allowed := got.GetAllowed()
	if len(allowed) != 1 || len(allowed[0].GetPorts()) != 1 || allowed[0].GetPorts()[0] != "443" {
		t.Fatalf("patched allowed not reflected: %+v", allowed)
	}

	// Untouched fields survive the merge patch.
	if len(got.GetSourceRanges()) != 1 || got.GetSourceRanges()[0] != "10.0.0.0/8" {
		t.Errorf("sourceRanges lost on patch: %v", got.GetSourceRanges())
	}
}

// TestSDKFirewallEgressAdvancedFields covers BUG4/BUG5: EGRESS + advanced
// firewall fields round-trip, and a minimal firewall reads back with the GCP
// defaults (direction INGRESS, priority 1000).
func TestSDKFirewallEgressAdvancedFields(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	client := newFwClient(t, ts.URL, ts.Client())

	egressOp, err := client.Insert(ctx, &computepb.InsertFirewallRequest{
		Project: testProject,
		FirewallResource: &computepb.Firewall{
			Name:              ptrStr("fw-egress"),
			Direction:         ptrStr("EGRESS"),
			Denied:            []*computepb.Denied{{IPProtocol: ptrStr("tcp"), Ports: []string{"25"}}},
			DestinationRanges: []string{"0.0.0.0/0"},
			SourceTags:        []string{"web"},
			Disabled:          ptrBool(true),
		},
	})
	if err != nil {
		t.Fatalf("Insert egress: %v", err)
	}

	if err := egressOp.Wait(ctx); err != nil {
		t.Fatalf("Insert egress wait: %v", err)
	}

	eg, err := client.Get(ctx, &computepb.GetFirewallRequest{Project: testProject, Firewall: "fw-egress"})
	if err != nil {
		t.Fatalf("Get egress: %v", err)
	}

	if eg.GetDirection() != "EGRESS" {
		t.Errorf("direction=%q want EGRESS", eg.GetDirection())
	}

	if len(eg.GetDestinationRanges()) != 1 || eg.GetDestinationRanges()[0] != "0.0.0.0/0" {
		t.Errorf("destinationRanges=%v", eg.GetDestinationRanges())
	}

	if len(eg.GetSourceTags()) != 1 || eg.GetSourceTags()[0] != "web" {
		t.Errorf("sourceTags=%v", eg.GetSourceTags())
	}

	if !eg.GetDisabled() {
		t.Error("disabled did not round-trip")
	}

	// A minimal firewall reads back with GCP defaults.
	minOp, err := client.Insert(ctx, &computepb.InsertFirewallRequest{
		Project: testProject,
		FirewallResource: &computepb.Firewall{
			Name:    ptrStr("fw-min"),
			Allowed: []*computepb.Allowed{{IPProtocol: ptrStr("tcp"), Ports: []string{"22"}}},
		},
	})
	if err != nil {
		t.Fatalf("Insert min: %v", err)
	}

	if err := minOp.Wait(ctx); err != nil {
		t.Fatalf("Insert min wait: %v", err)
	}

	mn, err := client.Get(ctx, &computepb.GetFirewallRequest{Project: testProject, Firewall: "fw-min"})
	if err != nil {
		t.Fatalf("Get min: %v", err)
	}

	if mn.GetDirection() != "INGRESS" {
		t.Errorf("default direction=%q want INGRESS", mn.GetDirection())
	}

	if mn.GetPriority() != 1000 {
		t.Errorf("default priority=%d want 1000", mn.GetPriority())
	}
}
