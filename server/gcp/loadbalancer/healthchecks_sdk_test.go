package loadbalancer_test

import (
	"context"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// TestSDKGCPHealthCheckRoundTrip reproduces the [BLOCKER] finding that the
// healthChecks collection was entirely unrouted (501), so no L4/L7 LB could be
// provisioned. Insert/Get/List/Delete must now round-trip.
func TestSDKGCPHealthCheckRoundTrip(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewHealthChecksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewHealthChecksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	op, err := client.Insert(ctx, &computepb.InsertHealthCheckRequest{
		Project: testProject,
		HealthCheckResource: &computepb.HealthCheck{
			Name: ptrStr("hc1"),
			Type: ptrStr("HTTP"),
			HttpHealthCheck: &computepb.HTTPHealthCheck{
				Port:        ptrI32(80),
				RequestPath: ptrStr("/healthz"),
			},
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetHealthCheckRequest{Project: testProject, HealthCheck: "hc1"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetName() != "hc1" {
		t.Fatalf("name = %q, want hc1", got.GetName())
	}

	if got.GetType() != "HTTP" {
		t.Errorf("type = %q, want HTTP", got.GetType())
	}

	if got.GetHttpHealthCheck().GetRequestPath() != "/healthz" {
		t.Errorf("requestPath = %q, want /healthz", got.GetHttpHealthCheck().GetRequestPath())
	}

	if got.GetHttpHealthCheck().GetPort() != 80 {
		t.Errorf("port = %d, want 80", got.GetHttpHealthCheck().GetPort())
	}

	if got.GetCreationTimestamp() == "" {
		t.Error("creationTimestamp empty")
	}

	// List.
	var names []string

	it := client.List(ctx, &computepb.ListHealthChecksRequest{Project: testProject})

	for {
		hc, iErr := it.Next()
		if iErr == iterator.Done {
			break
		}

		if iErr != nil {
			t.Fatalf("List: %v", iErr)
		}

		names = append(names, hc.GetName())
	}

	if len(names) != 1 || names[0] != "hc1" {
		t.Fatalf("list = %v, want [hc1]", names)
	}

	// Duplicate insert → 409.
	dupOp, err := client.Insert(ctx, &computepb.InsertHealthCheckRequest{
		Project:             testProject,
		HealthCheckResource: &computepb.HealthCheck{Name: ptrStr("hc1"), Type: ptrStr("TCP")},
	})
	if err == nil {
		err = dupOp.Wait(ctx)
	}

	if err == nil {
		t.Error("duplicate healthCheck insert: want error, got nil")
	}

	// Delete then Get → not found.
	delOp, err := client.Delete(ctx, &computepb.DeleteHealthCheckRequest{Project: testProject, HealthCheck: "hc1"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("Delete wait: %v", err)
	}

	if _, err := client.Get(ctx, &computepb.GetHealthCheckRequest{Project: testProject, HealthCheck: "hc1"}); err == nil {
		t.Fatal("Get after delete: want error, got nil")
	}
}

// TestSDKGCPHealthCheckPatch covers the missing PATCH verb: healthChecks.patch
// previously 405'd, breaking terraform apply on any change. The patch must merge
// onto the stored resource and be visible on the next get.
func TestSDKGCPHealthCheckPatch(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewHealthChecksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewHealthChecksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	insertOp, err := client.Insert(ctx, &computepb.InsertHealthCheckRequest{
		Project: testProject,
		HealthCheckResource: &computepb.HealthCheck{
			Name:             ptrStr("hc-patch"),
			Type:             ptrStr("HTTP"),
			CheckIntervalSec: ptrI32(5),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	patchOp, err := client.Patch(ctx, &computepb.PatchHealthCheckRequest{
		Project:             testProject,
		HealthCheck:         "hc-patch",
		HealthCheckResource: &computepb.HealthCheck{CheckIntervalSec: ptrI32(15)},
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if err := patchOp.Wait(ctx); err != nil {
		t.Fatalf("Patch wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetHealthCheckRequest{Project: testProject, HealthCheck: "hc-patch"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetCheckIntervalSec() != 15 {
		t.Errorf("checkIntervalSec = %d, want 15 (patch not applied)", got.GetCheckIntervalSec())
	}

	// A field omitted from the patch body must survive the merge.
	if got.GetType() != "HTTP" {
		t.Errorf("type = %q, want HTTP (patch clobbered an omitted field)", got.GetType())
	}
}
