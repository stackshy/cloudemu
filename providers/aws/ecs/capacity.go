package ecs

import (
	"strconv"

	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// Launch types recognized by RunTask. Empty defaults to EC2 (matching AWS when
// no capacityProviderStrategy is supplied).
const (
	launchEC2      = "EC2"
	launchFargate  = "FARGATE"
	launchExternal = "EXTERNAL"

	networkModeAwsvpc = "awsvpc"
	networkModeHost   = "host"

	// ephemeralPortBase / ephemeralPortSpan bound the dynamic host-port range ECS
	// assigns a bridge-mode container port mapping left with hostPort unset,
	// matching the Linux ephemeral-port range (32768-65535) real ECS agents draw
	// from.
	ephemeralPortBase = 32768
	ephemeralPortSpan = 65536 - ephemeralPortBase

	// ecsServicePrincipal / managedLaunchTag mark a container instance's backing
	// EC2 instance as managed by ECS (composing #159 with #300).
	ecsServicePrincipal = "ecs.amazonaws.com"
	managedLaunchTag    = "aws:ec2:managed-launch"

	// Placement-failure reasons, matching the ECS RunTask failures[] vocabulary.
	reasonNoInstances   = "AGENT"
	reasonResourceMem   = "RESOURCE:MEMORY"
	reasonResourceCPU   = "RESOURCE:CPU"
	noInstancesDetail   = "No Container Instances were found in your cluster."
	fargatePlatformLate = "1.4.0"
)

// effectiveLaunchType resolves the launch type RunTask should use: the requested
// value, or EC2 when empty. AWS defaults an empty launchType to EC2 only when no
// capacityProviderStrategy is supplied; capacity-provider resolution is Wave 4,
// so a supplied strategy is accepted and the placement still falls through to
// EC2 here (documented limitation).
func effectiveLaunchType(in *driver.RunTaskInput) string {
	if in.LaunchType != "" {
		return in.LaunchType
	}

	return launchEC2
}

// requiredResources computes the CPU units and memory (MiB) a task reserves.
// Task-level cpu/memory on the task definition win; when absent, the sum of the
// container-level values is used. This mirrors how ECS derives a task's resource
// footprint (task-level overrides, else the roll-up of its containers). It is a
// reasonable model, not a byte-exact reproduction of AWS's reservation math.
func requiredResources(td *driver.TaskDefinition) (cpu, memory int) {
	cpu = atoiSafe(td.CPU)
	memory = atoiSafe(td.Memory)

	if cpu == 0 {
		for i := range td.ContainerDefinitions {
			cpu += td.ContainerDefinitions[i].CPU
		}
	}

	if memory == 0 {
		for i := range td.ContainerDefinitions {
			memory += td.ContainerDefinitions[i].Memory
		}
	}

	return cpu, memory
}

// reserve finds the first ACTIVE, agent-connected container instance in the
// cluster with enough remaining CPU and memory, decrements its remaining
// resources, increments its RunningTasksCount, and returns its ARN. On failure
// it returns an empty ARN and a RunTask failure reason. First-fit placement is
// intentional: it is not real bin-pack or spread scheduling. Callers must not
// hold placeMu; reserve takes it to make the read-modify-write atomic.
func (m *Mock) reserve(cluster string, cpu, memory int) (arn, reason string) {
	m.placeMu.Lock()
	defer m.placeMu.Unlock()

	var haveCandidate, memShort bool

	for _, ci := range m.instances.SortedValues() {
		if instanceClusterName(ci.ARN) != cluster || ci.Status != statusActive || !ci.AgentConnected {
			continue
		}

		haveCandidate = true

		if ci.RemainingMemory < memory {
			memShort = true
			continue
		}

		if ci.RemainingCPU < cpu {
			continue
		}

		updated := *ci
		updated.RemainingCPU -= cpu
		updated.RemainingMemory -= memory
		updated.RunningTasksCount++
		m.instances.Set(updated.ARN, &updated)

		return updated.ARN, ""
	}

	switch {
	case !haveCandidate:
		return "", reasonNoInstances
	case memShort:
		return "", reasonResourceMem
	default:
		return "", reasonResourceCPU
	}
}

// release returns a stopped task's reserved CPU/memory to its container instance
// and decrements its RunningTasksCount. Remaining resources are capped at the
// registered amounts so repeated releases can never over-credit. Callers must
// hold placeMu.
func (m *Mock) release(instanceARN string, cpu, memory int) {
	ci, ok := m.instances.Get(instanceARN)
	if !ok {
		return
	}

	updated := *ci

	updated.RemainingCPU = capAt(updated.RemainingCPU+cpu, updated.RegisteredCPU)
	updated.RemainingMemory = capAt(updated.RemainingMemory+memory, updated.RegisteredMemory)

	if updated.RunningTasksCount > 0 {
		updated.RunningTasksCount--
	}

	m.instances.Set(instanceARN, &updated)
}

// capAt clamps v to limit.
func capAt(v, limit int) int {
	if v > limit {
		return limit
	}

	return v
}

// atoiSafe parses s as an int, returning 0 for empty or malformed values.
func atoiSafe(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}

	return n
}

// containsLaunchType reports whether launchType is present in the (possibly
// empty) requiresCompatibilities list.
func containsLaunchType(list []string, launchType string) bool {
	for _, v := range list {
		if v == launchType {
			return true
		}
	}

	return false
}
