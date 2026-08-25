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
