package cloudtasks_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/gcp/cloudtasks"
	"github.com/stackshy/cloudemu/v2/services/cloudtasks/driver"
)

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	src := cloudtasks.New(config.NewOptions())
	name := parent + "/queues/snap"

	if _, err := src.CreateQueue(context.Background(), driver.QueueConfig{
		Name:        name,
		RetryConfig: &driver.RetryConfig{MaxAttempts: 7},
	}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if _, err := src.SetIamPolicy(context.Background(), name, driver.IAMPolicy{
		Bindings: []driver.IAMBinding{{Role: "roles/viewer", Members: []string{"user:x@y.com"}}},
	}); err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	data, err := src.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := cloudtasks.New(config.NewOptions())
	if err := dst.Restore(context.Background(), data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	q, err := dst.GetQueue(context.Background(), name)
	if err != nil {
		t.Fatalf("GetQueue after restore: %v", err)
	}

	if q.State != driver.StateRunning || q.RetryConfig.MaxAttempts != 7 {
		t.Fatalf("queue not restored faithfully: %+v", q)
	}

	pol, err := dst.GetIamPolicy(context.Background(), name)
	if err != nil {
		t.Fatalf("GetIamPolicy after restore: %v", err)
	}

	if len(pol.Bindings) != 1 || pol.Bindings[0].Role != "roles/viewer" {
		t.Fatalf("IAM policy not restored: %+v", pol)
	}
}
