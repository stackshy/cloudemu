package ecs

import (
	"testing"

	driver "github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// TestTaskToWireCarriesEngineFields guards that an engine-backed container's
// ExitCode/Reason/RuntimeID reach the DescribeTasks wire response — they are
// populated on the driver struct but were previously dropped by taskToWire, so
// a real `aws ecs describe-tasks` never saw them.
func TestTaskToWireCarriesEngineFields(t *testing.T) {
	task := &driver.Task{
		ARN: "arn:task/1",
		Containers: []driver.Container{{
			Name: "app", Image: "alpine", LastStatus: "STOPPED",
			ExitCode: 7, Reason: "EssentialContainerExited", RuntimeID: "handle-abc",
		}},
	}

	wire := taskToWire(task)
	if len(wire.Containers) != 1 {
		t.Fatalf("want 1 container, got %d", len(wire.Containers))
	}

	c := wire.Containers[0]
	if c.ExitCode != 7 || c.Reason != "EssentialContainerExited" || c.RuntimeID != "handle-abc" {
		t.Fatalf("engine fields not carried to wire: %+v", c)
	}
}
