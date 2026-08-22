package containerinstances

import (
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/containerinstances/driver"
)

// synthContainers builds the initial per-container state for a group with no
// engine backing: every container is reported Running.
func synthContainers(cfgs []driver.ContainerConfig) []driver.ContainerInstance {
	out := make([]driver.ContainerInstance, 0, len(cfgs))

	for i := range cfgs {
		c := &cfgs[i]
		out = append(out, driver.ContainerInstance{
			Name:       c.Name,
			Image:      c.Image,
			Command:    append([]string(nil), c.Command...),
			CPU:        c.CPU,
			MemoryInGB: c.MemoryInGB,
			Env:        append([]driver.EnvVar(nil), c.Env...),
			Current:    driver.ContainerState{State: containerStateRunning},
		})
	}

	return out
}

// engineContainers maps the group's container configs onto the engine's neutral
// container spec.
func engineContainers(cfgs []driver.ContainerConfig) []config.ContainerSpec {
	out := make([]config.ContainerSpec, 0, len(cfgs))

	for i := range cfgs {
		c := &cfgs[i]
		out = append(out, config.ContainerSpec{
			Name:    c.Name,
			Image:   c.Image,
			Command: append([]string(nil), c.Command...),
			Env:     envMap(c.Env),
		})
	}

	return out
}

// envMap flattens a container's env vars into the engine's env map.
func envMap(env []driver.EnvVar) map[string]string {
	if len(env) == 0 {
		return nil
	}

	out := make(map[string]string, len(env))
	for _, kv := range env {
		out[kv.Name] = kv.Value
	}

	return out
}

// applyStatuses reflects the engine's observed per-container status onto the
// group's containers (matched by name) and rolls the group-level state up: a
// group whose containers have all exited is Succeeded, otherwise it is Running.
func applyStatuses(group *driver.ContainerGroup, statuses []config.ContainerStatus, now time.Time) {
	byName := make(map[string]config.ContainerStatus, len(statuses))
	for _, s := range statuses {
		byName[s.Name] = s
	}

	allExited := true

	for i := range group.Containers {
		s, ok := byName[group.Containers[i].Name]
		if !ok {
			allExited = false

			continue
		}

		group.Containers[i].Current = engineState(s, now)

		if s.State != engineStateExited {
			allExited = false
		}
	}

	if allExited && len(group.Containers) > 0 {
		group.State = groupStateSucceeded
	}
}

// engineState maps one engine container status onto ACI's currentState. A
// running container has no exit code; an exited one carries its code and a
// finish time.
func engineState(s config.ContainerStatus, now time.Time) driver.ContainerState {
	if s.State == engineStateExited {
		return driver.ContainerState{
			State:        containerStateTerminated,
			ExitCode:     s.ExitCode,
			HasExitCode:  true,
			StartTime:    now,
			FinishTime:   now,
			DetailStatus: "Completed",
		}
	}

	return driver.ContainerState{
		State:     containerStateRunning,
		StartTime: now,
	}
}
