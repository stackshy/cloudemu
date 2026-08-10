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
