// Package clouddeploy provides an in-memory mock of the Google Cloud Deploy
// control plane (clouddeploy.googleapis.com/v1). It models delivery pipelines
// and targets and the long-running operations their mutating RPCs return. It is
// control-plane only: releases, rollouts, rollbacks, automations, deploy
// policies, custom target types, IAM verbs, and any real render/deploy
// execution are out of scope.
package clouddeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	cdriver "github.com/stackshy/cloudemu/v2/services/clouddeploy/driver"
)

var _ cdriver.CloudDeploy = (*Mock)(nil)

const (
	pipelinesColl = "deliveryPipelines"
	targetsColl   = "targets"
)

// Mock is the in-memory Cloud Deploy control-plane implementation. Both resource
// collections are keyed by their full GCP resource name.
type Mock struct {
	mu sync.RWMutex

	pipelines  *memstore.Store[cdriver.Resource]
	targets    *memstore.Store[cdriver.Resource]
	operations *memstore.Store[cdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Cloud Deploy mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		pipelines:  memstore.New[cdriver.Resource](),
		targets:    memstore.New[cdriver.Resource](),
		operations: memstore.New[cdriver.Operation](),
		opts:       opts,
	}
}

// resourceName builds the full resource name for a collection.
func resourceName(coll, project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + coll + "/" + id
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *cdriver.Operation {
	scope := "projects/" + project + "/locations/" + location
	op := cdriver.Operation{
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
func (m *Mock) create(store *memstore.Store[cdriver.Resource], coll string, cfg *cdriver.Config) (
	*cdriver.Resource, *cdriver.Operation, error,
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
	res := cdriver.Resource{
		Project:    cfg.Project,
		Location:   cfg.Location,
		ID:         cfg.ID,
		UID:        idgen.UUID(),
		CreateTime: now,
		UpdateTime: now,
		Fields:     cloneRawMap(cfg.Fields),
	}
	res.Etag = etag(&res)
	store.Set(key, res)

	op := m.newOp(cfg.Project, cfg.Location, "create", key)
	out := cloneResource(&res)

	return &out, op, nil
}

// get returns a resource by its identity, cloned.
func (m *Mock) get(store *memstore.Store[cdriver.Resource], coll, project, location, id string) (
	*cdriver.Resource, error,
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
func (m *Mock) list(store *memstore.Store[cdriver.Resource], coll, project, location string) (
	[]cdriver.Resource, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + coll + "/"
	all := store.SortedValues()
	out := make([]cdriver.Resource, 0, len(all))

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
func (m *Mock) patch(store *memstore.Store[cdriver.Resource], coll string, cfg *cdriver.Config, mask []string) (
	*cdriver.Resource, *cdriver.Operation, error,
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
	r.Etag = etag(&r)
	store.Set(key, r)

	op := m.newOp(cfg.Project, cfg.Location, "update", key)
	out := cloneResource(&r)

	return &out, op, nil
}

// del removes a resource and returns the completed LRO.
func (m *Mock) del(store *memstore.Store[cdriver.Resource], coll, project, location, id string) (
	*cdriver.Operation, error,
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

// CreatePipeline provisions a new delivery pipeline.
func (m *Mock) CreatePipeline(_ context.Context, cfg *cdriver.Config) (*cdriver.Resource, *cdriver.Operation, error) {
	return m.create(m.pipelines, pipelinesColl, cfg)
}

// GetPipeline returns a delivery pipeline by identity.
func (m *Mock) GetPipeline(_ context.Context, project, location, id string) (*cdriver.Resource, error) {
	return m.get(m.pipelines, pipelinesColl, project, location, id)
}

// ListPipelines returns every delivery pipeline in a project+location.
func (m *Mock) ListPipelines(_ context.Context, project, location string) ([]cdriver.Resource, error) {
	return m.list(m.pipelines, pipelinesColl, project, location)
}

// PatchPipeline applies a masked update to a delivery pipeline.
func (m *Mock) PatchPipeline(_ context.Context, cfg *cdriver.Config, mask []string) (
	*cdriver.Resource, *cdriver.Operation, error,
) {
	return m.patch(m.pipelines, pipelinesColl, cfg, mask)
}

// DeletePipeline removes a delivery pipeline.
func (m *Mock) DeletePipeline(_ context.Context, project, location, id string) (*cdriver.Operation, error) {
	return m.del(m.pipelines, pipelinesColl, project, location, id)
}

// CreateTarget provisions a new target.
func (m *Mock) CreateTarget(_ context.Context, cfg *cdriver.Config) (*cdriver.Resource, *cdriver.Operation, error) {
	return m.create(m.targets, targetsColl, cfg)
}

// GetTarget returns a target by identity.
func (m *Mock) GetTarget(_ context.Context, project, location, id string) (*cdriver.Resource, error) {
	return m.get(m.targets, targetsColl, project, location, id)
}

// ListTargets returns every target in a project+location.
func (m *Mock) ListTargets(_ context.Context, project, location string) ([]cdriver.Resource, error) {
	return m.list(m.targets, targetsColl, project, location)
}

// PatchTarget applies a masked update to a target.
func (m *Mock) PatchTarget(_ context.Context, cfg *cdriver.Config, mask []string) (
	*cdriver.Resource, *cdriver.Operation, error,
) {
	return m.patch(m.targets, targetsColl, cfg, mask)
}

// DeleteTarget removes a target.
func (m *Mock) DeleteTarget(_ context.Context, project, location, id string) (*cdriver.Operation, error) {
	return m.del(m.targets, targetsColl, project, location, id)
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op
// id an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*cdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &cdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// notFoundErr builds the NOT_FOUND error carrying the full resource name, as
// real Cloud Deploy does for a Get/Patch/Delete of a missing resource.
func notFoundErr(coll, project, location, id string) error {
	return cerrors.Newf(cerrors.NotFound, "%s %q not found", coll, resourceName(coll, project, location, id))
}

// applyMask folds the masked top-level body fields from desired into the stored
// resource. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every
// field present in desired (lenient full-body update).
func applyMask(r *cdriver.Resource, desired map[string]json.RawMessage, mask []string) {
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

// etag derives a stable, content-addressed etag from a resource's identity and
// body, recomputed on every mutation and stable across reads.
func etag(r *cdriver.Resource) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(r.UID))
	_, _ = h.Write([]byte(r.UpdateTime.Format("2006-01-02T15:04:05.000000000Z07:00")))

	for _, k := range sortedKeys(r.Fields) {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write(r.Fields[k])
	}

	return fmt.Sprintf("%016x", h.Sum64())
}

// sortedKeys returns the keys of m in ascending order for a deterministic etag.
func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}

	return keys
}
