// Package ecs provides an in-memory mock implementation of Amazon ECS
// (Elastic Container Service). It satisfies services/ecs/driver.ECS so the
// real aws-sdk-go-v2/service/ecs client works against it via the AWS server.
package ecs

import (
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// Compile-time check that Mock implements driver.ECS.
var _ driver.ECS = (*Mock)(nil)

const (
	statusActive   = "ACTIVE"
	statusInactive = "INACTIVE"
	statusRunning  = "RUNNING"
	statusPending  = "PENDING"
	statusStopped  = "STOPPED"
	defaultCluster = "default"
)

// Mock is an in-memory mock implementation of Amazon ECS.
type Mock struct {
	clusters   *memstore.Store[*driver.Cluster]
	taskDefs   *memstore.Store[*driver.TaskDefinition] // keyed by "family:revision"
	tasks      *memstore.Store[*driver.Task]           // keyed by task ARN
	services   *memstore.Store[*driver.Service]        // keyed by "cluster/name"
	instances  *memstore.Store[*driver.ContainerInstance]
	tags       *memstore.Store[[]driver.Tag]           // keyed by resource ARN
	settings   *memstore.Store[*driver.AccountSetting] // keyed by setting name
	attributes *memstore.Store[*driver.Attribute]      // keyed by targetId + "\x00" + name
	opts       *config.Options
	regMu      sync.Mutex // serializes task-definition revision allocation
	placeMu    sync.Mutex // serializes container-instance capacity reserve/release
	clusterMu  sync.Mutex // serializes CreateCluster name-reuse compare-and-set

	launcher ManagedInstanceLauncher // optional: provisions backing managed EC2 instances

	// engineHandles maps a task ARN to the config.ContainerEngine handle backing
	// it. A present entry is the "engine-backed" marker consulted by StopTask and
	// ExecuteCommand; absent means the task is a synthetic (engine-less) task.
	engineHandles *memstore.Store[string]

	logs logdriver.Logging // optional: awslogs surfacing target (CloudWatch Logs)
}

// ManagedInstanceLauncher provisions the managed EC2 instance that backs an ECS
// container instance, so #159 (ECS) and #300 (EC2 managed-resource visibility)
// compose: a registered container instance surfaces as a real EC2 instance
// carrying Operator.Managed=true / Principal "ecs.amazonaws.com". The AWS EC2
// Mock satisfies this; it is wired by the provider factory.
type ManagedInstanceLauncher interface {
	// LaunchManaged provisions a managed instance and returns its EC2 instance id.
	LaunchManaged(instanceType, principal string, tags map[string]string) (string, error)
}

// SetManagedInstanceLauncher wires the EC2-backed launcher used when a container
// instance is registered without an explicit EC2 instance id. Safe to leave
// unset — registration then just synthesizes an id.
func (m *Mock) SetManagedInstanceLauncher(l ManagedInstanceLauncher) {
	m.launcher = l
}

// New creates a new ECS mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters:      memstore.New[*driver.Cluster](),
		taskDefs:      memstore.New[*driver.TaskDefinition](),
		tasks:         memstore.New[*driver.Task](),
		services:      memstore.New[*driver.Service](),
		instances:     memstore.New[*driver.ContainerInstance](),
		tags:          memstore.New[[]driver.Tag](),
		settings:      memstore.New[*driver.AccountSetting](),
		attributes:    memstore.New[*driver.Attribute](),
		engineHandles: memstore.New[string](),
		opts:          opts,
	}
}

// SetLogSink wires the CloudWatch Logs target that engine-backed tasks push
// their captured container logs into when a container's LogConfiguration uses
// the awslogs driver. Safe to leave unset — log surfacing is then skipped.
func (m *Mock) SetLogSink(l logdriver.Logging) {
	m.logs = l
}

func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(time.RFC3339)
}

func (m *Mock) arn(resource string) string {
	return idgen.AWSARN("ecs", m.opts.Region, m.opts.AccountID, resource)
}

// rootPrincipalARN is the account-root IAM principal ECS records as a service's
// createdBy when the request carries no more specific caller identity.
func (m *Mock) rootPrincipalARN() string {
	return "arn:aws:iam::" + m.opts.AccountID + ":root"
}

// hexID returns a 32-character hex id with no dashes, matching the ECS
// resource-id shape used in task and container-instance ARNs. idgen.GenerateID
// emits 8 hex digits per call, so four calls yield the 32-char id.
func (*Mock) hexID() string {
	return idgen.GenerateID("") + idgen.GenerateID("") + idgen.GenerateID("") + idgen.GenerateID("")
}

// resolveClusterName accepts a cluster name or ARN and returns the bare name,
// defaulting to "default" when empty.
func resolveClusterName(id string) string {
	if id == "" {
		return defaultCluster
	}

	if i := strings.LastIndex(id, "cluster/"); i >= 0 {
		return id[i+len("cluster/"):]
	}

	return id
}

// clusterNameFromARN extracts the cluster name from a cluster ARN, or returns
// the input unchanged if it is not an ARN.
func clusterNameFromARN(arn string) string {
	if i := strings.LastIndex(arn, "cluster/"); i >= 0 {
		return arn[i+len("cluster/"):]
	}

	return arn
}

// familyFromTaskDefARN extracts the family from a task-definition ARN
// (…:task-definition/family:revision).
func familyFromTaskDefARN(arn string) string {
	i := strings.LastIndex(arn, "task-definition/")
	if i < 0 {
		return ""
	}

	rest := arn[i+len("task-definition/"):]
	if j := strings.LastIndex(rest, ":"); j >= 0 {
		return rest[:j]
	}

	return rest
}

func copyTags(in []driver.Tag) []driver.Tag {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.Tag, len(in))
	copy(out, in)

	return out
}

// clusterExists reports whether a cluster with the given bare name is present
// (in any state, including INACTIVE). The implicit "default" cluster is always
// treated as present, matching AWS. Used by resource-resolution paths (e.g.
// tagging) that must still see a deleted cluster.
func (m *Mock) clusterExists(name string) bool {
	return name == defaultCluster || m.clusters.Has(name)
}

// clusterActive reports whether a cluster is present AND ACTIVE. Launch paths
// (RunTask, CreateService) gate on this rather than clusterExists: DeleteCluster
// leaves an INACTIVE tombstone, and real ECS rejects new work against a deleted
// cluster with ClusterNotFoundException. The implicit "default" cluster, when it
// was never explicitly created, is treated as ACTIVE.
func (m *Mock) clusterActive(name string) bool {
	c, ok := m.clusters.Get(name)
	if !ok {
		return name == defaultCluster
	}

	return c.Status == statusActive
}

// clusterCounts computes the live resource counts for a cluster from the task,
// service, and container-instance stores. Computing on read keeps the counts
// correct without maintaining increment/decrement bookkeeping across every
// mutator. It is also the basis for the DeleteCluster cascade guard.
func (m *Mock) clusterCounts(name string) (activeServices, runningTasks, pendingTasks, instances int) {
	activeServices = m.countActiveServices(name)
	runningTasks, pendingTasks = m.countTasksByStatus(name)
	instances = m.countRegisteredInstances(name)

	return activeServices, runningTasks, pendingTasks, instances
}

func (m *Mock) countActiveServices(name string) int {
	var n int

	for _, s := range m.services.All() {
		if clusterNameFromARN(s.ClusterARN) == name && s.Status == statusActive {
			n++
		}
	}

	return n
}

func (m *Mock) countTasksByStatus(name string) (running, pending int) {
	for _, t := range m.tasks.All() {
		if clusterNameFromARN(t.ClusterARN) != name {
			continue
		}

		switch t.LastStatus {
		case statusRunning:
			running++
		case statusPending:
			pending++
		}
	}

	return running, pending
}

func (m *Mock) countRegisteredInstances(name string) int {
	var n int

	for _, ci := range m.instances.All() {
		if instanceClusterName(ci.ARN) == name && ci.Status == statusActive {
			n++
		}
	}

	return n
}
