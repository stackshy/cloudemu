package scheduler_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/gcp/scheduler"
	"github.com/stackshy/cloudemu/v2/services/scheduler/driver"
)

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := scheduler.New(config.NewOptions())

	cfg := driver.JobConfig{
		Name:     parent + "/jobs/snap",
		Schedule: "0 0 * * *",
		PubsubTarget: &driver.PubsubTarget{
			TopicName:  "projects/demo/topics/t",
			Data:       []byte("payload"),
			Attributes: map[string]string{"k": "v"},
		},
	}

	if _, err := src.CreateJob(ctx, cfg); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := scheduler.New(config.NewOptions())
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := dst.GetJob(ctx, parent+"/jobs/snap")
	if err != nil {
		t.Fatalf("GetJob after restore: %v", err)
	}

	if got.PubsubTarget == nil || got.PubsubTarget.TopicName != "projects/demo/topics/t" {
		t.Fatalf("pubsub target lost across snapshot: %+v", got.PubsubTarget)
	}

	if string(got.PubsubTarget.Data) != "payload" || got.PubsubTarget.Attributes["k"] != "v" {
		t.Fatalf("pubsub data/attrs lost across snapshot: %+v", got.PubsubTarget)
	}

	if got.State != driver.StateEnabled {
		t.Fatalf("state = %q, want ENABLED after restore", got.State)
	}
}
