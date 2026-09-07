// Package metastore provides an in-memory mock of the Google Dataproc Metastore
// control plane (metastore.googleapis.com/v1). It models metastore services and
// the long-running operations their mutating RPCs return. It is control-plane
// only: metadataImports, backups, federations, and any real Hive metastore data
// plane are out of scope.
package metastore

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
	msdriver "github.com/stackshy/cloudemu/v2/services/metastore/driver"
)

var _ msdriver.Metastore = (*Mock)(nil)

const servicesColl = "services"

// Mock is the in-memory Dataproc Metastore control-plane implementation. Each
// service is keyed by its full GCP resource name.
type Mock struct {
	mu sync.RWMutex

	services   *memstore.Store[msdriver.Resource]
	operations *memstore.Store[msdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Dataproc Metastore mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		services:   memstore.New[msdriver.Resource](),
		operations: memstore.New[msdriver.Operation](),
		opts:       opts,
	}
}

// resourceName builds the full service resource name.
func resourceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + servicesColl + "/" + id
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *msdriver.Operation {
	scope := "projects/" + project + "/locations/" + location
	op := msdriver.Operation{
		Name:       fmt.Sprintf("%s/operations/operation-%d-%s", scope, m.opSeq.Add(1), idgen.UUID()),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// CreateService provisions a new metastore service with computed output fields
// carried verbatim (already seeded by the wire layer) and returns the completed
// LRO.
func (m *Mock) CreateService(_ context.Context, cfg *msdriver.Config) (
	*msdriver.Resource, *msdriver.Operation, error,
) {
	if cfg.ID == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "service id is required")
	}

	if cfg.Location == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "location is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(cfg.Project, cfg.Location, cfg.ID)
	if m.services.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "service %q already exists", cfg.ID)
	}

	now := m.opts.Clock.Now().UTC()
	res := msdriver.Resource{
		Project:    cfg.Project,
		Location:   cfg.Location,
		ID:         cfg.ID,
		CreateTime: now,
		UpdateTime: now,
		Fields:     cloneRawMap(cfg.Fields),
	}
	m.services.Set(key, res)

	op := m.newOp(cfg.Project, cfg.Location, "create", key)
	out := cloneResource(&res)

	return &out, op, nil
}

// GetService returns a service by identity, cloned.
func (m *Mock) GetService(_ context.Context, project, location, id string) (*msdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := m.services.Get(resourceName(project, location, id))
	if !ok {
		return nil, notFoundErr(project, location, id)
	}

	out := cloneResource(&r)

	return &out, nil
}

// ListServices returns every service in a project+location, ordered by resource
// name.
func (m *Mock) ListServices(_ context.Context, project, location string) ([]msdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + servicesColl + "/"
	all := m.services.SortedValues()
	out := make([]msdriver.Resource, 0, len(all))

	for i := range all {
		key := resourceName(all[i].Project, all[i].Location, all[i].ID)
		if strings.HasPrefix(key, prefix) {
			out = append(out, cloneResource(&all[i]))
		}
	}

	return out, nil
}

// PatchService applies a masked update and returns the completed LRO. Only the
// masked top-level body fields are written; a field outside the mask is left
// untouched. An empty mask replaces every field present in the request body.
func (m *Mock) PatchService(_ context.Context, cfg *msdriver.Config, mask []string) (
	*msdriver.Resource, *msdriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(cfg.Project, cfg.Location, cfg.ID)

	r, ok := m.services.Get(key)
	if !ok {
		return nil, nil, notFoundErr(cfg.Project, cfg.Location, cfg.ID)
	}

	applyMask(&r, cfg.Fields, mask)
	r.UpdateTime = m.opts.Clock.Now().UTC()
	m.services.Set(key, r)

	op := m.newOp(cfg.Project, cfg.Location, "update", key)
	out := cloneResource(&r)

	return &out, op, nil
}

// DeleteService removes a service and returns the completed LRO.
func (m *Mock) DeleteService(_ context.Context, project, location, id string) (*msdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(project, location, id)
	if !m.services.Has(key) {
		return nil, notFoundErr(project, location, id)
	}

	m.services.Delete(key)

	return m.newOp(project, location, "delete", key), nil
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op
// id an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*msdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &msdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// notFoundErr builds the NOT_FOUND error carrying the full resource name, as
// real Dataproc Metastore does for a Get/Patch/Delete of a missing service.
func notFoundErr(project, location, id string) error {
	return cerrors.Newf(cerrors.NotFound, "service %q not found", resourceName(project, location, id))
}

// applyMask folds the masked top-level body fields from desired into the stored
// service. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every
// field present in desired (lenient full-body update).
func applyMask(r *msdriver.Resource, desired map[string]json.RawMessage, mask []string) {
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
