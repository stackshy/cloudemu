// Package containerinstances provides an in-memory mock of Azure Container
// Instances (Microsoft.ContainerInstance/containerGroups). When a
// config.ContainerEngine is wired it runs the group's containers for real via
// the shared containerengine helper — mirroring how providers/aws/ecs backs its
// tasks — and reflects the engine's observed states and exit codes into the
// group's instanceView. With no engine configured it stays fully synthetic.
package containerinstances

import (
	"context"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/container/containerengine"
	"github.com/stackshy/cloudemu/v2/services/containerinstances/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// Compile-time check that Mock implements driver.ContainerInstances.
var _ driver.ContainerInstances = (*Mock)(nil)

const (
	// restartPolicyNever is the ACI restart policy under which the group's
	// containers run to completion instead of staying up.
	restartPolicyNever = "Never"

	provisioningStateSucceeded = "Succeeded"

	// Group-level instanceView states.
	groupStateRunning   = "Running"
	groupStateSucceeded = "Succeeded"

	// Per-container instanceView.currentState states (ACI vocabulary).
	containerStateRunning    = "Running"
	containerStateTerminated = "Terminated"

	// Engine container states surfaced by config.ContainerEngine.
	engineStateRunning = "running"
	engineStateExited  = "exited"
)

// groupData is a stored container group plus its engine linkage.
type groupData struct {
	group  driver.ContainerGroup
	handle string // config.ContainerEngine handle; empty when not engine-backed
	// engineBacked marks a group whose containers were run on a
	// config.ContainerEngine, so logs and teardown route to the engine.
	engineBacked bool
}

// Mock is an in-memory Azure Container Instances implementation.
type Mock struct {
	groups *memstore.Store[*groupData]
	opts   *config.Options
	mu     sync.Mutex // serializes create-or-update replace of an existing group
}

// New creates a new Azure Container Instances mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		groups: memstore.New[*groupData](),
		opts:   opts,
	}
}

// CreateContainerGroup creates the group, replacing any existing group of the
// same name (ARM PUT is create-or-update). When an engine is wired the
// containers run for real and their observed state is reflected back.
//
//nolint:gocritic // cfg is the by-value config defined by the driver interface.
func (m *Mock) CreateContainerGroup(ctx context.Context, cfg driver.ContainerGroupConfig) (*driver.ContainerGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Replacing an existing group tears down its prior engine workload first.
	if prev, ok := m.groups.Get(cfg.Name); ok {
		m.stopWorkload(ctx, prev)
	}

	group := driver.ContainerGroup{
		Name:              cfg.Name,
		Location:          cfg.Location,
		OSType:            cfg.OSType,
		RestartPolicy:     cfg.RestartPolicy,
		ProvisioningState: provisioningStateSucceeded,
		State:             groupStateRunning,
		Containers:        synthContainers(cfg.Containers),
		Tags:              cfg.Tags,
		Scope:             cfg.Scope,
	}

	data := &groupData{group: group}
	if err := m.backWithEngine(ctx, &cfg, data); err != nil {
		return nil, err
	}

	m.groups.Set(cfg.Name, data)

	out := data.group

	return &out, nil
}

// backWithEngine runs the group's containers on the configured ContainerEngine
// (when one is wired) and reflects the observed per-container state and group
// state back onto data. It is a no-op when no engine is configured, leaving the
// synthetic Running state in place.
func (m *Mock) backWithEngine(ctx context.Context, cfg *driver.ContainerGroupConfig, data *groupData) error {
	spec := config.ContainerRunSpec{
		Name:            cfg.Name,
		Containers:      engineContainers(cfg.Containers),
		RunToCompletion: cfg.RestartPolicy == restartPolicyNever,
	}

	handle, statuses, err := containerengine.Run(ctx, m.opts.ContainerEngine, spec)
	if err != nil {
		return cerrors.Newf(cerrors.Internal, "run container group %q: %v", cfg.Name, err)
	}

	if handle == "" {
		return nil
	}

	data.handle = handle
	data.engineBacked = true
	applyStatuses(&data.group, statuses, m.opts.Clock.Now())

	return nil
}

// GetContainerGroup returns the recorded group.
func (m *Mock) GetContainerGroup(_ context.Context, name string) (*driver.ContainerGroup, error) {
	data, ok := m.groups.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container group %q not found", name)
	}

	out := data.group

	return &out, nil
}

// DeleteContainerGroup removes the group, tearing down any engine workload.
func (m *Mock) DeleteContainerGroup(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, ok := m.groups.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container group %q not found", name)
	}

	m.stopWorkload(ctx, data)
	m.groups.Delete(name)

	return nil
}

// ListContainerGroups returns the groups visible under filter.
func (m *Mock) ListContainerGroups(_ context.Context, filter scope.Scope) ([]driver.ContainerGroup, error) {
	all := m.groups.All()
	out := make([]driver.ContainerGroup, 0, len(all))

	for _, data := range all {
		if data.group.Scope.Matches(filter) {
			out = append(out, data.group)
		}
	}

	return out, nil
}

// ContainerLogs returns the engine-captured output for one container. It is
// empty for a group that never ran on an engine.
func (m *Mock) ContainerLogs(ctx context.Context, group, container string, tail int) (string, error) {
	data, ok := m.groups.Get(group)
	if !ok {
		return "", cerrors.Newf(cerrors.NotFound, "container group %q not found", group)
	}

	if !data.engineBacked {
		return "", nil
	}

	return containerengine.Logs(ctx, m.opts.ContainerEngine, data.handle, container, tail)
}

// stopWorkload tears down an engine-backed group's containers. It is a no-op for
// a synthetic group.
func (m *Mock) stopWorkload(ctx context.Context, data *groupData) {
	if !data.engineBacked {
		return
	}

	_ = containerengine.Stop(ctx, m.opts.ContainerEngine, data.handle)
}
