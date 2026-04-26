package compute_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

// newDisksSDKClient builds a real google-cloud-go DisksRESTClient pointing
// at the given test server.
func newDisksSDKClient(t *testing.T, ts *httptest.Server) *gcpcompute.DisksClient {
	t.Helper()

	ctx := context.Background()

	client, err := gcpcompute.NewDisksRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewDisksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

// TestSDKDiskRoundTrip drives the disk lifecycle (insert → get → list →
// delete) using a real cloud.google.com/go DisksClient.
func TestSDKDiskRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Compute: cloudP.GCE})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client := newDisksSDKClient(t, ts)
	ctx := context.Background()

	insertOp, err := client.Insert(ctx, &computepb.InsertDiskRequest{
		Project: testProject,
		Zone:    testZone,
		DiskResource: &computepb.Disk{
			Name:   ptrStr("data-disk-1"),
			SizeGb: ptrInt64(64),
			Type:   ptrStr("zones/" + testZone + "/diskTypes/pd-standard"),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetDiskRequest{
		Project: testProject, Zone: testZone, Disk: "data-disk-1",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetName() != "data-disk-1" {
		t.Errorf("name=%s want data-disk-1", got.GetName())
	}

	if got.GetSizeGb() != 64 {
		t.Errorf("sizeGb=%d want 64", got.GetSizeGb())
	}

	if !strings.HasSuffix(got.GetType(), "/diskTypes/pd-standard") {
		t.Errorf("type=%s", got.GetType())
	}

	it := client.List(ctx, &computepb.ListDisksRequest{Project: testProject, Zone: testZone})

	found := false
	for {
		d, err := it.Next()
		if err != nil {
			break
		}

		if d.GetName() == "data-disk-1" {
			found = true
		}
	}

	if !found {
		t.Error("List did not return data-disk-1")
	}

	delOp, err := client.Delete(ctx, &computepb.DeleteDiskRequest{
		Project: testProject, Zone: testZone, Disk: "data-disk-1",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Errorf("Delete wait: %v", err)
	}
}

func ptrInt64(v int64) *int64 { return &v }
