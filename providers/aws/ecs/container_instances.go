package ecs

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// Default capacity of a seeded container instance, modeling a modest EC2 box.
const (
	defaultInstanceCPU    = 2048
	defaultInstanceMemory = 4096

	statusDraining = "DRAINING"
)

// InstanceOption customizes a seeded container instance. It is the seam through
// which a future RegisterContainerInstance SDK op (Wave 4) and the existing
// SeedContainerInstance helper share one registration path.
type InstanceOption func(*driver.ContainerInstance)

// WithCapacity sets the registered CPU units and memory (MiB) of a seeded
// container instance. Remaining capacity is initialized to the registered
// amounts.
func WithCapacity(cpu, memory int) InstanceOption {
	return func(ci *driver.ContainerInstance) {
		ci.RegisteredCPU = cpu
		ci.RegisteredMemory = memory
		ci.RemainingCPU = cpu
		ci.RemainingMemory = memory
	}
}

// newInstance builds an ACTIVE container-instance record for a cluster with the
// given EC2 instance id and capacity, without storing it. It is the shared
// construction seam for SeedContainerInstance and RegisterContainerInstance.
func (m *Mock) newInstance(cluster, ec2InstanceID string, cpu, memory int) *driver.ContainerInstance {
	name := resolveClusterName(cluster)

	return &driver.ContainerInstance{
		ARN:              m.arn("container-instance/" + name + "/" + m.hexID()),
		EC2InstanceID:    ec2InstanceID,
		Status:           statusActive,
		AgentConnected:   true,
		RegisteredCPU:    cpu,
		RegisteredMemory: memory,
		RemainingCPU:     cpu,
		RemainingMemory:  memory,
	}
}

// launchManagedInstance provisions a backing managed EC2 instance via the wired
// launcher (so the container instance is discoverable as a managed EC2 instance,
// composing #159 with #300). When no launcher is wired it synthesizes an id; a
// wired launcher that fails is surfaced, not silently swallowed (a silent
// fallback would hide a real misconfiguration and regress the compose).
func (m *Mock) launchManagedInstance(cluster string) (string, error) {
	if m.launcher == nil {
		return "i-" + m.hexID()[:17], nil
	}

	id, err := m.launcher.LaunchManaged("m5.large", ecsServicePrincipal, map[string]string{
		managedLaunchTag:       "ecs-managed-instances",
		"aws:ecs:cluster-name": resolveClusterName(cluster),
	})
	if err != nil {
		return "", err
	}

	if id == "" {
		return "i-" + m.hexID()[:17], nil
	}

	return id, nil
}

// SeedContainerInstance registers a container instance into a cluster and
// returns it. Tests and examples use this helper to populate the
// EC2-launch-type capacity pool. Without options it registers a default 2048
// CPU / 4096 MiB box; pass WithCapacity to size it explicitly.
func (m *Mock) SeedContainerInstance(cluster, ec2InstanceID string, opts ...InstanceOption) *driver.ContainerInstance {
	ci := m.newInstance(cluster, ec2InstanceID, defaultInstanceCPU, defaultInstanceMemory)

	for _, opt := range opts {
		opt(ci)
	}

	m.instances.Set(ci.ARN, ci)

	out := *ci

	return &out
}

// RegisterContainerInstance registers an EC2 container instance from a
// RegisterContainerInstance request, deriving the EC2 instance id from the
// identity document (or generating one) and sizing capacity from the CPU/MEMORY
// TotalResources entries (defaulting to 2048/4096).
//
//nolint:gocritic // in is passed by value to satisfy the driver.ECS interface; the copy is cheap for a mock.
func (m *Mock) RegisterContainerInstance(
	_ context.Context, in driver.RegisterContainerInstanceInput,
) (*driver.ContainerInstance, error) {
	cpu, memory := capacityFromResources(in.TotalResources)

	ec2ID := instanceIDFromDoc(in.InstanceIdentityDocument)
	if ec2ID == "" {
		id, err := m.launchManagedInstance(in.Cluster)
		if err != nil {
			return nil, apiErrf(errors.Internal, excServer, "failed to launch managed EC2 instance: %v", err)
		}

		ec2ID = id
	}

	ci := m.newInstance(in.Cluster, ec2ID, cpu, memory)
	m.instances.Set(ci.ARN, ci)

	out := *ci

	return &out, nil
}

// DeregisterContainerInstance removes a container instance from the pool and
// returns it (marked INACTIVE). Without force, an instance that still has
// running tasks surfaces an InvalidParameterException.
func (m *Mock) DeregisterContainerInstance(
	_ context.Context, _, containerInstance string, force bool,
) (*driver.ContainerInstance, error) {
	m.placeMu.Lock()
	defer m.placeMu.Unlock()

	ci, ok := m.resolveInstance(containerInstance)
	if !ok {
		return nil, apiErrf(errors.NotFound, excInvalidParameter,
			"container instance %q not found", containerInstance)
	}

	if !force && ci.RunningTasksCount > 0 {
		return nil, apiErrf(errors.FailedPrecondition, excInvalidParameter,
			"container instance %q has %d running task(s); use force to deregister", ci.EC2InstanceID, ci.RunningTasksCount)
	}

	// Force-deregistering an instance with running tasks stops those tasks, as
	// real ECS does — leaving them RUNNING on a deleted instance would strand
	// them. The instance is removed, so its capacity need not be returned.
	if force {
		m.stopTasksOnInstance(ci.ARN)
	}

	m.instances.Delete(ci.ARN)

	out := *ci
	out.Status = statusInactive

	return &out, nil
}

// stopTasksOnInstance marks every non-stopped task placed on the given instance
// STOPPED. The caller holds placeMu (so this must not call StopTask, which also
// takes it); capacity is not released because the instance is being removed.
// This is an administrative teardown, not a user StopTask, so it clears (rather
// than replaces) any launch-settle window a task may still be in: without this
// a task force-stopped mid-launch-transient would keep reporting its stale
// PROVISIONING/PENDING lastStatus instead of the STOPPED set here.
func (m *Mock) stopTasksOnInstance(instanceARN string) {
	for _, t := range m.tasks.SortedValues() {
		if t.ContainerInstanceARN != instanceARN || t.LastStatus == statusStopped {
			continue
		}

		updated := cloneTask(t)
		updated.LastStatus = statusStopped
		updated.DesiredStatus = statusStopped
		updated.StoppedReason = "Container instance deregistered."
		updated.StopCode = "TerminationNotice"

		for i := range updated.Containers {
			updated.Containers[i].LastStatus = statusStopped
		}

		m.tasks.Set(updated.ARN, &updated)
		m.taskSettle.Clear(updated.ARN)
	}
}

// UpdateContainerInstancesState sets each resolved instance to ACTIVE or
// DRAINING; unresolved ids become failures. Only ACTIVE and DRAINING are valid
// target states.
func (m *Mock) UpdateContainerInstancesState(
	_ context.Context, _ string, ids []string, status string,
) ([]driver.ContainerInstance, []driver.Failure, error) {
	if status != statusActive && status != statusDraining {
		return nil, nil, apiErrf(errors.InvalidArgument, excInvalidParameter,
			"container instance status must be ACTIVE or DRAINING, got %q", status)
	}

	// Hold placeMu for the whole read-modify-write: reserve (capacity.go),
	// release/StopTask, and DeregisterContainerInstance all mutate an instance
	// under placeMu, so resolving-then-Set without it would let a concurrent
	// capacity reserve be silently reverted (a logical lost update -race can't
	// see, since memstore.Set is per-key locked). Resolving under the lock also
	// guarantees the clone reflects the freshest capacity counts.
	m.placeMu.Lock()
	defer m.placeMu.Unlock()

	found := make([]driver.ContainerInstance, 0, len(ids))
	failures := make([]driver.Failure, 0, len(ids))

	for _, id := range ids {
		ci, ok := m.resolveInstance(id)
		if !ok {
			failures = append(failures, driver.Failure{ARN: id, Reason: "MISSING"})
			continue
		}

		// Copy-on-write: mutate a clone and Set it back so concurrent readers
		// never race the status write.
		updated := *ci
		updated.Status = status
		m.instances.Set(updated.ARN, &updated)

		found = append(found, updated)
	}

	return found, failures, nil
}

// capacityFromResources derives CPU units and memory (MiB) from the CPU and
// MEMORY resource entries, defaulting to the standard box when absent.
func capacityFromResources(resources []driver.Resource) (cpu, memory int) {
	cpu, memory = defaultInstanceCPU, defaultInstanceMemory

	for i := range resources {
		switch resources[i].Name {
		case "CPU":
			if v := resourceInt(&resources[i]); v > 0 {
				cpu = v
			}
		case "MEMORY":
			if v := resourceInt(&resources[i]); v > 0 {
				memory = v
			}
		}
	}

	return cpu, memory
}

// resourceInt reads the first non-zero integer-shaped value from a resource.
func resourceInt(r *driver.Resource) int {
	switch {
	case r.IntegerValue != 0:
		return r.IntegerValue
	case r.LongValue != 0:
		return int(r.LongValue)
	case r.DoubleValue != 0:
		return int(r.DoubleValue)
	default:
		return 0
	}
}

// instanceIDFromDoc extracts the instanceId from an EC2 instance identity
// document (a JSON object), returning "" if it is absent or unparseable.
func instanceIDFromDoc(doc string) string {
	if doc == "" {
		return ""
	}

	var parsed struct {
		InstanceID string `json:"instanceId"`
	}

	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		return ""
	}

	return parsed.InstanceID
}

// ListContainerInstances returns container instances in a cluster.
func (m *Mock) ListContainerInstances(_ context.Context, cluster string) ([]driver.ContainerInstance, error) {
	want := resolveClusterName(cluster)

	all := m.instances.SortedValues()

	out := make([]driver.ContainerInstance, 0, len(all))

	for _, ci := range all {
		if instanceClusterName(ci.ARN) == want {
			out = append(out, *ci)
		}
	}

	return out, nil
}

// DescribeContainerInstances resolves instances by id or ARN; unresolved ids
// become failures.
func (m *Mock) DescribeContainerInstances(_ context.Context, _ string, ids []string) (
	[]driver.ContainerInstance, []driver.Failure, error,
) {
	found := make([]driver.ContainerInstance, 0, len(ids))
	failures := make([]driver.Failure, 0, len(ids))

	for _, id := range ids {
		if ci, ok := m.resolveInstance(id); ok {
			found = append(found, *ci)
			continue
		}

		failures = append(failures, driver.Failure{ARN: id, Reason: "MISSING"})
	}

	return found, failures, nil
}

// resolveInstance looks up a container instance by full ARN or trailing id.
func (m *Mock) resolveInstance(id string) (*driver.ContainerInstance, bool) {
	if ci, ok := m.instances.Get(id); ok {
		return ci, true
	}

	for _, ci := range m.instances.All() {
		if id != "" && strings.HasSuffix(ci.ARN, "/"+id) {
			return ci, true
		}
	}

	return nil, false
}

// instanceClusterName extracts the cluster name from a container-instance ARN
// (…:container-instance/cluster/id).
func instanceClusterName(arn string) string {
	i := strings.LastIndex(arn, "container-instance/")
	if i < 0 {
		return ""
	}

	rest := arn[i+len("container-instance/"):]
	if j := strings.Index(rest, "/"); j >= 0 {
		return rest[:j]
	}

	return rest
}
