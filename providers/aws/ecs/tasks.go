package ecs

import (
	"context"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/services/container/containerengine"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// maxRunTaskCount is the maximum number of tasks a single RunTask may launch,
// matching the AWS ECS limit. It also bounds the result-slice allocation.
const maxRunTaskCount = 10

// RunTask creates count tasks (default 1) from a task definition, branching on
// the launch type. EC2 (the default when launchType is empty) places each task
// onto a container instance with enough remaining capacity and reports a
// placement failure when none fits; FARGATE runs each task with a synthesized
// ENI attachment and no capacity pool; EXTERNAL places onto an external instance
// when one is present, else runs unplaced. An unresolved task definition and
// request-level validation errors (launch-type mismatch, missing Fargate
// networking) are synchronous errors, not placement failures.
//
//nolint:gocritic // in is passed by value to satisfy the driver.ECS interface; the copy is cheap for a mock.
func (m *Mock) RunTask(ctx context.Context, in driver.RunTaskInput) ([]driver.Task, []driver.Failure, error) {
	cluster := resolveClusterName(in.Cluster)

	// A task is a child of its cluster, so its cluster/task ARNs must carry the
	// cluster's region — the region the cluster was created in — not the region
	// this RunTask request happens to be signed for. Derive it from the stored
	// cluster's ARN; only the implicit (never-created) default cluster has no
	// stored ARN, so it falls back to the request region.
	region := regionctx.RegionOr(ctx, m.opts.Region)
	if c, ok := m.clusters.Get(cluster); ok {
		region = arnRegion(c.ARN, region)
	}

	clusterARN := m.arnIn(region, "cluster/"+cluster)

	if !m.clusterActive(cluster) {
		return nil, nil, apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", cluster)
	}

	// An unresolved or deregistered (INACTIVE) task definition is a synchronous
	// ClientException in real ECS, not a placement failure — failures[] is
	// reserved for capacity/placement.
	td, err := m.resolveLaunchableTaskDef(in.TaskDefinition)
	if err != nil {
		return nil, nil, err
	}

	count := in.Count
	if count <= 0 {
		count = 1
	}

	// AWS RunTask caps count at 10; reject anything larger (also bounds the
	// allocation below to a constant maximum).
	if count > maxRunTaskCount {
		return nil, nil, errors.Newf(errors.InvalidArgument, "count %d exceeds the maximum of %d", count, maxRunTaskCount)
	}

	launchType := effectiveLaunchType(&in)
	if err := validateLaunch(td, launchType, in.NetworkConfiguration); err != nil {
		return nil, nil, err
	}

	spec := taskSpec{
		cluster: cluster, clusterARN: clusterARN, td: td, launchType: launchType,
		group: in.Group, startedBy: in.StartedBy, platformVersion: in.PlatformVersion,
		netCfg: in.NetworkConfiguration, tags: in.Tags, runToCompletion: true,
	}

	// Capacity is the validated constant maximum, not the user value, so the
	// allocation size never depends on caller input.
	tasks := make([]driver.Task, 0, maxRunTaskCount)
	failures := make([]driver.Failure, 0, maxRunTaskCount)

	for range count {
		task, failure := m.launchTask(ctx, &spec, false)
		if failure != nil {
			failures = append(failures, *failure)
			continue
		}

		tasks = append(tasks, *task)
	}

	return tasks, failures, nil
}

// validateLaunch enforces the request-level rules that must hold before any task
// is placed: the task definition must support the requested launch type, and a
// FARGATE launch requires awsvpc networking (both on the task definition and in
// the request) plus task-level cpu and memory. It is shared by RunTask and the
// service scheduler.
func validateLaunch(td *driver.TaskDefinition, launchType string, netCfg *driver.NetworkConfiguration) error {
	if len(td.RequiresCompatibilities) > 0 && !containsLaunchType(td.RequiresCompatibilities, launchType) {
		return apiErrf(errors.InvalidArgument, excInvalidParameter,
			"task definition does not support the %s launch type", launchType)
	}

	if launchType != launchFargate {
		return nil
	}

	if td.NetworkMode != networkModeAwsvpc {
		return apiErrf(errors.InvalidArgument, excInvalidParameter,
			"Fargate requires the awsvpc network mode")
	}

	if !hasSubnets(netCfg) {
		return apiErrf(errors.InvalidArgument, excInvalidParameter,
			"Network Configuration must be provided when networkMode is awsvpc")
	}

	if td.CPU == "" || td.Memory == "" {
		return apiErrf(errors.InvalidArgument, excInvalidParameter,
			"Fargate requires task-level cpu and memory to be specified")
	}

	if !validFargateCPUMemory(td.CPU, td.Memory) {
		return apiErrf(errors.InvalidArgument, excInvalidParameter,
			"No Fargate configuration exists for given cpu (%s) and memory (%s) values", td.CPU, td.Memory)
	}

	return nil
}

// fargateMemRange is the supported task memory (MiB) for one Fargate task-cpu
// value: either an explicit set of sizes or an inclusive [min,max] with a fixed
// step between allowed sizes. The .25 vCPU (256) tier is the one non-uniform
// case, so it uses the explicit set.
type fargateMemRange struct {
	minMiB, maxMiB, step int
	explicit             []int
}

// fargateMemRanges maps a Fargate task cpu value (vCPU units) to its supported
// memory sizes, matching the configurations AWS Fargate allows.
//
//nolint:gochecknoglobals // static Fargate cpu→memory configuration table.
var fargateMemRanges = map[int]fargateMemRange{
	256:   {explicit: []int{512, 1024, 2048}},
	512:   {minMiB: 1024, maxMiB: 4096, step: 1024},
	1024:  {minMiB: 2048, maxMiB: 8192, step: 1024},
	2048:  {minMiB: 4096, maxMiB: 16384, step: 1024},
	4096:  {minMiB: 8192, maxMiB: 30720, step: 1024},
	8192:  {minMiB: 16384, maxMiB: 61440, step: 4096},
	16384: {minMiB: 32768, maxMiB: 122880, step: 8192},
}

// validFargateCPUMemory reports whether the task-level cpu/memory pair is a
// supported Fargate configuration. Real Fargate rejects any other pairing with a
// "No Fargate configuration exists for given values" error.
func validFargateCPUMemory(cpuStr, memStr string) bool {
	r, ok := fargateMemRanges[atoiSafe(cpuStr)]
	if !ok {
		return false
	}

	mem := atoiSafe(memStr)

	if len(r.explicit) > 0 {
		for _, v := range r.explicit {
			if v == mem {
				return true
			}
		}

		return false
	}

	return mem >= r.minMiB && mem <= r.maxMiB && (mem-r.minMiB)%r.step == 0
}

// hasSubnets reports whether the request carries an awsvpc configuration with at
// least one subnet.
func hasSubnets(nc *driver.NetworkConfiguration) bool {
	return nc != nil && nc.AwsVpcConfiguration != nil && len(nc.AwsVpcConfiguration.Subnets) > 0
}

// taskSpec is the resolved, launch-type-agnostic description of one task to
// place. It is the shared input to launchTask, used by both RunTask and the
// service scheduler.
type taskSpec struct {
	cluster         string
	clusterARN      string
	td              *driver.TaskDefinition
	launchType      string
	group           string
	startedBy       string
	platformVersion string
	netCfg          *driver.NetworkConfiguration
	tags            []driver.Tag
	// runToCompletion selects the engine run mode: standalone RunTask runs its
	// containers to completion (blocking for exit codes), while the service
	// scheduler launches them detached.
	runToCompletion bool
}

// launchTask builds a single task from spec and places it per the launch type,
// storing it and returning a clone. FARGATE always runs (unlimited pool);
// EXTERNAL runs unplaced when no instance fits. For EC2 with no fitting
// instance the behavior depends on pendingOnShortfall: RunTask (false) returns a
// placement failure and stores nothing, while the service scheduler (true)
// stores the task PENDING so the service reports RunningCount<DesiredCount.
func (m *Mock) launchTask(ctx context.Context, spec *taskSpec, pendingOnShortfall bool) (*driver.Task, *driver.Failure) {
	task := &driver.Task{
		ARN:               m.arnIn(arnRegion(spec.clusterARN, m.opts.Region), "task/"+spec.cluster+"/"+m.hexID()),
		ClusterARN:        spec.clusterARN,
		TaskDefinitionARN: spec.td.ARN,
		LaunchType:        spec.launchType,
		DesiredStatus:     statusRunning,
		Group:             spec.group,
		StartedBy:         spec.startedBy,
		CreatedAt:         m.now(),
		Containers:        m.containersFor(spec.td),
		Tags:              copyTags(spec.tags),
	}

	if spec.launchType == launchFargate {
		m.placeFargate(task, spec.netCfg, spec.platformVersion)
		m.backTaskWithEngine(ctx, task, spec)
		m.tasks.Set(task.ARN, task)
		clone := cloneTask(task)

		return &clone, nil
	}

	failure := m.placeOnInstance(task, spec.cluster, spec.clusterARN, spec.td, spec.launchType)
	if failure != nil {
		if !pendingOnShortfall {
			return nil, failure
		}

		markContainers(task, statusPending)
		task.LastStatus = statusPending
		m.tasks.Set(task.ARN, task)
		clone := cloneTask(task)

		return &clone, nil
	}

	task.LastStatus = statusRunning
	m.backTaskWithEngine(ctx, task, spec)
	m.tasks.Set(task.ARN, task)
	clone := cloneTask(task)

	return &clone, nil
}

// markContainers sets every container's last status. A container marked
// PENDING (EC2 capacity shortfall) never actually reserved a host port, so its
// speculative network bindings are cleared rather than reporting a phantom one.
func markContainers(task *driver.Task, status string) {
	for i := range task.Containers {
		task.Containers[i].LastStatus = status

		if status == statusPending {
			task.Containers[i].NetworkBindings = nil
		}
	}
}

// placeFargate marks a Fargate task RUNNING, echoes/normalizes its platform
// version (LATEST or empty resolves to 1.4.0), and synthesizes an elastic
// network interface attachment. Fargate has no capacity pool (treated as
// unlimited), so placement never fails.
func (m *Mock) placeFargate(task *driver.Task, netCfg *driver.NetworkConfiguration, platformVersion string) {
	task.LastStatus = statusRunning
	task.PlatformVersion = fargatePlatformVersion(platformVersion)
	task.Attachments = []driver.Attachment{m.syntheticENI(netCfg)}
	m.tasks.Set(task.ARN, task)
}

// fargatePlatformVersion resolves the effective platform version: an explicit
// value passes through, while empty or "LATEST" resolves to a concrete version.
func fargatePlatformVersion(requested string) string {
	if requested == "" || requested == "LATEST" {
		return fargatePlatformLate
	}

	return requested
}

// syntheticENI builds an ElasticNetworkInterface attachment with a private IPv4
// address and (when available) the first requested subnet.
func (m *Mock) syntheticENI(nc *driver.NetworkConfiguration) driver.Attachment {
	subnet := ""
	if nc != nil && nc.AwsVpcConfiguration != nil && len(nc.AwsVpcConfiguration.Subnets) > 0 {
		subnet = nc.AwsVpcConfiguration.Subnets[0]
	}

	id := m.hexID()

	return driver.Attachment{
		Type:   "ElasticNetworkInterface",
		Status: "ATTACHED",
		Details: []driver.KeyValue{
			{Name: "networkInterfaceId", Value: "eni-" + id[:17]},
			{Name: "privateIPv4Address", Value: "10.0.0." + strconv.Itoa(int(id[0])%254+1)},
			{Name: "subnetId", Value: subnet},
			{Name: "macAddress", Value: "0a:58:0a:00:00:01"},
		},
	}
}

// placeOnInstance reserves capacity for an EC2 or EXTERNAL task. EC2 requires a
// fitting container instance and returns a placement failure otherwise; EXTERNAL
// places onto an external instance when one fits but otherwise runs unplaced (no
// failure), modeling the anywhere-hosted external launch type.
func (m *Mock) placeOnInstance(
	task *driver.Task, cluster, clusterARN string, td *driver.TaskDefinition, launchType string,
) *driver.Failure {
	cpu, memory := requiredResources(td)

	arn, reason := m.reserve(cluster, cpu, memory)
	if arn != "" {
		task.ContainerInstanceARN = arn

		return nil
	}

	if launchType == launchExternal {
		return nil
	}

	failure := &driver.Failure{ARN: clusterARN, Reason: reason}
	if reason == reasonNoInstances {
		failure.Detail = noInstancesDetail
	}

	return failure
}

// containersFor builds the RUNNING containers for a newly launched task,
// resolving each container's bridge/host network bindings (awsvpc/Fargate
// tasks carry none — traffic reaches the container directly through its ENI).
func (m *Mock) containersFor(td *driver.TaskDefinition) []driver.Container {
	out := make([]driver.Container, 0, len(td.ContainerDefinitions))

	for i := range td.ContainerDefinitions {
		cd := &td.ContainerDefinitions[i]
		out = append(out, driver.Container{
			Name:            cd.Name,
			Image:           cd.Image,
			LastStatus:      statusRunning,
			NetworkBindings: m.networkBindingsFor(td.NetworkMode, cd.PortMappings),
		})
	}

	return out
}

// defaultProtocol is the ECS network-binding protocol assumed when a port
// mapping leaves Protocol unset, matching the AWS default.
const defaultProtocol = "tcp"

// networkBindingsFor resolves the host IP/port ECS binds for each container
// port mapping under the task's network mode. Host mode always binds the host
// port to the same value as the container port; bridge mode uses the caller's
// explicit hostPort or, when left 0, a dynamically assigned one. awsvpc mode
// (Fargate or EC2 trunking) carries no bindings — the container's ENI IP is
// addressed directly.
func (m *Mock) networkBindingsFor(networkMode string, mappings []driver.PortMapping) []driver.NetworkBinding {
	if networkMode == networkModeAwsvpc || len(mappings) == 0 {
		return nil
	}

	out := make([]driver.NetworkBinding, 0, len(mappings))

	for _, pm := range mappings {
		hostPort := pm.HostPort

		switch {
		case networkMode == networkModeHost:
			hostPort = pm.ContainerPort
		case hostPort == 0:
			hostPort = m.nextEphemeralPort()
		}

		protocol := pm.Protocol
		if protocol == "" {
			protocol = defaultProtocol
		}

		out = append(out, driver.NetworkBinding{
			BindIP:        "0.0.0.0",
			ContainerPort: pm.ContainerPort,
			HostPort:      hostPort,
			Protocol:      protocol,
		})
	}

	return out
}

// StopTask marks a task STOPPED, releasing any container-instance capacity it
// reserved back to the instance. Releasing is guarded by placeMu (shared with
// placement) and skipped for an already-stopped task, so a repeated StopTask can
// never double-credit an instance.
func (m *Mock) StopTask(ctx context.Context, cluster, task, reason string) (*driver.Task, error) {
	m.placeMu.Lock()
	defer m.placeMu.Unlock()

	// The cluster scopes the task lookup: a missing cluster is
	// ClusterNotFoundException, matching real ECS (StopTask lists
	// ClusterNotFoundException + InvalidParameterException).
	want := resolveClusterName(cluster)
	if !m.clusterExists(want) {
		return nil, apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", want)
	}

	// A task that resolves but lives in a different cluster is not visible to this
	// StopTask, same as a task that does not exist at all: InvalidParameterException.
	t, ok := m.resolveTask(task)
	if !ok || clusterNameFromARN(t.ClusterARN) != want {
		return nil, apiErrf(errors.NotFound, excInvalidParameter, "task %q not found", task)
	}

	// Tear down the backing engine workload (if any) before flipping to STOPPED,
	// then drop the handle so a repeated StopTask is a no-op.
	if handle, backed := m.taskHandle(t.ARN); backed {
		_ = containerengine.Stop(ctx, m.opts.ContainerEngine, handle)
		m.engineHandles.Delete(t.ARN)
	}

	// Release reserved capacity exactly once, before flipping the task to STOPPED.
	if t.LastStatus != statusStopped && t.ContainerInstanceARN != "" {
		if td, tdOK := m.resolveTaskDef(t.TaskDefinitionARN); tdOK {
			cpu, memory := requiredResources(td)
			m.release(t.ContainerInstanceARN, cpu, memory)
		}
	}

	// Copy-on-write: mutate a clone (with its own Containers backing array) and
	// Set it back, never mutating the stored record a concurrent reader may hold.
	updated := cloneTask(t)
	updated.LastStatus = statusStopped
	updated.DesiredStatus = statusStopped
	updated.StoppedReason = reason
	updated.StopCode = "UserInitiated"

	for i := range updated.Containers {
		updated.Containers[i].LastStatus = statusStopped
	}

	m.tasks.Set(updated.ARN, &updated)

	out := cloneTask(&updated)

	return &out, nil
}

// ListTasks returns tasks in a cluster, optionally filtered by family, desired
// status, and service name. The service filter matches a task's group against
// the "service:<name>" convention the service scheduler tags tasks with.
func (m *Mock) ListTasks(_ context.Context, cluster, family, desiredStatus, serviceName string) ([]driver.Task, error) {
	want := resolveClusterName(cluster)

	if !m.clusterExists(want) {
		return nil, apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", want)
	}

	group := serviceGroup(serviceName)
	all := m.tasks.SortedValues()

	out := make([]driver.Task, 0, len(all))

	for _, t := range all {
		if clusterNameFromARN(t.ClusterARN) != want {
			continue
		}

		if family != "" && familyFromTaskDefARN(t.TaskDefinitionARN) != family {
			continue
		}

		if desiredStatus != "" && t.DesiredStatus != desiredStatus {
			continue
		}

		if group != "" && t.Group != group {
			continue
		}

		out = append(out, cloneTask(t))
	}

	return out, nil
}

// DescribeTasks resolves tasks by id or ARN; unresolved ids become failures. A
// nonexistent cluster is rejected up front with ClusterNotFoundException,
// matching real ECS (the implicit "default" cluster always resolves).
// DescribeTasks is cluster-scoped: a task that resolves but belongs to a
// different cluster than the requested one is reported as a MISSING failure,
// never as a found task, matching real ECS and the sibling StopTask/ListTasks.
func (m *Mock) DescribeTasks(_ context.Context, cluster string, ids []string) ([]driver.Task, []driver.Failure, error) {
	want := resolveClusterName(cluster)
	if !m.clusterExists(want) {
		return nil, nil, apiErrf(errors.NotFound, excClusterNotFound, "cluster %q not found", want)
	}

	found := make([]driver.Task, 0, len(ids))
	failures := make([]driver.Failure, 0, len(ids))

	for _, id := range ids {
		if t, ok := m.resolveTask(id); ok && clusterNameFromARN(t.ClusterARN) == want {
			found = append(found, cloneTask(t))
			continue
		}

		failures = append(failures, driver.Failure{ARN: id, Reason: "MISSING"})
	}

	return found, failures, nil
}

// resolveTask looks up a task by full ARN or bare 32-hex id.
func (m *Mock) resolveTask(id string) (*driver.Task, bool) {
	if t, ok := m.tasks.Get(id); ok {
		return t, true
	}

	// Match by trailing id segment when a bare id was supplied.
	for _, t := range m.tasks.All() {
		if id != "" && strings.HasSuffix(t.ARN, "/"+id) {
			return t, true
		}
	}

	return nil, false
}
