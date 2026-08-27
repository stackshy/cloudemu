package vpc_test

import (
	"context"
	"net/http/httptest"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const (
	testProject = "p1"
	testRegion  = "us-central1"
)

func ptrStr(s string) *string { return &s }
func ptrInt32(i int32) *int32 { return &i }

func newGCPNetServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewGCP()
	// Register Compute too so the shared operations-polling endpoint is wired
	// up — networks return Operation envelopes the SDK polls there.
	srv := gcpserver.New(gcpserver.Drivers{Networking: cloudP.VPC, Compute: cloudP.GCE})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func TestSDKNetworkRoundTrip(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewNetworksRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewNetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	insertOp, err := client.Insert(ctx, &computepb.InsertNetworkRequest{
		Project: testProject,
		NetworkResource: &computepb.Network{
			Name:                  ptrStr("net-1"),
			AutoCreateSubnetworks: func() *bool { b := false; return &b }(),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetNetworkRequest{
		Project: testProject, Network: "net-1",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetName() != "net-1" {
		t.Errorf("name=%s want net-1", got.GetName())
	}

	it := client.List(ctx, &computepb.ListNetworksRequest{Project: testProject})

	found := false
	for {
		n, err := it.Next()
		if err != nil {
			break
		}

		if n.GetName() == "net-1" {
			found = true
		}
	}

	if !found {
		t.Error("List did not return net-1")
	}

	delOp, err := client.Delete(ctx, &computepb.DeleteNetworkRequest{
		Project: testProject, Network: "net-1",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Errorf("Delete wait: %v", err)
	}
}

// TestSDKNetworkRoutingModeMtuDescription covers the HIGH finding that a
// network's description, routingConfig.routingMode and mtu were dropped on
// insert (Terraform google_compute_network sees a perpetual plan diff).
func TestSDKNetworkRoutingModeMtuDescription(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewNetworksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewNetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	op, err := client.Insert(ctx, &computepb.InsertNetworkRequest{
		Project: testProject,
		NetworkResource: &computepb.Network{
			Name:                  ptrStr("net-cfg"),
			Description:           ptrStr("app network"),
			AutoCreateSubnetworks: func() *bool { b := false; return &b }(),
			RoutingConfig:         &computepb.NetworkRoutingConfig{RoutingMode: ptrStr("GLOBAL")},
			Mtu:                   ptrInt32(1500),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetNetworkRequest{Project: testProject, Network: "net-cfg"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetDescription() != "app network" {
		t.Errorf("description=%q want %q", got.GetDescription(), "app network")
	}

	if got.GetRoutingConfig().GetRoutingMode() != "GLOBAL" {
		t.Errorf("routingMode=%q want GLOBAL", got.GetRoutingConfig().GetRoutingMode())
	}

	if got.GetMtu() != 1500 {
		t.Errorf("mtu=%d want 1500", got.GetMtu())
	}
}

// TestSDKFirewallMissingNetworkRejected covers the MED finding that a firewall
// referencing an unknown network fabricated a phantom VPC that then leaked into
// networks.list. Real GCP rejects the insert with 404; no VPC is created.
func TestSDKFirewallMissingNetworkRejected(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	fwClient, err := gcpcompute.NewFirewallsRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewFirewallsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = fwClient.Close() })

	_, err = fwClient.Insert(ctx, &computepb.InsertFirewallRequest{
		Project: testProject,
		FirewallResource: &computepb.Firewall{
			Name:    ptrStr("fw-bad"),
			Network: ptrStr("projects/" + testProject + "/global/networks/does-not-exist"),
			Allowed: []*computepb.Allowed{{IPProtocol: ptrStr("tcp"), Ports: []string{"22"}}},
		},
	})
	if err == nil {
		t.Fatal("firewall Insert with missing network succeeded, want 404")
	}

	// No phantom network must have leaked into networks.list.
	netClient, err := gcpcompute.NewNetworksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewNetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = netClient.Close() })

	it := netClient.List(ctx, &computepb.ListNetworksRequest{Project: testProject})

	count := 0
	for {
		if _, err := it.Next(); err != nil {
			break
		}

		count++
	}

	if count != 0 {
		t.Errorf("networks.list returned %d networks, want 0 (phantom VPC leaked)", count)
	}
}

func TestSDKFirewallRoundTrip(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewFirewallsRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewFirewallsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	insertOp, err := client.Insert(ctx, &computepb.InsertFirewallRequest{
		Project: testProject,
		FirewallResource: &computepb.Firewall{
			Name: ptrStr("fw-1"),
			Allowed: []*computepb.Allowed{{
				IPProtocol: ptrStr("tcp"),
				Ports:      []string{"80", "443"},
			}},
			SourceRanges: []string{"10.0.0.0/8"},
			Direction:    ptrStr("INGRESS"),
			Priority:     ptrInt32(900),
			TargetTags:   []string{"web"},
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetFirewallRequest{
		Project: testProject, Firewall: "fw-1",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetName() != "fw-1" {
		t.Errorf("name=%s want fw-1", got.GetName())
	}

	// #321: firewall rules must round-trip, not read back empty.
	allowed := got.GetAllowed()
	if len(allowed) != 1 || allowed[0].GetIPProtocol() != "tcp" || len(allowed[0].GetPorts()) != 2 {
		t.Fatalf("allowed did not round-trip: %+v", allowed)
	}

	if len(got.GetSourceRanges()) != 1 || got.GetSourceRanges()[0] != "10.0.0.0/8" {
		t.Errorf("sourceRanges=%v", got.GetSourceRanges())
	}

	if got.GetDirection() != "INGRESS" || got.GetPriority() != 900 {
		t.Errorf("direction=%s priority=%d", got.GetDirection(), got.GetPriority())
	}

	if len(got.GetTargetTags()) != 1 || got.GetTargetTags()[0] != "web" {
		t.Errorf("targetTags=%v", got.GetTargetTags())
	}

	delOp, err := client.Delete(ctx, &computepb.DeleteFirewallRequest{
		Project: testProject, Firewall: "fw-1",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Errorf("Delete wait: %v", err)
	}
}
