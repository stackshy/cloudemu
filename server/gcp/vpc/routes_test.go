package vpc_test

import (
	"context"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

// TestSDKRouteRoundTrip covers the compute-networking finding that the Routes
// API was entirely unrouted (501). Insert/Get/List/Delete must now work.
func TestSDKRouteRoundTrip(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewRoutesRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewRoutesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	insertOp, err := client.Insert(ctx, &computepb.InsertRouteRequest{
		Project: testProject,
		RouteResource: &computepb.Route{
			Name:           ptrStr("route-1"),
			Network:        ptrStr("projects/" + testProject + "/global/networks/default"),
			DestRange:      ptrStr("0.0.0.0/0"),
			NextHopGateway: ptrStr("projects/" + testProject + "/global/gateways/default-internet-gateway"),
			Priority:       func() *uint32 { p := uint32(1000); return &p }(),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetRouteRequest{Project: testProject, Route: "route-1"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetName() != "route-1" {
		t.Errorf("name=%s want route-1", got.GetName())
	}

	if got.GetDestRange() != "0.0.0.0/0" {
		t.Errorf("destRange=%s want 0.0.0.0/0", got.GetDestRange())
	}

	if got.GetKind() != "compute#route" || got.GetSelfLink() == "" {
		t.Errorf("kind=%s selfLink=%s want populated", got.GetKind(), got.GetSelfLink())
	}

	if got.GetCreationTimestamp() == "" {
		t.Error("creationTimestamp empty, want RFC3339")
	}

	it := client.List(ctx, &computepb.ListRoutesRequest{Project: testProject})

	found := false

	for {
		rt, iterErr := it.Next()
		if iterErr != nil {
			break
		}

		if rt.GetName() == "route-1" {
			found = true
		}
	}

	if !found {
		t.Error("List did not return route-1")
	}

	delOp, err := client.Delete(ctx, &computepb.DeleteRouteRequest{Project: testProject, Route: "route-1"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Errorf("Delete wait: %v", err)
	}

	if _, err := client.Get(ctx, &computepb.GetRouteRequest{Project: testProject, Route: "route-1"}); err == nil {
		t.Error("Get after Delete: want error, got nil")
	}
}
