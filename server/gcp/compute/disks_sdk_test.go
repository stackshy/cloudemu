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

// newGCPServer builds an in-process GCP server backed by a fresh GCP cloud.
func newGCPServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Compute: cloudP.GCE})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func insertDisk(t *testing.T, c *gcpcompute.DisksClient, disk *computepb.Disk) {
	t.Helper()
	insertDiskZone(t, c, testZone, disk)
}

// insertDiskZone inserts a disk into a specific zone and waits for the op.
func insertDiskZone(t *testing.T, c *gcpcompute.DisksClient, zone string, disk *computepb.Disk) {
	t.Helper()

	ctx := context.Background()

	op, err := c.Insert(ctx, &computepb.InsertDiskRequest{
		Project: testProject, Zone: zone, DiskResource: disk,
	})
	if err != nil {
		t.Fatalf("disk Insert (%s): %v", zone, err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("disk Insert wait (%s): %v", zone, err)
	}
}

// otherZone is a second zone (distinct from testZone) used to prove disk
// operations are zone-scoped and never resolve across zones.
const otherZone = "us-west1-b"

// TestSDKDiskGetCrossZoneNotFound proves get/delete/resize against a zone that
// does not hold the disk return 404 instead of resolving another zone's disk
// (the cross-zone data-loss bug for delete).
func TestSDKDiskGetCrossZoneNotFound(t *testing.T) {
	ts := newGCPServer(t)
	client := newDisksSDKClient(t, ts)
	ctx := context.Background()

	// Disk lives in otherZone (us-west1-b).
	insertDiskZone(t, client, otherZone, &computepb.Disk{Name: ptrStr("west-disk"), SizeGb: ptrInt64(10)})

	// GET via testZone (us-central1-a) must NOT resolve the west disk.
	if _, err := client.Get(ctx, &computepb.GetDiskRequest{
		Project: testProject, Zone: testZone, Disk: "west-disk",
	}); err == nil {
		t.Fatal("Get in wrong zone resolved a disk, want 404 notFound")
	}

	// DELETE via the wrong zone must 404 (must NOT destroy the west disk).
	if _, err := client.Delete(ctx, &computepb.DeleteDiskRequest{
		Project: testProject, Zone: testZone, Disk: "west-disk",
	}); err == nil {
		t.Fatal("Delete in wrong zone succeeded, want 404 notFound (data loss)")
	}

	// RESIZE via the wrong zone must 404.
	if _, err := client.Resize(ctx, &computepb.ResizeDiskRequest{
		Project: testProject, Zone: testZone, Disk: "west-disk",
		DisksResizeRequestResource: &computepb.DisksResizeRequest{SizeGb: ptrInt64(50)},
	}); err == nil {
		t.Fatal("Resize in wrong zone succeeded, want 404 notFound")
	}

	// The west disk is untouched: still gettable in its own zone at its size.
	got, err := client.Get(ctx, &computepb.GetDiskRequest{
		Project: testProject, Zone: otherZone, Disk: "west-disk",
	})
	if err != nil {
		t.Fatalf("Get west-disk in its own zone: %v", err)
	}

	if got.GetSizeGb() != 10 {
		t.Errorf("west-disk sizeGb=%d want 10 (should be untouched)", got.GetSizeGb())
	}

	if !strings.HasSuffix(got.GetZone(), "/zones/"+otherZone) {
		t.Errorf("west-disk zone=%s want suffix /zones/%s", got.GetZone(), otherZone)
	}
}

// TestSDKDiskListZoneScoped proves disks.list returns only the requested zone's
// disks, each labeled with its own zone selfLink.
func TestSDKDiskListZoneScoped(t *testing.T) {
	ts := newGCPServer(t)
	client := newDisksSDKClient(t, ts)
	ctx := context.Background()

	insertDiskZone(t, client, testZone, &computepb.Disk{Name: ptrStr("central-disk"), SizeGb: ptrInt64(10)})
	insertDiskZone(t, client, otherZone, &computepb.Disk{Name: ptrStr("west-disk"), SizeGb: ptrInt64(10)})

	it := client.List(ctx, &computepb.ListDisksRequest{Project: testProject, Zone: testZone})

	names := map[string]string{}
	for {
		d, err := it.Next()
		if err != nil {
			break
		}

		names[d.GetName()] = d.GetZone()
	}

	if _, ok := names["west-disk"]; ok {
		t.Error("List(us-central1-a) leaked west-disk from another zone")
	}

	zone, ok := names["central-disk"]
	if !ok {
		t.Fatal("List(us-central1-a) did not return central-disk")
	}

	if !strings.HasSuffix(zone, "/zones/"+testZone) {
		t.Errorf("central-disk zone=%s want suffix /zones/%s", zone, testZone)
	}

	if len(names) != 1 {
		t.Errorf("List returned %d disks, want 1 (only the central zone)", len(names))
	}
}

// TestSDKDiskSameNameDifferentZones proves the same disk name inserts
// successfully in two different zones (disk names are unique per-zone).
func TestSDKDiskSameNameDifferentZones(t *testing.T) {
	ts := newGCPServer(t)
	client := newDisksSDKClient(t, ts)
	ctx := context.Background()

	insertDiskZone(t, client, testZone, &computepb.Disk{Name: ptrStr("twin"), SizeGb: ptrInt64(10)})

	// Same name in a different zone must succeed, not falsely 409.
	if _, err := client.Insert(ctx, &computepb.InsertDiskRequest{
		Project: testProject, Zone: otherZone,
		DiskResource: &computepb.Disk{Name: ptrStr("twin"), SizeGb: ptrInt64(20)},
	}); err != nil {
		t.Fatalf("same-name insert in different zone failed: %v", err)
	}

	// Both are distinct disks, each in its own zone with its own size.
	central, err := client.Get(ctx, &computepb.GetDiskRequest{Project: testProject, Zone: testZone, Disk: "twin"})
	if err != nil {
		t.Fatalf("Get twin in central: %v", err)
	}

	west, err := client.Get(ctx, &computepb.GetDiskRequest{Project: testProject, Zone: otherZone, Disk: "twin"})
	if err != nil {
		t.Fatalf("Get twin in west: %v", err)
	}

	if central.GetSizeGb() != 10 || west.GetSizeGb() != 20 {
		t.Errorf("sizes central=%d west=%d want 10 and 20 (distinct disks)", central.GetSizeGb(), west.GetSizeGb())
	}
}

// TestSDKDiskAggregatedListSpansZones proves aggregatedList still spans zones
// after the get/list scoping fix, grouping each disk under its own zone.
func TestSDKDiskAggregatedListSpansZones(t *testing.T) {
	ts := newGCPServer(t)
	client := newDisksSDKClient(t, ts)
	ctx := context.Background()

	insertDiskZone(t, client, testZone, &computepb.Disk{Name: ptrStr("central-agg"), SizeGb: ptrInt64(10)})
	insertDiskZone(t, client, otherZone, &computepb.Disk{Name: ptrStr("west-agg"), SizeGb: ptrInt64(10)})

	it := client.AggregatedList(ctx, &computepb.AggregatedListDisksRequest{Project: testProject})

	seen := map[string]bool{}
	for {
		pair, err := it.Next()
		if err != nil {
			break
		}

		for _, d := range pair.Value.GetDisks() {
			seen[d.GetName()] = true
		}
	}

	if !seen["central-agg"] || !seen["west-agg"] {
		t.Errorf("aggregatedList missing a zone's disk: seen=%v", seen)
	}
}

// TestSDKDiskSourceImageReflected proves disks.get echoes sourceImage and a
// derived sourceImageId (previously dropped).
func TestSDKDiskSourceImageReflected(t *testing.T) {
	ts := newGCPServer(t)
	client := newDisksSDKClient(t, ts)
	ctx := context.Background()

	const srcImage = "projects/debian-cloud/global/images/family/debian-12"

	insertDisk(t, client, &computepb.Disk{
		Name:        ptrStr("img-disk"),
		SizeGb:      ptrInt64(20),
		SourceImage: ptrStr(srcImage),
	})

	got, err := client.Get(ctx, &computepb.GetDiskRequest{
		Project: testProject, Zone: testZone, Disk: "img-disk",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetSourceImage() != srcImage {
		t.Errorf("sourceImage=%q want %q", got.GetSourceImage(), srcImage)
	}

	if got.GetSourceImageId() == "" {
		t.Error("sourceImageId is empty")
	}
}

// TestSDKDiskDuplicateNameConflict proves a duplicate-name insert is rejected
// (409) rather than silently creating a second disk.
func TestSDKDiskDuplicateNameConflict(t *testing.T) {
	ts := newGCPServer(t)
	client := newDisksSDKClient(t, ts)
	ctx := context.Background()

	insertDisk(t, client, &computepb.Disk{Name: ptrStr("dup"), SizeGb: ptrInt64(10)})

	if _, err := client.Insert(ctx, &computepb.InsertDiskRequest{
		Project: testProject, Zone: testZone,
		DiskResource: &computepb.Disk{Name: ptrStr("dup"), SizeGb: ptrInt64(10)},
	}); err == nil {
		t.Fatal("duplicate Insert succeeded, want conflict")
	}

	it := client.List(ctx, &computepb.ListDisksRequest{Project: testProject, Zone: testZone})

	count := 0
	for {
		if _, err := it.Next(); err != nil {
			break
		}

		count++
	}

	if count != 1 {
		t.Errorf("disk count=%d want 1", count)
	}
}

// TestSDKDiskCreationTimestamp proves disks.get returns a creationTimestamp.
func TestSDKDiskCreationTimestamp(t *testing.T) {
	ts := newGCPServer(t)
	client := newDisksSDKClient(t, ts)
	ctx := context.Background()

	insertDisk(t, client, &computepb.Disk{Name: ptrStr("ts-disk"), SizeGb: ptrInt64(10)})

	got, err := client.Get(ctx, &computepb.GetDiskRequest{
		Project: testProject, Zone: testZone, Disk: "ts-disk",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetCreationTimestamp() == "" {
		t.Error("creationTimestamp is empty")
	}
}

// TestSDKDiskResize proves disks.resize grows the disk (previously 501).
func TestSDKDiskResize(t *testing.T) {
	ts := newGCPServer(t)
	client := newDisksSDKClient(t, ts)
	ctx := context.Background()

	insertDisk(t, client, &computepb.Disk{Name: ptrStr("grow"), SizeGb: ptrInt64(32)})

	op, err := client.Resize(ctx, &computepb.ResizeDiskRequest{
		Project: testProject, Zone: testZone, Disk: "grow",
		DisksResizeRequestResource: &computepb.DisksResizeRequest{SizeGb: ptrInt64(50)},
	})
	if err != nil {
		t.Fatalf("Resize: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Resize wait: %v", err)
	}

	got, err := client.Get(ctx, &computepb.GetDiskRequest{
		Project: testProject, Zone: testZone, Disk: "grow",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetSizeGb() != 50 {
		t.Errorf("sizeGb=%d want 50", got.GetSizeGb())
	}
}

// TestSDKDiskAggregatedList proves aggregatedList returns disks grouped by zone
// (previously 501).
func TestSDKDiskAggregatedList(t *testing.T) {
	ts := newGCPServer(t)
	client := newDisksSDKClient(t, ts)
	ctx := context.Background()

	insertDisk(t, client, &computepb.Disk{Name: ptrStr("agg-disk"), SizeGb: ptrInt64(10)})

	it := client.AggregatedList(ctx, &computepb.AggregatedListDisksRequest{Project: testProject})

	found := false
	for {
		pair, err := it.Next()
		if err != nil {
			break
		}

		for _, d := range pair.Value.GetDisks() {
			if d.GetName() == "agg-disk" {
				found = true
			}
		}
	}

	if !found {
		t.Error("AggregatedList did not return agg-disk")
	}
}

// TestSDKDiskUsersReflectAttachment proves a disk attached to an instance shows
// the instance in users[], consistent with the instance-side disks[].
func TestSDKDiskUsersReflectAttachment(t *testing.T) {
	ts := newGCPServer(t)
	disks := newDisksSDKClient(t, ts)
	instances := newSDKInstancesClient(t, ts)
	ctx := context.Background()

	insertDisk(t, disks, &computepb.Disk{Name: ptrStr("shared-disk"), SizeGb: ptrInt64(10)})

	insOp, err := instances.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: testProject, Zone: testZone,
		InstanceResource: &computepb.Instance{
			Name:        ptrStr("holder-vm"),
			MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
			NetworkInterfaces: []*computepb.NetworkInterface{
				{Network: ptrStr("global/networks/default")},
			},
		},
	})
	if err != nil {
		t.Fatalf("instance Insert: %v", err)
	}

	if err := insOp.Wait(ctx); err != nil {
		t.Fatalf("instance Insert wait: %v", err)
	}

	diskURL := "projects/" + testProject + "/zones/" + testZone + "/disks/shared-disk"

	attachOp, err := instances.AttachDisk(ctx, &computepb.AttachDiskInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "holder-vm",
		AttachedDiskResource: &computepb.AttachedDisk{
			Source: ptrStr(diskURL), DeviceName: ptrStr("data"),
		},
	})
	if err != nil {
		t.Fatalf("AttachDisk: %v", err)
	}

	if err := attachOp.Wait(ctx); err != nil {
		t.Fatalf("AttachDisk wait: %v", err)
	}

	got, err := disks.Get(ctx, &computepb.GetDiskRequest{
		Project: testProject, Zone: testZone, Disk: "shared-disk",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	users := got.GetUsers()
	if len(users) != 1 || !strings.HasSuffix(users[0], "/instances/holder-vm") {
		t.Errorf("users=%v want one link ending /instances/holder-vm", users)
	}
}
