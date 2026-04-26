package compute_test

import (
	"context"
	"net/http/httptest"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

func newSnapshotsSDKClient(t *testing.T, ts *httptest.Server) *gcpcompute.SnapshotsClient {
	t.Helper()

	ctx := context.Background()

	client, err := gcpcompute.NewSnapshotsRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewSnapshotsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

func newDisksSDKClientForSnap(t *testing.T, ts *httptest.Server) *gcpcompute.DisksClient {
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

func TestSDKSnapshotRoundTripGCP(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Compute: cloudP.GCE})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	// Create the source disk first.
	disksClient := newDisksSDKClientForSnap(t, ts)

	diskOp, err := disksClient.Insert(ctx, &computepb.InsertDiskRequest{
		Project: testProject, Zone: testZone,
		DiskResource: &computepb.Disk{
			Name:   ptrStr("src-disk"),
			SizeGb: ptrInt64(64),
			Type:   ptrStr("zones/" + testZone + "/diskTypes/pd-standard"),
		},
	})
	if err != nil {
		t.Fatalf("disk Insert: %v", err)
	}

	if err := diskOp.Wait(ctx); err != nil {
		t.Fatalf("disk wait: %v", err)
	}

	snapsClient := newSnapshotsSDKClient(t, ts)

	insertOp, err := snapsClient.Insert(ctx, &computepb.InsertSnapshotRequest{
		Project: testProject,
		SnapshotResource: &computepb.Snapshot{
			Name:       ptrStr("snap-1"),
			SourceDisk: ptrStr("projects/" + testProject + "/zones/" + testZone + "/disks/src-disk"),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := snapsClient.Get(ctx, &computepb.GetSnapshotRequest{
		Project: testProject, Snapshot: "snap-1",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetName() != "snap-1" {
		t.Errorf("name=%s want snap-1", got.GetName())
	}

	it := snapsClient.List(ctx, &computepb.ListSnapshotsRequest{Project: testProject})

	found := false
	for {
		s, err := it.Next()
		if err != nil {
			break
		}

		if s.GetName() == "snap-1" {
			found = true
		}
	}

	if !found {
		t.Error("List did not return snap-1")
	}

	delOp, err := snapsClient.Delete(ctx, &computepb.DeleteSnapshotRequest{
		Project: testProject, Snapshot: "snap-1",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Errorf("Delete wait: %v", err)
	}
}
