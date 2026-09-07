// Package cloudids provides an in-memory mock of the Google Cloud IDS control
// plane (ids.googleapis.com/v1). It models endpoints and the long-running
// operations their mutating RPCs return. It is control-plane only: there is no
// data plane (no packet mirroring, intrusion detection, or threat alerts).
package cloudids

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
	idsdriver "github.com/stackshy/cloudemu/v2/services/cloudids/driver"
)

var _ idsdriver.CloudIDs = (*Mock)(nil)

const endpointsColl = "endpoints"

// Mock is the in-memory Cloud IDS control-plane implementation. Each endpoint is
// keyed by its full GCP resource name.
type Mock struct {
	mu sync.RWMutex

	endpoints  *memstore.Store[idsdriver.Resource]
	operations *memstore.Store[idsdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Cloud IDS mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		endpoints:  memstore.New[idsdriver.Resource](),
		operations: memstore.New[idsdriver.Operation](),
		opts:       opts,
	}
}

// resourceName builds the full endpoint resource name.
func resourceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + endpointsColl + "/" + id
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *idsdriver.Operation {
	scope := "projects/" + project + "/locations/" + location
	op := idsdriver.Operation{
		Name:       fmt.Sprintf("%s/operations/operation-%d-%s", scope, m.opSeq.Add(1), idgen.UUID()),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// CreateEndpoint provisions a new endpoint with computed output fields carried
// verbatim (already seeded by the wire layer) and returns the completed LRO.
func (m *Mock) CreateEndpoint(_ context.Context, cfg *idsdriver.Config) (
	*idsdriver.Resource, *idsdriver.Operation, error,
) {
	if cfg.ID == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "endpoint id is required")
	}

	if cfg.Location == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "location is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(cfg.Project, cfg.Location, cfg.ID)
	if m.endpoints.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "endpoint %q already exists", cfg.ID)
	}

	now := m.opts.Clock.Now().UTC()
	res := idsdriver.Resource{
		Project:    cfg.Project,
		Location:   cfg.Location,
		ID:         cfg.ID,
		CreateTime: now,
		UpdateTime: now,
		Fields:     cloneRawMap(cfg.Fields),
	}
	m.endpoints.Set(key, res)

	op := m.newOp(cfg.Project, cfg.Location, "create", key)
	out := cloneResource(&res)

	return &out, op, nil
}

// GetEndpoint returns an endpoint by identity, cloned.
func (m *Mock) GetEndpoint(_ context.Context, project, location, id string) (*idsdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := m.endpoints.Get(resourceName(project, location, id))
	if !ok {
		return nil, notFoundErr(project, location, id)
	}

	out := cloneResource(&r)

	return &out, nil
}

// ListEndpoints returns every endpoint in a project+location, ordered by resource
// name.
func (m *Mock) ListEndpoints(_ context.Context, project, location string) ([]idsdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + endpointsColl + "/"
	all := m.endpoints.SortedValues()
	out := make([]idsdriver.Resource, 0, len(all))

	for i := range all {
		key := resourceName(all[i].Project, all[i].Location, all[i].ID)
		if strings.HasPrefix(key, prefix) {
			out = append(out, cloneResource(&all[i]))
		}
	}

	return out, nil
}

// PatchEndpoint applies a masked update and returns the completed LRO. Only the
// masked top-level body fields are written; a field outside the mask is left
// untouched. An empty mask replaces every field present in the request body.
func (m *Mock) PatchEndpoint(_ context.Context, cfg *idsdriver.Config, mask []string) (
	*idsdriver.Resource, *idsdriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(cfg.Project, cfg.Location, cfg.ID)

	r, ok := m.endpoints.Get(key)
	if !ok {
		return nil, nil, notFoundErr(cfg.Project, cfg.Location, cfg.ID)
	}

	applyMask(&r, cfg.Fields, mask)
	r.UpdateTime = m.opts.Clock.Now().UTC()
	m.endpoints.Set(key, r)

	op := m.newOp(cfg.Project, cfg.Location, "update", key)
	out := cloneResource(&r)

	return &out, op, nil
}

// DeleteEndpoint removes an endpoint and returns the completed LRO.
func (m *Mock) DeleteEndpoint(_ context.Context, project, location, id string) (*idsdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(project, location, id)
	if !m.endpoints.Has(key) {
		return nil, notFoundErr(project, location, id)
	}

	m.endpoints.Delete(key)

	return m.newOp(project, location, "delete", key), nil
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op id
// an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*idsdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &idsdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// notFoundErr builds the NOT_FOUND error carrying the full resource name, as real
// Cloud IDS does for a Get/Patch/Delete of a missing endpoint.
func notFoundErr(project, location, id string) error {
	return cerrors.Newf(cerrors.NotFound, "endpoint %q not found", resourceName(project, location, id))
}

// applyMask folds the masked top-level body fields from desired into the stored
// endpoint. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every field
// present in desired (lenient full-body update).
func applyMask(r *idsdriver.Resource, desired map[string]json.RawMessage, mask []string) {
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
