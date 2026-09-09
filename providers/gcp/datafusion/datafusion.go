// Package datafusion provides an in-memory mock of the Google Cloud Data Fusion
// control plane (datafusion.googleapis.com/v1). It models instances and the
// long-running operations their mutating RPCs return, plus the :restart custom
// verb and the instance lifecycle state machine. It is control-plane only: the
// CDAP pipeline/data plane, DNS peerings, and IAM policy verbs are out of scope.
package datafusion

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	dfdriver "github.com/stackshy/cloudemu/v2/services/datafusion/driver"
)

var _ dfdriver.DataFusion = (*Mock)(nil)

const instancesColl = "instances"

// Mock is the in-memory Data Fusion control-plane implementation. Instances are
// keyed by their full GCP resource name.
type Mock struct {
	mu sync.RWMutex

	instances  *memstore.Store[dfdriver.Resource]
	operations *memstore.Store[dfdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Data Fusion mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		instances:  memstore.New[dfdriver.Resource](),
		operations: memstore.New[dfdriver.Operation](),
		opts:       opts,
	}
}

// instanceName builds the full resource name of an instance.
func instanceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + instancesColl + "/" + id
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *dfdriver.Operation {
	scope := "projects/" + project + "/locations/" + location
	op := dfdriver.Operation{
		Name:       fmt.Sprintf("%s/operations/operation-%d-%s", scope, m.opSeq.Add(1), idgen.UUID()),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// CreateInstance provisions a new instance. It settles synchronously to ACTIVE
// (a real create takes ~20min and lands ACTIVE; the emulator must land ACTIVE or
// a Terraform apply hangs / drifts).
func (m *Mock) CreateInstance(_ context.Context, cfg *dfdriver.Config) (
	*dfdriver.Resource, *dfdriver.Operation, error,
) {
	if cfg.ID == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "instance id is required")
	}

	if cfg.Location == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "location is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := instanceName(cfg.Project, cfg.Location, cfg.ID)
	if m.instances.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "instance %q already exists", cfg.ID)
	}

	now := m.opts.Clock.Now().UTC()
	res := dfdriver.Resource{
		Project:    cfg.Project,
		Location:   cfg.Location,
		ID:         cfg.ID,
		State:      dfdriver.StateActive,
		CreateTime: now,
		UpdateTime: now,
		Fields:     cloneRawMap(cfg.Fields),
	}
	m.instances.Set(key, res)

	op := m.newOp(cfg.Project, cfg.Location, "create", key)
	out := cloneResource(&res)

	return &out, op, nil
}

// GetInstance returns an instance by identity, cloned.
func (m *Mock) GetInstance(_ context.Context, project, location, id string) (*dfdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := m.instances.Get(instanceName(project, location, id))
	if !ok {
		return nil, notFoundErr(project, location, id)
	}

	out := cloneResource(&r)

	return &out, nil
}

// ListInstances returns every instance in a project+location, ordered by name.
func (m *Mock) ListInstances(_ context.Context, project, location string) ([]dfdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + instancesColl + "/"
	all := m.instances.SortedValues()
	out := make([]dfdriver.Resource, 0, len(all))

	for i := range all {
		key := instanceName(all[i].Project, all[i].Location, all[i].ID)
		if strings.HasPrefix(key, prefix) {
			out = append(out, cloneResource(&all[i]))
		}
	}

	return out, nil
}

// PatchInstance applies a masked update and returns the completed LRO. Only the
// masked top-level body fields are written; a field outside the mask is left
// untouched. An empty mask replaces every field present in the request body.
func (m *Mock) PatchInstance(_ context.Context, cfg *dfdriver.Config, mask []string) (
	*dfdriver.Resource, *dfdriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := instanceName(cfg.Project, cfg.Location, cfg.ID)

	r, ok := m.instances.Get(key)
	if !ok {
		return nil, nil, notFoundErr(cfg.Project, cfg.Location, cfg.ID)
	}

	applyMask(&r, cfg.Fields, mask)
	r.UpdateTime = m.opts.Clock.Now().UTC()
	m.instances.Set(key, r)

	op := m.newOp(cfg.Project, cfg.Location, "update", key)
	out := cloneResource(&r)

	return &out, op, nil
}

// DeleteInstance removes an instance and returns the completed LRO.
func (m *Mock) DeleteInstance(_ context.Context, project, location, id string) (*dfdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := instanceName(project, location, id)
	if !m.instances.Has(key) {
		return nil, notFoundErr(project, location, id)
	}

	m.instances.Delete(key)

	return m.newOp(project, location, "delete", key), nil
}

// RestartInstance runs an ACTIVE instance through RESTARTING back to ACTIVE. A
// restart of a non-ACTIVE instance is rejected with FAILED_PRECONDITION; of a
// missing instance, NOT_FOUND — matching the real API's state machine.
func (m *Mock) RestartInstance(_ context.Context, project, location, id string) (
	*dfdriver.Resource, *dfdriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := instanceName(project, location, id)

	r, ok := m.instances.Get(key)
	if !ok {
		return nil, nil, notFoundErr(project, location, id)
	}

	if err := restartAllowed(r.State); err != nil {
		return nil, nil, err
	}

	// The instance passes through RESTARTING and settles back to ACTIVE. The
	// emulator completes synchronously, so the stored, observable state is the
	// settled ACTIVE; only UpdateTime advances.
	r.State = dfdriver.StateActive
	r.UpdateTime = m.opts.Clock.Now().UTC()
	m.instances.Set(key, r)

	op := m.newOp(project, location, "restart", key)
	out := cloneResource(&r)

	return &out, op, nil
}

// restartAllowed reports whether an instance in the given state may be
// restarted. Only an ACTIVE instance may; a CREATING/DELETING/RESTARTING/FAILED
// instance is rejected with FAILED_PRECONDITION, as the real API does.
func restartAllowed(state string) error {
	if state == dfdriver.StateActive {
		return nil
	}

	return cerrors.Newf(cerrors.FailedPrecondition,
		"instance is in state %s and cannot be restarted (must be ACTIVE)", state)
}

// Owns reports whether this store holds the named instance.
func (m *Mock) Owns(project, location, id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.instances.Has(instanceName(project, location, id))
}

// OwnsAnyIn reports whether this store holds any instance in the scope.
func (m *Mock) OwnsAnyIn(project, location string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + instancesColl + "/"
	for _, r := range m.instances.SortedValues() {
		if strings.HasPrefix(instanceName(r.Project, r.Location, r.ID), prefix) {
			return true
		}
	}

	return false
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op
// id an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*dfdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &dfdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// notFoundErr builds the NOT_FOUND error carrying the full resource name, as
// real Data Fusion does for a verb on a missing instance.
func notFoundErr(project, location, id string) error {
	return cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceName(project, location, id))
}
