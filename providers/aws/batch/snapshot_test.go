package batch_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	mustCreateCE(t, src, "ce")

	if _, err := src.CreateJobQueue(ctx, driver.CreateJobQueueInput{
		Name:                    "q",
		Priority:                7,
		ComputeEnvironmentOrder: []driver.ComputeEnvironmentOrder{{Order: 1, ComputeEnvironment: "ce"}},
	}); err != nil {
		t.Fatalf("create queue: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := src.RegisterJobDefinition(ctx, driver.RegisterJobDefinitionInput{
			Name: "jd", Type: driver.JDTypeContainer, ContainerProperties: json.RawMessage(`{"image":"alpine"}`),
		}); err != nil {
			t.Fatalf("register: %v", err)
		}
	}

	data, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	ces, _ := dst.DescribeComputeEnvironments(ctx, nil)
	if len(ces) != 1 || ces[0].Name != "ce" || ces[0].Status != driver.StatusValid {
		t.Fatalf("compute environment not restored: %+v", ces)
	}

	queues, _ := dst.DescribeJobQueues(ctx, nil)
	if len(queues) != 1 || queues[0].Priority != 7 || len(queues[0].ComputeEnvironmentOrder) != 1 {
		t.Fatalf("job queue not restored: %+v", queues)
	}

	jds, _ := dst.DescribeJobDefinitions(ctx, driver.DescribeJobDefinitionsInput{Name: "jd"})
	if len(jds) != 2 {
		t.Fatalf("want 2 restored revisions, got %d", len(jds))
	}

	// The max-revision counter must survive so the next register continues at 3.
	next, err := dst.RegisterJobDefinition(ctx, driver.RegisterJobDefinitionInput{
		Name: "jd", Type: driver.JDTypeContainer, ContainerProperties: json.RawMessage(`{"image":"alpine"}`),
	})
	if err != nil {
		t.Fatalf("register after restore: %v", err)
	}

	if next.Revision != 3 {
		t.Fatalf("revision counter not restored: want 3, got %d", next.Revision)
	}
}
