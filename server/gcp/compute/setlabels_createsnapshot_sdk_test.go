package compute_test

import (
	"context"
	"strings"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
)

// TestSDKDiskSetLabels proves disks.setLabels REPLACES the disk's label set
// (a dropped key disappears) under labelFingerprint optimistic-concurrency,
// using a real cloud.google.com/go DisksClient.
func TestSDKDiskSetLabels(t *testing.T) {
	ts := newGCPServer(t)
	disks := newDisksSDKClient(t, ts)
	ctx := context.Background()

	insertDisk(t, disks, &computepb.Disk{
		Name:   ptrStr("lbl-disk"),
		SizeGb: ptrInt64(16),
		Labels: map[string]string{"env": "prod", "team": "core"},
	})

	got, err := disks.Get(ctx, &computepb.GetDiskRequest{Project: testProject, Zone: testZone, Disk: "lbl-disk"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Replace the label set: keep env (new value), drop team, add owner.
	setOp, err := disks.SetLabels(ctx, &computepb.SetLabelsDiskRequest{
		Project: testProject, Zone: testZone, Resource: "lbl-disk",
		ZoneSetLabelsRequestResource: &computepb.ZoneSetLabelsRequest{
			LabelFingerprint: ptrStr(got.GetLabelFingerprint()),
			Labels:           map[string]string{"env": "staging", "owner": "nitin"},
		},
	})
	if err != nil {
		t.Fatalf("SetLabels: %v", err)
	}

	if err := setOp.Wait(ctx); err != nil {
		t.Fatalf("SetLabels wait: %v", err)
	}

	after, err := disks.Get(ctx, &computepb.GetDiskRequest{Project: testProject, Zone: testZone, Disk: "lbl-disk"})
	if err != nil {
		t.Fatalf("Get after SetLabels: %v", err)
	}

	labels := after.GetLabels()
	if labels["env"] != "staging" || labels["owner"] != "nitin" {
		t.Errorf("labels=%v want env=staging owner=nitin", labels)
	}

	if _, dropped := labels["team"]; dropped {
		t.Errorf("labels=%v: team should have been replaced away (setLabels replaces the set)", labels)
	}

	if after.GetLabelFingerprint() == got.GetLabelFingerprint() {
		t.Error("labelFingerprint did not change after replacing labels")
	}
}

// TestSDKDiskSetLabelsStaleFingerprint proves a wrong labelFingerprint loses the
// optimistic-concurrency check with a 412 conditionNotMet.
func TestSDKDiskSetLabelsStaleFingerprint(t *testing.T) {
	ts := newGCPServer(t)
	disks := newDisksSDKClient(t, ts)
	ctx := context.Background()

	insertDisk(t, disks, &computepb.Disk{
		Name:   ptrStr("stale-disk"),
		SizeGb: ptrInt64(16),
		Labels: map[string]string{"env": "prod"},
	})

	_, err := disks.SetLabels(ctx, &computepb.SetLabelsDiskRequest{
		Project: testProject, Zone: testZone, Resource: "stale-disk",
		ZoneSetLabelsRequestResource: &computepb.ZoneSetLabelsRequest{
			LabelFingerprint: ptrStr("bogus-stale-fingerprint"),
			Labels:           map[string]string{"env": "staging"},
		},
	})
	if err == nil {
		t.Fatal("SetLabels with stale fingerprint succeeded, want 412 conditionNotMet")
	}

	if !strings.Contains(err.Error(), "412") && !strings.Contains(err.Error(), "conditionNotMet") {
		t.Errorf("SetLabels error = %v, want 412/conditionNotMet", err)
	}
}

// TestSDKDiskSetLabelsNotFound proves setLabels on a missing disk is a 404.
func TestSDKDiskSetLabelsNotFound(t *testing.T) {
	ts := newGCPServer(t)
	disks := newDisksSDKClient(t, ts)
	ctx := context.Background()

	_, err := disks.SetLabels(ctx, &computepb.SetLabelsDiskRequest{
		Project: testProject, Zone: testZone, Resource: "ghost-disk",
		ZoneSetLabelsRequestResource: &computepb.ZoneSetLabelsRequest{
			Labels: map[string]string{"env": "staging"},
		},
	})
	if err == nil {
		t.Fatal("SetLabels on missing disk succeeded, want 404")
	}

	if !strings.Contains(err.Error(), "404") && !strings.Contains(err.Error(), "not found") {
		t.Errorf("SetLabels error = %v, want 404/not found", err)
	}
}

// TestSDKImageSetLabels proves images.setLabels replaces the image's label set.
func TestSDKImageSetLabels(t *testing.T) {
	ts := newGCPServer(t)
	disks := newDisksSDKClient(t, ts)
	images := newImagesSDKClient(t, ts)
	ctx := context.Background()

	insertDisk(t, disks, &computepb.Disk{Name: ptrStr("img-src-disk"), SizeGb: ptrInt64(20)})

	imgOp, err := images.Insert(ctx, &computepb.InsertImageRequest{
		Project: testProject,
		ImageResource: &computepb.Image{
			Name:       ptrStr("lbl-image"),
			SourceDisk: ptrStr("projects/" + testProject + "/zones/" + testZone + "/disks/img-src-disk"),
			Labels:     map[string]string{"env": "prod", "team": "core"},
		},
	})
	if err != nil {
		t.Fatalf("image Insert: %v", err)
	}

	if err := imgOp.Wait(ctx); err != nil {
		t.Fatalf("image Insert wait: %v", err)
	}

	got, err := images.Get(ctx, &computepb.GetImageRequest{Project: testProject, Image: "lbl-image"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	setOp, err := images.SetLabels(ctx, &computepb.SetLabelsImageRequest{
		Project: testProject, Resource: "lbl-image",
		GlobalSetLabelsRequestResource: &computepb.GlobalSetLabelsRequest{
			LabelFingerprint: ptrStr(got.GetLabelFingerprint()),
			Labels:           map[string]string{"env": "staging"},
		},
	})
	if err != nil {
		t.Fatalf("SetLabels: %v", err)
	}

	if err := setOp.Wait(ctx); err != nil {
		t.Fatalf("SetLabels wait: %v", err)
	}

	after, err := images.Get(ctx, &computepb.GetImageRequest{Project: testProject, Image: "lbl-image"})
	if err != nil {
		t.Fatalf("Get after SetLabels: %v", err)
	}

	labels := after.GetLabels()
	if labels["env"] != "staging" {
		t.Errorf("labels=%v want env=staging", labels)
	}

	if _, dropped := labels["team"]; dropped {
		t.Errorf("labels=%v: team should have been replaced away", labels)
	}
}

// TestSDKSnapshotSetLabels proves snapshots.setLabels replaces the label set.
func TestSDKSnapshotSetLabels(t *testing.T) {
	ts := newGCPServer(t)
	disks := newDisksSDKClient(t, ts)
	snaps := newSnapshotsSDKClient(t, ts)
	ctx := context.Background()

	insertDisk(t, disks, &computepb.Disk{Name: ptrStr("snap-lbl-src"), SizeGb: ptrInt64(24)})

	snapOp, err := snaps.Insert(ctx, &computepb.InsertSnapshotRequest{
		Project: testProject,
		SnapshotResource: &computepb.Snapshot{
			Name:       ptrStr("lbl-snap"),
			SourceDisk: ptrStr("projects/" + testProject + "/zones/" + testZone + "/disks/snap-lbl-src"),
			Labels:     map[string]string{"env": "prod", "team": "core"},
		},
	})
	if err != nil {
		t.Fatalf("snapshot Insert: %v", err)
	}

	if err := snapOp.Wait(ctx); err != nil {
		t.Fatalf("snapshot Insert wait: %v", err)
	}

	got, err := snaps.Get(ctx, &computepb.GetSnapshotRequest{Project: testProject, Snapshot: "lbl-snap"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	setOp, err := snaps.SetLabels(ctx, &computepb.SetLabelsSnapshotRequest{
		Project: testProject, Resource: "lbl-snap",
		GlobalSetLabelsRequestResource: &computepb.GlobalSetLabelsRequest{
			LabelFingerprint: ptrStr(got.GetLabelFingerprint()),
			Labels:           map[string]string{"env": "staging"},
		},
	})
	if err != nil {
		t.Fatalf("SetLabels: %v", err)
	}

	if err := setOp.Wait(ctx); err != nil {
		t.Fatalf("SetLabels wait: %v", err)
	}

	after, err := snaps.Get(ctx, &computepb.GetSnapshotRequest{Project: testProject, Snapshot: "lbl-snap"})
	if err != nil {
		t.Fatalf("Get after SetLabels: %v", err)
	}

	labels := after.GetLabels()
	if labels["env"] != "staging" {
		t.Errorf("labels=%v want env=staging", labels)
	}

	if _, dropped := labels["team"]; dropped {
		t.Errorf("labels=%v: team should have been replaced away", labels)
	}
}

// TestSDKDiskCreateSnapshot proves the disk-scoped createSnapshot verb produces
// a global snapshot whose sourceDisk points at the disk and whose labels match
// the create request, visible through snapshots.get.
func TestSDKDiskCreateSnapshot(t *testing.T) {
	ts := newGCPServer(t)
	disks := newDisksSDKClient(t, ts)
	snaps := newSnapshotsSDKClient(t, ts)
	ctx := context.Background()

	insertDisk(t, disks, &computepb.Disk{Name: ptrStr("cs-disk"), SizeGb: ptrInt64(50)})

	const wantSource = "projects/" + testProject + "/zones/" + testZone + "/disks/cs-disk"

	csOp, err := disks.CreateSnapshot(ctx, &computepb.CreateSnapshotDiskRequest{
		Project: testProject, Zone: testZone, Disk: "cs-disk",
		SnapshotResource: &computepb.Snapshot{
			Name:   ptrStr("cs-snap"),
			Labels: map[string]string{"purpose": "backup"},
		},
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	if err := csOp.Wait(ctx); err != nil {
		t.Fatalf("CreateSnapshot wait: %v", err)
	}

	got, err := snaps.Get(ctx, &computepb.GetSnapshotRequest{Project: testProject, Snapshot: "cs-snap"})
	if err != nil {
		t.Fatalf("Get: %v (disk-scoped snapshot should be visible in snapshots.get)", err)
	}

	if got.GetName() != "cs-snap" {
		t.Errorf("name=%q want cs-snap", got.GetName())
	}

	if got.GetSourceDisk() != wantSource {
		t.Errorf("sourceDisk=%q want %q", got.GetSourceDisk(), wantSource)
	}

	if got.GetDiskSizeGb() != 50 {
		t.Errorf("diskSizeGb=%d want 50 (inherited from source disk)", got.GetDiskSizeGb())
	}

	if got.GetLabels()["purpose"] != "backup" {
		t.Errorf("labels=%v want purpose=backup", got.GetLabels())
	}
}

// TestSDKDiskCreateSnapshotNotFound proves createSnapshot on a missing disk is a 404.
func TestSDKDiskCreateSnapshotNotFound(t *testing.T) {
	ts := newGCPServer(t)
	disks := newDisksSDKClient(t, ts)
	ctx := context.Background()

	_, err := disks.CreateSnapshot(ctx, &computepb.CreateSnapshotDiskRequest{
		Project: testProject, Zone: testZone, Disk: "ghost-disk",
		SnapshotResource: &computepb.Snapshot{Name: ptrStr("ghost-snap")},
	})
	if err == nil {
		t.Fatal("CreateSnapshot on missing disk succeeded, want 404")
	}

	if !strings.Contains(err.Error(), "404") && !strings.Contains(err.Error(), "not found") {
		t.Errorf("CreateSnapshot error = %v, want 404/not found", err)
	}
}
