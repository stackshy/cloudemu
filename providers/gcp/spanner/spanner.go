// Package spanner provides an in-memory mock of the Google Cloud Spanner admin
// control plane (spanner.googleapis.com/v1). It models instances and databases
// and the long-running operations their mutating RPCs return. It is
// control-plane only: the SQL data plane, backups, custom instance configs, IAM,
// and database roles are out of scope.
package spanner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	spdriver "github.com/stackshy/cloudemu/v2/services/spanner/driver"
)

var _ spdriver.Spanner = (*Mock)(nil)

// Mock is the in-memory Spanner admin implementation. Stores are keyed by the
// resource's full GCP name (projects/{p}/instances/{i}[/databases/{d}]).
type Mock struct {
	mu sync.RWMutex

	instances  *memstore.Store[spdriver.Instance]
	databases  *memstore.Store[spdriver.Database]
	operations *memstore.Store[spdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Spanner admin mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		instances:  memstore.New[spdriver.Instance](),
		databases:  memstore.New[spdriver.Database](),
		operations: memstore.New[spdriver.Operation](),
		opts:       opts,
	}
}

// newOp records a completed operation whose name is scoped to target (the full
// resource name it acted on) and returns it. The caller holds the write lock.
func (m *Mock) newOp(scope, opType, target string) *spdriver.Operation {
	op := spdriver.Operation{
		Name:       fmt.Sprintf("%s/operations/spanner-%s-%d", scope, opType, m.opSeq.Add(1)),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

func copyLabels(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

func cloneInstance(in *spdriver.Instance) spdriver.Instance {
	i := *in
	i.Labels = copyLabels(i.Labels)

	return i
}

// deriveCapacity fills whichever of nodeCount / processingUnits is unset from the
// other, using Spanner's fixed 1 node = 1000 processing units ratio. When both
// are zero it leaves them zero (an autoscaling instance reports both as
// output-only, which is out of scope here).
func deriveCapacity(nodeCount, processingUnits int64) (nodes, units int64) {
	switch {
	case nodeCount > 0 && processingUnits == 0:
		return nodeCount, nodeCount * spdriver.ProcessingUnitsPerNode
	case processingUnits > 0 && nodeCount == 0:
		return processingUnits / spdriver.ProcessingUnitsPerNode, processingUnits
	default:
		return nodeCount, processingUnits
	}
}

// CreateInstance creates a Spanner instance, ready immediately.
func (m *Mock) CreateInstance(_ context.Context, cfg spdriver.CreateInstanceConfig) (*spdriver.Instance, *spdriver.Operation, error) {
	if cfg.Config == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "instance config is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.instances.Has(cfg.Name) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "instance %q already exists", cfg.Name)
	}

	nodes, units := deriveCapacity(cfg.NodeCount, cfg.ProcessingUnits)
	now := m.opts.Clock.Now().UTC()

	inst := spdriver.Instance{
		Name:            cfg.Name,
		Config:          cfg.Config,
		DisplayName:     orDefault(cfg.DisplayName, lastSegment(cfg.Name)),
		NodeCount:       nodes,
		ProcessingUnits: units,
		State:           spdriver.StateReady,
		Labels:          copyLabels(cfg.Labels),
		CreateTime:      now,
		UpdateTime:      now,
	}
	m.instances.Set(cfg.Name, inst)

	op := m.newOp(cfg.Name, "create-instance", cfg.Name)
	out := cloneInstance(&inst)

	return &out, op, nil
}

// GetInstance returns an instance by full name.
func (m *Mock) GetInstance(_ context.Context, name string) (*spdriver.Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	inst, ok := m.instances.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "instance %q not found", name)
	}

	out := cloneInstance(&inst)

	return &out, nil
}

// ListInstances returns every instance in a project.
func (m *Mock) ListInstances(_ context.Context, project string) ([]spdriver.Instance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/instances/"
	all := m.instances.SortedValues()
	out := make([]spdriver.Instance, 0, len(all))

	for i := range all {
		if strings.HasPrefix(all[i].Name, prefix) {
			out = append(out, cloneInstance(&all[i]))
		}
	}

	return out, nil
}

// UpdateInstance applies a masked update to an instance and returns the LRO.
func (m *Mock) UpdateInstance(
	_ context.Context, name string, cfg spdriver.UpdateInstanceConfig,
) (*spdriver.Instance, *spdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	inst, ok := m.instances.Get(name)
	if !ok {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "instance %q not found", name)
	}

	applyInstanceUpdate(&inst, cfg)
	inst.UpdateTime = m.opts.Clock.Now().UTC()
	m.instances.Set(name, inst)

	op := m.newOp(name, "update-instance", name)
	out := cloneInstance(&inst)

	return &out, op, nil
}

// applyInstanceUpdate mutates inst per cfg. With a field mask only the named
// fields are written (matching real Spanner, which requires an explicit mask);
// with no mask it falls back to presence heuristics for direct-driver callers.
func applyInstanceUpdate(inst *spdriver.Instance, cfg spdriver.UpdateInstanceConfig) {
	if len(cfg.FieldMask) == 0 {
		applyInstanceHeuristic(inst, cfg)
		return
	}

	if maskHas(cfg.FieldMask, "displayname") {
		inst.DisplayName = cfg.DisplayName
	}

	if maskHas(cfg.FieldMask, "labels") {
		inst.Labels = copyLabels(cfg.Labels)
	}

	if maskHas(cfg.FieldMask, "nodecount") || maskHas(cfg.FieldMask, "processingunits") {
		inst.NodeCount, inst.ProcessingUnits = deriveCapacity(cfg.NodeCount, cfg.ProcessingUnits)
	}
}

func applyInstanceHeuristic(inst *spdriver.Instance, cfg spdriver.UpdateInstanceConfig) {
	inst.DisplayName = orKeep(cfg.DisplayName, inst.DisplayName)

	if cfg.Labels != nil {
		inst.Labels = copyLabels(cfg.Labels)
	}

	if cfg.NodeCount > 0 || cfg.ProcessingUnits > 0 {
		inst.NodeCount, inst.ProcessingUnits = deriveCapacity(cfg.NodeCount, cfg.ProcessingUnits)
	}
}

func maskHas(mask []string, field string) bool {
	for _, p := range mask {
		if p == field {
			return true
		}
	}

	return false
}

// DeleteInstance removes an instance and cascade-deletes its databases.
func (m *Mock) DeleteInstance(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.instances.Has(name) {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", name)
	}

	dbPrefix := name + "/databases/"
	for _, k := range m.databases.Keys() {
		if strings.HasPrefix(k, dbPrefix) {
			m.databases.Delete(k)
		}
	}

	m.instances.Delete(name)

	return nil
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op id
// an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*spdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &spdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

func orKeep(v, cur string) string {
	if v == "" {
		return cur
	}

	return v
}

func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}
