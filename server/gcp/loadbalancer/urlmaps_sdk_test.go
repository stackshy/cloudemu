package loadbalancer_test

import (
	"context"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// TestSDKGCPURLMapRoundTrip reproduces the [HIGH] finding that the urlMaps
// collection was unrouted (501), breaking every external HTTP(S) LB /
// google_compute_url_map. Insert/Get/List/Delete must round-trip.
func TestSDKGCPURLMapRoundTrip(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewUrlMapsRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewUrlMapsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	bsClient := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))
	insertBS(ctx, t, bsClient, "web-bs")

	defaultSvc := "projects/" + testProject + "/global/backendServices/web-bs"

	op, err := client.Insert(ctx, &computepb.InsertUrlMapRequest{
		Project: testProject,
		UrlMapResource: &computepb.UrlMap{
			Name:           ptrStr("web-map"),
			DefaultService: ptrStr(defaultSvc),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetUrlMapRequest{Project: testProject, UrlMap: "web-map"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetName() != "web-map" {
		t.Fatalf("name = %q, want web-map", got.GetName())
	}

	if got.GetDefaultService() != defaultSvc {
		t.Errorf("defaultService = %q, want %q", got.GetDefaultService(), defaultSvc)
	}

	if got.GetSelfLink() == "" {
		t.Error("selfLink empty")
	}

	var names []string

	it := client.List(ctx, &computepb.ListUrlMapsRequest{Project: testProject})

	for {
		um, iErr := it.Next()
		if iErr == iterator.Done {
			break
		}

		if iErr != nil {
			t.Fatalf("List: %v", iErr)
		}

		names = append(names, um.GetName())
	}

	if len(names) != 1 || names[0] != "web-map" {
		t.Fatalf("list = %v, want [web-map]", names)
	}

	delOp, err := client.Delete(ctx, &computepb.DeleteUrlMapRequest{Project: testProject, UrlMap: "web-map"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("Delete wait: %v", err)
	}

	if _, err := client.Get(ctx, &computepb.GetUrlMapRequest{Project: testProject, UrlMap: "web-map"}); err == nil {
		t.Fatal("Get after delete: want error, got nil")
	}
}

// TestSDKGCPURLMapUpdate covers the missing UPDATE verb: urlMaps.update
// previously 405'd, so google_compute_url_map could never change its routing.
// The update must replace the stored resource and be visible on the next get.
func TestSDKGCPURLMapUpdate(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	client, err := gcpcompute.NewUrlMapsRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewUrlMapsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	bsClient := newBackendServicesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))
	insertBS(ctx, t, bsClient, "bs-a")
	insertBS(ctx, t, bsClient, "bs-b")

	svcA := "projects/" + testProject + "/global/backendServices/bs-a"
	svcB := "projects/" + testProject + "/global/backendServices/bs-b"

	insertOp, err := client.Insert(ctx, &computepb.InsertUrlMapRequest{
		Project:        testProject,
		UrlMapResource: &computepb.UrlMap{Name: ptrStr("edit-map"), DefaultService: ptrStr(svcA)},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	updOp, err := client.Update(ctx, &computepb.UpdateUrlMapRequest{
		Project:        testProject,
		UrlMap:         "edit-map",
		UrlMapResource: &computepb.UrlMap{Name: ptrStr("edit-map"), DefaultService: ptrStr(svcB)},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if err := updOp.Wait(ctx); err != nil {
		t.Fatalf("Update wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetUrlMapRequest{Project: testProject, UrlMap: "edit-map"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetDefaultService() != svcB {
		t.Errorf("defaultService = %q, want %q (update not applied)", got.GetDefaultService(), svcB)
	}
}
