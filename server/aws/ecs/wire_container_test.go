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
	if c.ExitCode == nil || *c.ExitCode != 7 || c.Reason != "EssentialContainerExited" || c.RuntimeID != "handle-abc" {
		t.Fatalf("engine fields not carried to wire: %+v", c)
	}
}

// TestTaskToWireExitZeroSurfacesOnStopped guards that a stopped container with a
// genuine exit 0 reports exitCode:0 (a pointer, so omitempty keeps it), while a
// still-running container omits it.
func TestTaskToWireExitZeroSurfacesOnStopped(t *testing.T) {
	task := &driver.Task{Containers: []driver.Container{
		{Name: "done", LastStatus: "STOPPED", ExitCode: 0},
		{Name: "live", LastStatus: "RUNNING", ExitCode: 0},
	}}

	wire := taskToWire(task)
	if wire.Containers[0].ExitCode == nil || *wire.Containers[0].ExitCode != 0 {
		t.Fatalf("stopped container should report exit 0, got %v", wire.Containers[0].ExitCode)
	}

	if wire.Containers[1].ExitCode != nil {
		t.Fatalf("running container should omit exitCode, got %v", *wire.Containers[1].ExitCode)
	}
}
