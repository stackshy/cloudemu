package dataproc

import (
	"context"
	"testing"

	dpdriver "github.com/stackshy/cloudemu/v2/services/dataproc/driver"
)

func TestSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	if _, _, err := src.CreateCluster(ctx, createCfg()); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Identity is preserved: the cluster resolves under its original coordinates
	// with its full config and generated uuid.
	c, err := dst.GetCluster(ctx, "proj", "us-central1", "analytics")
	if err != nil {
		t.Fatalf("GetCluster after restore: %v", err)
	}

	if c.ClusterUUID == "" || c.Config.WorkerConfig.NumInstances != 2 {
		t.Fatalf("restored cluster incomplete: uuid=%q worker=%d", c.ClusterUUID, c.Config.WorkerConfig.NumInstances)
	}

	if c.Config.SoftwareConfig.ImageVersion != "2.1-debian11" {
		t.Fatalf("restored imageVersion = %q", c.Config.SoftwareConfig.ImageVersion)
	}

	// A fresh operation after restore must not collide with the restored ids.
	_, op, err := dst.UpdateCluster(ctx, "proj", "us-central1", "analytics", dpdriver.UpdateClusterConfig{
		FieldMask:          []string{"config.worker_config.num_instances"},
		WorkerNumInstances: 3,
	})
	if err != nil || op == nil {
		t.Fatalf("UpdateCluster after restore: %v op=%+v", err, op)
	}
}
