package vpc_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

func newNetClient(t *testing.T, url string, client *http.Client) *gcpcompute.NetworksClient {
	t.Helper()

	c, err := gcpcompute.NewNetworksRESTClient(context.Background(),
		option.WithEndpoint(url), option.WithoutAuthentication(), option.WithHTTPClient(client))
	if err != nil {
		t.Fatalf("NewNetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func mustInsertNetwork(t *testing.T, ctx context.Context, c *gcpcompute.NetworksClient, n *computepb.Network) {
	t.Helper()

	op, err := c.Insert(ctx, &computepb.InsertNetworkRequest{Project: testProject, NetworkResource: n})
	if err != nil {
		t.Fatalf("network Insert %s: %v", n.GetName(), err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("network Insert wait %s: %v", n.GetName(), err)
	}
}

// TestSDKNetworkCreationTimestamp covers the finding that creationTimestamp was
// empty on every networking resource (checked here for a network).
func TestSDKNetworkCreationTimestamp(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()
	c := newNetClient(t, ts.URL, ts.Client())

	mustInsertNetwork(t, ctx, c, &computepb.Network{Name: ptrStr("ts-net")})

	got, err := c.Get(ctx, &computepb.GetNetworkRequest{Project: testProject, Network: "ts-net"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetCreationTimestamp() == "" {
		t.Error("creationTimestamp empty, want RFC3339")
	}
}

// TestSDKCustomModeNetworkOmitsIPv4Range covers the finding that IPv4Range was
// always defaulted to 10.0.0.0/16 and always emitted, so a custom-mode network
// wrongly read back as legacy. A modern (auto/custom) network must omit it; a
// legacy network (explicit IPv4Range) must retain it.
func TestSDKCustomModeNetworkOmitsIPv4Range(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()
	c := newNetClient(t, ts.URL, ts.Client())

	falsePtr := func() *bool { b := false; return &b }

	mustInsertNetwork(t, ctx, c, &computepb.Network{
		Name:                  ptrStr("custom-net"),
		AutoCreateSubnetworks: falsePtr(),
	})

	custom, err := c.Get(ctx, &computepb.GetNetworkRequest{Project: testProject, Network: "custom-net"})
	if err != nil {
		t.Fatalf("Get custom-net: %v", err)
	}

	if custom.GetIPv4Range() != "" {
		t.Errorf("custom-mode IPv4Range=%q want empty", custom.GetIPv4Range())
	}

	mustInsertNetwork(t, ctx, c, &computepb.Network{
		Name:      ptrStr("legacy-net"),
		IPv4Range: ptrStr("192.168.0.0/16"),
	})

	legacy, err := c.Get(ctx, &computepb.GetNetworkRequest{Project: testProject, Network: "legacy-net"})
	if err != nil {
		t.Fatalf("Get legacy-net: %v", err)
	}

	if legacy.GetIPv4Range() != "192.168.0.0/16" {
		t.Errorf("legacy IPv4Range=%q want 192.168.0.0/16", legacy.GetIPv4Range())
	}
}

// TestSDKNetworkDeleteInUse covers the finding that deleting a network with a
// live subnetwork succeeded and orphaned the subnet. It must fail until the
// subnet is removed.
func TestSDKNetworkDeleteInUse(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()
	c := newNetClient(t, ts.URL, ts.Client())

	mustInsertNetwork(t, ctx, c, &computepb.Network{
		Name:                  ptrStr("vpc-inuse"),
		AutoCreateSubnetworks: func() *bool { b := false; return &b }(),
	})

	subClient, err := gcpcompute.NewSubnetworksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewSubnetworksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = subClient.Close() })

	subOp, err := subClient.Insert(ctx, &computepb.InsertSubnetworkRequest{
		Project: testProject,
		Region:  "us-central1",
		SubnetworkResource: &computepb.Subnetwork{
			Name:        ptrStr("sub-inuse"),
			Network:     ptrStr("projects/" + testProject + "/global/networks/vpc-inuse"),
			IpCidrRange: ptrStr("10.5.0.0/16"),
		},
	})
	if err != nil {
		t.Fatalf("subnet Insert: %v", err)
	}

	if err := subOp.Wait(ctx); err != nil {
		t.Fatalf("subnet Insert wait: %v", err)
	}

	_, delErr := c.Delete(ctx, &computepb.DeleteNetworkRequest{Project: testProject, Network: "vpc-inuse"})
	if delErr == nil {
		t.Fatal("Delete of in-use network: want error, got nil")
	}

	// The message must name the real resources, not internal driver ids.
	if msg := delErr.Error(); !strings.Contains(msg, "vpc-inuse") || !strings.Contains(msg, "sub-inuse") {
		t.Errorf("in-use message %q must contain real names vpc-inuse and sub-inuse", msg)
	}

	// The subnet must still exist (delete rejected, not partially applied).
	if _, err := subClient.Get(ctx, &computepb.GetSubnetworkRequest{
		Project: testProject, Region: "us-central1", Subnetwork: "sub-inuse",
	}); err != nil {
		t.Errorf("subnet Get after rejected network delete: %v", err)
	}
}

// TestSDKNetworkDeleteBlockedByFirewall verifies the provider-layer guard, now
// authoritative for every caller, also blocks a network delete over the wire
// while a firewall rule still references the network — real GCP answers
// resourceInUseByAnotherResource. Deleting the firewall unblocks the network.
func TestSDKNetworkDeleteBlockedByFirewall(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()
	c := newNetClient(t, ts.URL, ts.Client())

	mustInsertNetwork(t, ctx, c, &computepb.Network{
		Name:                  ptrStr("vpc-fw"),
		AutoCreateSubnetworks: func() *bool { b := false; return &b }(),
	})

	fwClient, err := gcpcompute.NewFirewallsRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewFirewallsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = fwClient.Close() })

	fwOp, err := fwClient.Insert(ctx, &computepb.InsertFirewallRequest{
		Project: testProject,
		FirewallResource: &computepb.Firewall{
			Name:         ptrStr("fw-block"),
			Network:      ptrStr("projects/" + testProject + "/global/networks/vpc-fw"),
			Allowed:      []*computepb.Allowed{{IPProtocol: ptrStr("tcp"), Ports: []string{"22"}}},
			SourceRanges: []string{"0.0.0.0/0"},
		},
	})
	if err != nil {
		t.Fatalf("firewall Insert: %v", err)
	}

	if err := fwOp.Wait(ctx); err != nil {
		t.Fatalf("firewall Insert wait: %v", err)
	}

	_, delErr := c.Delete(ctx, &computepb.DeleteNetworkRequest{Project: testProject, Network: "vpc-fw"})
	if delErr == nil {
		t.Fatal("Delete of network with firewall: want error, got nil")
	}

	// The message must name the real resources the caller typed, not the
	// internal driver ids the provider error carries.
	if msg := delErr.Error(); !strings.Contains(msg, "vpc-fw") || !strings.Contains(msg, "fw-block") {
		t.Errorf("in-use message %q must contain real names vpc-fw and fw-block", msg)
	}

	// Remove the firewall, then the network deletes cleanly.
	delFwOp, err := fwClient.Delete(ctx, &computepb.DeleteFirewallRequest{Project: testProject, Firewall: "fw-block"})
	if err != nil {
		t.Fatalf("firewall Delete: %v", err)
	}

	if err := delFwOp.Wait(ctx); err != nil {
		t.Fatalf("firewall Delete wait: %v", err)
	}

	delOp, err := c.Delete(ctx, &computepb.DeleteNetworkRequest{Project: testProject, Network: "vpc-fw"})
	if err != nil {
		t.Fatalf("network Delete after firewall removed: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("network Delete wait: %v", err)
	}
}

// TestNetworkListPagination covers the finding that maxResults/pageToken were
// ignored and nextPageToken never set. Verified over the wire since the SDK
// iterator transparently follows pages.
func TestNetworkListPagination(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()
	c := newNetClient(t, ts.URL, ts.Client())

	for _, name := range []string{"pg-a", "pg-b", "pg-c"} {
		mustInsertNetwork(t, ctx, c, &computepb.Network{Name: ptrStr(name)})
	}

	var page struct {
		Items         []json.RawMessage `json:"items"`
		NextPageToken string            `json:"nextPageToken"`
	}

	url := ts.URL + "/compute/v1/projects/" + testProject + "/global/networks?maxResults=1"

	resp, err := ts.Client().Get(url)
	if err != nil {
		t.Fatalf("list GET: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(page.Items) != 1 {
		t.Errorf("page items=%d want 1 (maxResults ignored)", len(page.Items))
	}

	if page.NextPageToken == "" {
		t.Error("nextPageToken empty, want a continuation token")
	}
}

// TestComputeUnmatchedPathReturnsJSONError covers the finding that an
// unimplemented /compute/v1 path returned a bare-text 501 with the wrong
// Content-Type instead of a GCP JSON error envelope.
func TestComputeUnmatchedPathReturnsJSONError(t *testing.T) {
	ts := newGCPNetServer(t)

	url := ts.URL + "/compute/v1/projects/" + testProject + "/global/targetHttpProxies"

	resp, err := ts.Client().Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status=%d want 501", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type=%q want application/json", ct)
	}

	var env struct {
		Error struct {
			Code    int    `json:"code"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}

	if env.Error.Code != http.StatusNotImplemented || env.Error.Status == "" {
		t.Errorf("envelope code=%d status=%q want 501/non-empty", env.Error.Code, env.Error.Status)
	}
}
