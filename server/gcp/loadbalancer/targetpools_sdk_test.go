package loadbalancer_test

import (
	"context"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const testRegion = "us-central1"

// TestSDKGCPTargetPoolRoundTrip reproduces the [HIGH] finding that the
// (regional) targetPools collection was unrouted (501), breaking the classic
// network LB / google_compute_target_pool. Insert/Get/List/Delete must
// round-trip under the region scope.
func TestSDKGCPTargetPoolRoundTrip(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewTargetPoolsRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewTargetPoolsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	op, err := client.Insert(ctx, &computepb.InsertTargetPoolRequest{
		Project: testProject,
		Region:  testRegion,
		TargetPoolResource: &computepb.TargetPool{
			Name:            ptrStr("tp1"),
			SessionAffinity: ptrStr("CLIENT_IP"),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetTargetPoolRequest{Project: testProject, Region: testRegion, TargetPool: "tp1"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetName() != "tp1" {
		t.Fatalf("name = %q, want tp1", got.GetName())
	}

	if got.GetSessionAffinity() != "CLIENT_IP" {
		t.Errorf("sessionAffinity = %q, want CLIENT_IP", got.GetSessionAffinity())
	}

	if got.GetRegion() == "" {
		t.Error("region self-link empty on a regional resource")
	}

	var names []string

	it := client.List(ctx, &computepb.ListTargetPoolsRequest{Project: testProject, Region: testRegion})

	for {
		tp, iErr := it.Next()
		if iErr == iterator.Done {
			break
		}

		if iErr != nil {
			t.Fatalf("List: %v", iErr)
		}

		names = append(names, tp.GetName())
	}

	if len(names) != 1 || names[0] != "tp1" {
		t.Fatalf("list = %v, want [tp1]", names)
	}

	delOp, err := client.Delete(ctx, &computepb.DeleteTargetPoolRequest{Project: testProject, Region: testRegion, TargetPool: "tp1"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("Delete wait: %v", err)
	}

	if _, err := client.Get(ctx,
		&computepb.GetTargetPoolRequest{Project: testProject, Region: testRegion, TargetPool: "tp1"}); err == nil {
		t.Fatal("Get after delete: want error, got nil")
	}
}
