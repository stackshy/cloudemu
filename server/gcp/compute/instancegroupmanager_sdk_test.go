package compute_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newMIGSDKClient builds a real google-cloud-go InstanceGroupManagersRESTClient
// pointing at the given test server.
func newMIGSDKClient(t *testing.T, ts *httptest.Server) *gcpcompute.InstanceGroupManagersClient {
	t.Helper()

	client, err := gcpcompute.NewInstanceGroupManagersRESTClient(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewInstanceGroupManagersRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

// TestSDKInstanceGroupManagerRoundTrip drives the zonal MIG lifecycle (insert →
// get → list → resize → delete) with a real cloud.google.com/go client so the
// wire shapes are SDK-compatible. targetSize is the load-bearing field: the
// Terraform google provider reads a GKE node pool's node_count off it.
func TestSDKInstanceGroupManagerRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Compute: cloudP.GCE})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client := newMIGSDKClient(t, ts)
	ctx := context.Background()

	insertOp, err := client.Insert(ctx, &computepb.InsertInstanceGroupManagerRequest{
		Project: testProject,
		Zone:    testZone,
		InstanceGroupManagerResource: &computepb.InstanceGroupManager{
			Name:             ptrStr("web-mig"),
			BaseInstanceName: ptrStr("web"),
			TargetSize:       ptrInt32(3),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetInstanceGroupManagerRequest{
		Project: testProject, Zone: testZone, InstanceGroupManager: "web-mig",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetName() != "web-mig" {
		t.Errorf("name=%s want web-mig", got.GetName())
	}

	if got.GetTargetSize() != 3 {
		t.Errorf("targetSize=%d want 3", got.GetTargetSize())
	}

	if got.GetBaseInstanceName() != "web" {
		t.Errorf("baseInstanceName=%s want web", got.GetBaseInstanceName())
	}

	if !strings.HasSuffix(got.GetInstanceGroup(), "/instanceGroups/web-mig") {
		t.Errorf("instanceGroup=%s want .../instanceGroups/web-mig", got.GetInstanceGroup())
	}

	if got.GetStatus() == nil || !got.GetStatus().GetIsStable() {
		t.Error("status.isStable=false want true")
	}

	it := client.List(ctx, &computepb.ListInstanceGroupManagersRequest{Project: testProject, Zone: testZone})

	found := false

	for {
		m, err := it.Next()
		if err != nil {
			break
		}

		if m.GetName() == "web-mig" {
			found = true
		}
	}

	if !found {
		t.Error("List did not return web-mig")
	}

	resizeOp, err := client.Resize(ctx, &computepb.ResizeInstanceGroupManagerRequest{
		Project: testProject, Zone: testZone, InstanceGroupManager: "web-mig", Size: 5,
	})
	if err != nil {
		t.Fatalf("Resize: %v", err)
	}

	if err := resizeOp.Wait(ctx); err != nil {
		t.Fatalf("Resize wait: %v", err)
	}

	after, err := client.Get(ctx, &computepb.GetInstanceGroupManagerRequest{
		Project: testProject, Zone: testZone, InstanceGroupManager: "web-mig",
	})
	if err != nil {
		t.Fatalf("Get after resize: %v", err)
	}

	if after.GetTargetSize() != 5 {
		t.Errorf("targetSize after resize=%d want 5", after.GetTargetSize())
	}

	delOp, err := client.Delete(ctx, &computepb.DeleteInstanceGroupManagerRequest{
		Project: testProject, Zone: testZone, InstanceGroupManager: "web-mig",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Errorf("Delete wait: %v", err)
	}

	if _, err := client.Get(ctx, &computepb.GetInstanceGroupManagerRequest{
		Project: testProject, Zone: testZone, InstanceGroupManager: "web-mig",
	}); err == nil {
		t.Error("Get after delete: expected NotFound")
	}
}

// TestSDKInstanceGroupManagerAggregatedList proves aggregatedList groups MIGs by
// their zones/{zone} scope.
func TestSDKInstanceGroupManagerAggregatedList(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Compute: cloudP.GCE})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client := newMIGSDKClient(t, ts)
	ctx := context.Background()

	insertOp, err := client.Insert(ctx, &computepb.InsertInstanceGroupManagerRequest{
		Project: testProject,
		Zone:    testZone,
		InstanceGroupManagerResource: &computepb.InstanceGroupManager{
			Name:       ptrStr("agg-mig"),
			TargetSize: ptrInt32(1),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	it := client.AggregatedList(ctx, &computepb.AggregatedListInstanceGroupManagersRequest{Project: testProject})

	found := false

	for {
		pair, err := it.Next()
		if err != nil {
			break
		}

		for _, m := range pair.Value.GetInstanceGroupManagers() {
			if m.GetName() == "agg-mig" && pair.Key == "zones/"+testZone {
				found = true
			}
		}
	}

	if !found {
		t.Errorf("aggregatedList did not return agg-mig under zones/%s", testZone)
	}
}

func ptrInt32(v int32) *int32 { return &v }
