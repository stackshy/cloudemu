package cloudrun

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
)

// TestSnapshotRoundTripCloudRun proves a snapshot/restore round-trip preserves a
// job and a service (with its materialized revision) under their original ids,
// with the container image reference — the deployable "code" — intact.
func TestSnapshotRoundTripCloudRun(t *testing.T) {
	ctx := context.Background()
	src := newMock(t, nil)

	if _, err := src.CreateJob(ctx, jobCfg()); err != nil {
		t.Fatalf("create job: %v", err)
	}

	if _, err := src.CreateService(ctx, driver.ServiceConfig{
		Name: "web", Location: "us-central1",
		Containers: []driver.Container{{Name: "app", Image: "gcr.io/proj/app:v1"}},
	}); err != nil {
		t.Fatalf("create service: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock(t, nil)
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	job, err := dst.GetJob(ctx, "batch")
	if err != nil || len(job.Containers) != 1 || job.Containers[0].Image != "busybox" {
		t.Fatalf("restored job = %+v, err %v", job, err)
	}

	svc, err := dst.GetService(ctx, "web")
	if err != nil || len(svc.Containers) != 1 || svc.Containers[0].Image != "gcr.io/proj/app:v1" {
		t.Fatalf("restored service = %+v, err %v", svc, err)
	}

	// The service's first revision was materialized on create and must survive.
	revs, err := dst.ListRevisions(ctx, "web")
	if err != nil || len(revs) != 1 || revs[0].Containers[0].Image != "gcr.io/proj/app:v1" {
		t.Fatalf("restored revisions = %+v, err %v", revs, err)
	}
}
