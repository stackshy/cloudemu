package composer

import (
	"context"
	"testing"

	cdriver "github.com/stackshy/cloudemu/v2/services/composer/driver"
)

func TestSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	if _, _, err := src.CreateEnvironment(ctx, createCfg()); err != nil {
		t.Fatalf("CreateEnvironment: %v", err)
	}

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Identity is preserved: the environment resolves under its original
	// coordinates with its full config, computed outputs, and generated uuid.
	e, err := dst.GetEnvironment(ctx, "proj", "us-central1", "analytics")
	if err != nil {
		t.Fatalf("GetEnvironment after restore: %v", err)
	}

	if e.UUID == "" || e.Config.SoftwareConfig.ImageVersion != "composer-2.9.7-airflow-2.9.3" {
		t.Fatalf("restored environment incomplete: uuid=%q image=%q",
			e.UUID, e.Config.SoftwareConfig.ImageVersion)
	}

	if e.Config.GkeCluster == "" || e.Config.DagGcsPrefix == "" {
		t.Fatalf("restored computed outputs missing: %+v", e.Config)
	}

	if string(e.Config.Other["privateEnvironmentConfig"]) != `{"enablePrivateEnvironment":true}` {
		t.Fatalf("restored opaque block missing: %s", e.Config.Other["privateEnvironmentConfig"])
	}

	// A fresh operation after restore must not collide with the restored ids.
	_, op, err := dst.UpdateEnvironment(ctx, "proj", "us-central1", "analytics",
		&cdriver.EnvironmentConfig{}, map[string]string{"team": "data"}, []string{"labels"})
	if err != nil || op == nil {
		t.Fatalf("UpdateEnvironment after restore: %v op=%+v", err, op)
	}
}
