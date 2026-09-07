// Package securesourcemanager provides an in-memory mock of the Google Secure
// Source Manager control plane (securesourcemanager.googleapis.com/v1). It
// models instances and repositories and the long-running operations their
// mutating RPCs return. It is control-plane only: IAM verbs, branch rules,
// hooks, and any real git hosting (the data plane) are out of scope.
package securesourcemanager

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
	ssmdriver "github.com/stackshy/cloudemu/v2/services/securesourcemanager/driver"
)

var _ ssmdriver.SecureSourceManager = (*Mock)(nil)

const (
	instancesColl    = "instances"
	repositoriesColl = "repositories"
)

// Mock is the in-memory Secure Source Manager control-plane implementation. Each
// resource collection is keyed by its full GCP resource name.
type Mock struct {
	mu sync.RWMutex

	instances    *memstore.Store[ssmdriver.Resource]
	repositories *memstore.Store[ssmdriver.Resource]
	operations   *memstore.Store[ssmdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Secure Source Manager mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		instances:    memstore.New[ssmdriver.Resource](),
		repositories: memstore.New[ssmdriver.Resource](),
		operations:   memstore.New[ssmdriver.Operation](),
		opts:         opts,
	}
}

// resourceName builds the full resource name for a collection.
func resourceName(coll, project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + coll + "/" + id
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *ssmdriver.Operation {
	scope := "projects/" + project + "/locations/" + location
	op := ssmdriver.Operation{
		Name:       fmt.Sprintf("%s/operations/operation-%d-%s", scope, m.opSeq.Add(1), idgen.UUID()),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// create provisions a new resource in the given collection with computed output
// fields derived deterministically, and returns the completed LRO.
func (m *Mock) create(store *memstore.Store[ssmdriver.Resource], coll string, cfg *ssmdriver.Config) (
	*ssmdriver.Resource, *ssmdriver.Operation, error,
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
	res := ssmdriver.Resource{
		Project:    cfg.Project,
		Location:   cfg.Location,
		ID:         cfg.ID,
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
func (m *Mock) get(store *memstore.Store[ssmdriver.Resource], coll, project, location, id string) (
	*ssmdriver.Resource, error,
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
func (m *Mock) list(store *memstore.Store[ssmdriver.Resource], coll, project, location string) (
	[]ssmdriver.Resource, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + coll + "/"
	all := store.SortedValues()
	out := make([]ssmdriver.Resource, 0, len(all))

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
// An empty mask replaces every field present in the request body.
func (m *Mock) patch(store *memstore.Store[ssmdriver.Resource], coll string, cfg *ssmdriver.Config, mask []string) (
	*ssmdriver.Resource, *ssmdriver.Operation, error,
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
func (m *Mock) del(store *memstore.Store[ssmdriver.Resource], coll, project, location, id string) (
	*ssmdriver.Operation, error,
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

// CreateInstance provisions a new instance.
func (m *Mock) CreateInstance(_ context.Context, cfg *ssmdriver.Config) (
	*ssmdriver.Resource, *ssmdriver.Operation, error,
) {
	return m.create(m.instances, instancesColl, cfg)
}

// GetInstance returns an instance by identity.
func (m *Mock) GetInstance(_ context.Context, project, location, id string) (*ssmdriver.Resource, error) {
	return m.get(m.instances, instancesColl, project, location, id)
}

// ListInstances returns every instance in a project+location.
func (m *Mock) ListInstances(_ context.Context, project, location string) ([]ssmdriver.Resource, error) {
	return m.list(m.instances, instancesColl, project, location)
}

// DeleteInstance removes an instance.
func (m *Mock) DeleteInstance(_ context.Context, project, location, id string) (*ssmdriver.Operation, error) {
	return m.del(m.instances, instancesColl, project, location, id)
}

// CreateRepository provisions a new repository.
func (m *Mock) CreateRepository(_ context.Context, cfg *ssmdriver.Config) (
	*ssmdriver.Resource, *ssmdriver.Operation, error,
) {
	return m.create(m.repositories, repositoriesColl, cfg)
}

// GetRepository returns a repository by identity.
func (m *Mock) GetRepository(_ context.Context, project, location, id string) (*ssmdriver.Resource, error) {
	return m.get(m.repositories, repositoriesColl, project, location, id)
}

// ListRepositories returns every repository in a project+location.
func (m *Mock) ListRepositories(_ context.Context, project, location string) ([]ssmdriver.Resource, error) {
	return m.list(m.repositories, repositoriesColl, project, location)
}

// PatchRepository applies a masked update to a repository.
func (m *Mock) PatchRepository(_ context.Context, cfg *ssmdriver.Config, mask []string) (
	*ssmdriver.Resource, *ssmdriver.Operation, error,
) {
	return m.patch(m.repositories, repositoriesColl, cfg, mask)
}

// DeleteRepository removes a repository.
func (m *Mock) DeleteRepository(_ context.Context, project, location, id string) (*ssmdriver.Operation, error) {
	return m.del(m.repositories, repositoriesColl, project, location, id)
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op
// id an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*ssmdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &ssmdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// notFoundErr builds the NOT_FOUND error carrying the full resource name, as
// real Secure Source Manager does for a Get/Patch/Delete of a missing resource.
func notFoundErr(coll, project, location, id string) error {
	return cerrors.Newf(cerrors.NotFound, "%s %q not found", coll, resourceName(coll, project, location, id))
}

// applyMask folds the masked top-level body fields from desired into the stored
// resource. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every
// field present in desired (lenient full-body update).
func applyMask(r *ssmdriver.Resource, desired map[string]json.RawMessage, mask []string) {
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
