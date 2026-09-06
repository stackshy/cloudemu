package vpc_test

import (
	"context"
	"net/http/httptest"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

func newRoutersClient(t *testing.T, ts *httptest.Server) *gcpcompute.RoutersClient {
	t.Helper()

	ctx := context.Background()

	client, err := gcpcompute.NewRoutersRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewRoutersRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

// TestSDKRouterRoundTrip covers a region-scoped router insert/get: the server
// must stamp kind, selfLink, region and creationTimestamp (the Terraform
// google_compute_router provider dereferences selfLink unconditionally and
// crashes when it is absent) and round-trip the nested BGP block.
func TestSDKRouterRoundTrip(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()
	client := newRoutersClient(t, ts)

	insertOp, err := client.Insert(ctx, &computepb.InsertRouterRequest{
		Project: testProject,
		Region:  testRegion,
		RouterResource: &computepb.Router{
			Name:    ptrStr("r1"),
			Network: ptrStr("projects/" + testProject + "/global/networks/default"),
			Bgp: &computepb.RouterBgp{
				Asn:           func() *uint32 { a := uint32(64514); return &a }(),
				AdvertiseMode: ptrStr("DEFAULT"),
			},
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetRouterRequest{
		Project: testProject, Region: testRegion, Router: "r1",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetName() != "r1" {
		t.Errorf("name=%s want r1", got.GetName())
	}

	if got.GetKind() != "compute#router" || got.GetSelfLink() == "" {
		t.Errorf("kind=%q selfLink=%q want compute#router and non-empty", got.GetKind(), got.GetSelfLink())
	}

	if got.GetCreationTimestamp() == "" {
		t.Error("creationTimestamp is empty")
	}

	if got.GetBgp().GetAsn() != 64514 {
		t.Errorf("bgp.asn=%d want 64514", got.GetBgp().GetAsn())
	}
}

// TestSDKRouterNatPartialPatchPreservesBgp is the core regression: Terraform's
// google_compute_router_nat adds NAT with a partial patch carrying only nats[]
// (no name/network/bgp). The server must merge that onto the stored router so
// bgp survives, and must fill the NAT idle-timeout defaults real Compute stamps.
func TestSDKRouterNatPartialPatchPreservesBgp(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()
	client := newRoutersClient(t, ts)

	insertOp, err := client.Insert(ctx, &computepb.InsertRouterRequest{
		Project: testProject,
		Region:  testRegion,
		RouterResource: &computepb.Router{
			Name:    ptrStr("r2"),
			Network: ptrStr("projects/" + testProject + "/global/networks/default"),
			Bgp: &computepb.RouterBgp{
				Asn:           func() *uint32 { a := uint32(64515); return &a }(),
				AdvertiseMode: ptrStr("CUSTOM"),
			},
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	// Partial patch: only name + nats, exactly what google_compute_router_nat
	// sends. bgp/network are intentionally omitted.
	patchOp, err := client.Patch(ctx, &computepb.PatchRouterRequest{
		Project: testProject,
		Region:  testRegion,
		Router:  "r2",
		RouterResource: &computepb.Router{
			Name: ptrStr("r2"),
			Nats: []*computepb.RouterNat{{
				Name:                          ptrStr("nat1"),
				NatIpAllocateOption:           ptrStr("AUTO_ONLY"),
				SourceSubnetworkIpRangesToNat: ptrStr("ALL_SUBNETWORKS_ALL_IP_RANGES"),
			}},
		},
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if err := patchOp.Wait(ctx); err != nil {
		t.Fatalf("Patch wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetRouterRequest{
		Project: testProject, Region: testRegion, Router: "r2",
	})
	if err != nil {
		t.Fatalf("Get after patch: %v", err)
	}

	// bgp and network must survive the partial patch (the regression).
	if got.GetBgp().GetAsn() != 64515 {
		t.Errorf("bgp.asn=%d want 64515 (bgp dropped by partial patch)", got.GetBgp().GetAsn())
	}

	if got.GetNetwork() == "" {
		t.Error("network dropped by partial patch")
	}

	if len(got.GetNats()) != 1 || got.GetNats()[0].GetName() != "nat1" {
		t.Fatalf("nats=%v want single nat1", got.GetNats())
	}

	nat := got.GetNats()[0]
	if nat.GetUdpIdleTimeoutSec() != 30 || nat.GetTcpEstablishedIdleTimeoutSec() != 1200 ||
		nat.GetTcpTransitoryIdleTimeoutSec() != 30 || nat.GetIcmpIdleTimeoutSec() != 30 {
		t.Errorf("NAT timeout defaults not applied: udp=%d tcpEst=%d tcpTrans=%d icmp=%d",
			nat.GetUdpIdleTimeoutSec(), nat.GetTcpEstablishedIdleTimeoutSec(),
			nat.GetTcpTransitoryIdleTimeoutSec(), nat.GetIcmpIdleTimeoutSec())
	}

	// creationTimestamp must be stable across the patch (not re-stamped).
	if got.GetCreationTimestamp() == "" {
		t.Error("creationTimestamp lost after patch")
	}
}
