// Package networkconnectivity provides an in-memory mock of the Google Network
// Connectivity Center control plane (networkconnectivity.googleapis.com/v1). It
// models hubs (global) and spokes (regional) and the long-running operations
// their mutating RPCs return. It is control-plane only: groups, route tables,
// routes, policy-based routes, IAM verbs, and any real data-plane connectivity
// are out of scope.
package networkconnectivity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	nccdriver "github.com/stackshy/cloudemu/v2/services/networkconnectivity/driver"
)

var _ nccdriver.NetworkConnectivity = (*Mock)(nil)

const (
	hubsColl   = "hubs"
	spokesColl = "spokes"

	// activeState is the lifecycle state a hub and a linked spoke are minted
	// with. Real Network Connectivity Center auto-activates a hub and a VPC spoke,
	// so a stable ACTIVE lets a Terraform refresh reconcile clean.
	activeState = "ACTIVE"
)

// Mock is the in-memory Network Connectivity Center control-plane
// implementation. Both resource collections are keyed by their full GCP resource
// name.
type Mock struct {
	mu sync.RWMutex

	hubs       *memstore.Store[nccdriver.Resource]
	spokes     *memstore.Store[nccdriver.Resource]
	operations *memstore.Store[nccdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Network Connectivity Center mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		hubs:       memstore.New[nccdriver.Resource](),
		spokes:     memstore.New[nccdriver.Resource](),
		operations: memstore.New[nccdriver.Operation](),
		opts:       opts,
	}
}

// resourceName builds the full resource name for a collection.
func resourceName(coll, project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + coll + "/" + id
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *nccdriver.Operation {
	scope := "projects/" + project + "/locations/" + location
	op := nccdriver.Operation{
		Name:       fmt.Sprintf("%s/operations/operation-%d-%s", scope, m.opSeq.Add(1), idgen.UUID()),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// create provisions a new resource in the given collection with computed output
// fields (uniqueId, state, createTime, updateTime) minted deterministically, and
// returns the completed LRO.
func (m *Mock) create(store *memstore.Store[nccdriver.Resource], coll string, cfg *nccdriver.Config) (
	*nccdriver.Resource, *nccdriver.Operation, error,
) {
	if cfg.ID == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, coll+" id is required")
	}

	if cfg.Location == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "location is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(coll, cfg.Project, cfg.Location, cfg.ID)
	if store.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "%s %q already exists", coll, cfg.ID)
	}

	now := m.opts.Clock.Now().UTC()
	res := nccdriver.Resource{
		Project:    cfg.Project,
		Location:   cfg.Location,
		ID:         cfg.ID,
		UniqueID:   idgen.UUID(),
		State:      activeState,
		CreateTime: now,
		UpdateTime: now,
		Fields:     cloneRawMap(cfg.Fields),
	}
	store.Set(key, res)

	op := m.newOp(cfg.Project, cfg.Location, "create", key)
	out := cloneResource(&res)

	return &out, op, nil
}

// get returns a resource by its identity, cloned.
func (m *Mock) get(store *memstore.Store[nccdriver.Resource], coll, project, location, id string) (
	*nccdriver.Resource, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := store.Get(resourceName(coll, project, location, id))
	if !ok {
		return nil, notFoundErr(coll, project, location, id)
	}

	out := cloneResource(&r)

	return &out, nil
}

// list returns every resource in a project+location, ordered by resource name.
func (m *Mock) list(store *memstore.Store[nccdriver.Resource], coll, project, location string) (
	[]nccdriver.Resource, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + coll + "/"
	all := store.SortedValues()
	out := make([]nccdriver.Resource, 0, len(all))

	for i := range all {
		key := resourceName(coll, all[i].Project, all[i].Location, all[i].ID)
		if strings.HasPrefix(key, prefix) {
			out = append(out, cloneResource(&all[i]))
		}
	}

	return out, nil
}

// patch applies a masked update and returns the completed LRO. Only the masked
// top-level body fields are written; a field outside the mask is left untouched.
// An empty mask replaces every field present in the request body. The computed
// output fields (uniqueId, state, createTime) are never mutated; updateTime is
// bumped.
func (m *Mock) patch(store *memstore.Store[nccdriver.Resource], coll string, cfg *nccdriver.Config, mask []string) (
	*nccdriver.Resource, *nccdriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(coll, cfg.Project, cfg.Location, cfg.ID)

	r, ok := store.Get(key)
	if !ok {
		return nil, nil, notFoundErr(coll, cfg.Project, cfg.Location, cfg.ID)
	}

	applyMask(&r, cfg.Fields, mask)
	r.UpdateTime = m.opts.Clock.Now().UTC()
	store.Set(key, r)

	op := m.newOp(cfg.Project, cfg.Location, "update", key)
	out := cloneResource(&r)

	return &out, op, nil
}

// del removes a resource and returns the completed LRO.
func (m *Mock) del(store *memstore.Store[nccdriver.Resource], coll, project, location, id string) (
	*nccdriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(coll, project, location, id)
	if !store.Has(key) {
		return nil, notFoundErr(coll, project, location, id)
	}

	store.Delete(key)

	return m.newOp(project, location, "delete", key), nil
}

// CreateHub provisions a new hub.
func (m *Mock) CreateHub(_ context.Context, cfg *nccdriver.Config) (*nccdriver.Resource, *nccdriver.Operation, error) {
	return m.create(m.hubs, hubsColl, cfg)
}

// GetHub returns a hub by identity.
func (m *Mock) GetHub(_ context.Context, project, location, id string) (*nccdriver.Resource, error) {
	return m.get(m.hubs, hubsColl, project, location, id)
}

// ListHubs returns every hub in a project+location.
func (m *Mock) ListHubs(_ context.Context, project, location string) ([]nccdriver.Resource, error) {
	return m.list(m.hubs, hubsColl, project, location)
}

// PatchHub applies a masked update to a hub.
func (m *Mock) PatchHub(_ context.Context, cfg *nccdriver.Config, mask []string) (
	*nccdriver.Resource, *nccdriver.Operation, error,
) {
	return m.patch(m.hubs, hubsColl, cfg, mask)
}

// DeleteHub removes a hub.
func (m *Mock) DeleteHub(_ context.Context, project, location, id string) (*nccdriver.Operation, error) {
	return m.del(m.hubs, hubsColl, project, location, id)
}

// CreateSpoke provisions a new spoke.
func (m *Mock) CreateSpoke(_ context.Context, cfg *nccdriver.Config) (*nccdriver.Resource, *nccdriver.Operation, error) {
	return m.create(m.spokes, spokesColl, cfg)
}

// GetSpoke returns a spoke by identity.
func (m *Mock) GetSpoke(_ context.Context, project, location, id string) (*nccdriver.Resource, error) {
	return m.get(m.spokes, spokesColl, project, location, id)
}

// ListSpokes returns every spoke in a project+location.
func (m *Mock) ListSpokes(_ context.Context, project, location string) ([]nccdriver.Resource, error) {
	return m.list(m.spokes, spokesColl, project, location)
}

// PatchSpoke applies a masked update to a spoke.
func (m *Mock) PatchSpoke(_ context.Context, cfg *nccdriver.Config, mask []string) (
	*nccdriver.Resource, *nccdriver.Operation, error,
) {
	return m.patch(m.spokes, spokesColl, cfg, mask)
}

// DeleteSpoke removes a spoke.
func (m *Mock) DeleteSpoke(_ context.Context, project, location, id string) (*nccdriver.Operation, error) {
	return m.del(m.spokes, spokesColl, project, location, id)
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op
// id an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*nccdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &nccdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// notFoundErr builds the NOT_FOUND error carrying the full resource name, as
// real Network Connectivity Center does for a Get/Patch/Delete of a missing
// resource.
func notFoundErr(coll, project, location, id string) error {
	return cerrors.Newf(cerrors.NotFound, "%s %q not found", coll, resourceName(coll, project, location, id))
}

// applyMask folds the masked top-level body fields from desired into the stored
// resource. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every
// field present in desired (lenient full-body update).
func applyMask(r *nccdriver.Resource, desired map[string]json.RawMessage, mask []string) {
	if r.Fields == nil {
		r.Fields = map[string]json.RawMessage{}
	}

	if len(mask) == 0 {
		for k, v := range desired {
			r.Fields[k] = append(json.RawMessage(nil), v...)
		}

		return
	}

	for _, path := range mask {
		field := path
		if i := strings.IndexByte(path, '.'); i >= 0 {
			field = path[:i]
		}

		if v, ok := desired[field]; ok {
			r.Fields[field] = append(json.RawMessage(nil), v...)
			continue
		}

		delete(r.Fields, field)
	}
}
