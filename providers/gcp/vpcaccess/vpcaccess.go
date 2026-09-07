// Package vpcaccess provides an in-memory mock of the Google Serverless VPC
// Access control plane (vpcaccess.googleapis.com/v1). It models connectors and
// the long-running operations their mutating RPCs return. It is control-plane
// only: IAM verbs and any real data-plane traffic routing are out of scope.
package vpcaccess

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
	vpcdriver "github.com/stackshy/cloudemu/v2/services/vpcaccess/driver"
)

var _ vpcdriver.VPCAccess = (*Mock)(nil)

const connectorsColl = "connectors"

// Mock is the in-memory Serverless VPC Access control-plane implementation. Each
// connector is keyed by its full GCP resource name.
type Mock struct {
	mu sync.RWMutex

	connectors *memstore.Store[vpcdriver.Resource]
	operations *memstore.Store[vpcdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Serverless VPC Access mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		connectors: memstore.New[vpcdriver.Resource](),
		operations: memstore.New[vpcdriver.Operation](),
		opts:       opts,
	}
}

// resourceName builds the full connector resource name.
func resourceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + connectorsColl + "/" + id
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *vpcdriver.Operation {
	scope := "projects/" + project + "/locations/" + location
	op := vpcdriver.Operation{
		Name:       fmt.Sprintf("%s/operations/operation-%d-%s", scope, m.opSeq.Add(1), idgen.UUID()),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// CreateConnector provisions a new connector with computed output fields carried
// verbatim (already seeded by the wire layer) and returns the completed LRO.
func (m *Mock) CreateConnector(_ context.Context, cfg *vpcdriver.Config) (
	*vpcdriver.Resource, *vpcdriver.Operation, error,
) {
	if cfg.ID == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "connector id is required")
	}

	if cfg.Location == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "location is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(cfg.Project, cfg.Location, cfg.ID)
	if m.connectors.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "connector %q already exists", cfg.ID)
	}

	now := m.opts.Clock.Now().UTC()
	res := vpcdriver.Resource{
		Project:    cfg.Project,
		Location:   cfg.Location,
		ID:         cfg.ID,
		CreateTime: now,
		UpdateTime: now,
		Fields:     cloneRawMap(cfg.Fields),
	}
	m.connectors.Set(key, res)

	op := m.newOp(cfg.Project, cfg.Location, "create", key)
	out := cloneResource(&res)

	return &out, op, nil
}

// GetConnector returns a connector by identity, cloned.
func (m *Mock) GetConnector(_ context.Context, project, location, id string) (*vpcdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := m.connectors.Get(resourceName(project, location, id))
	if !ok {
		return nil, notFoundErr(project, location, id)
	}

	out := cloneResource(&r)

	return &out, nil
}

// ListConnectors returns every connector in a project+location, ordered by
// resource name.
func (m *Mock) ListConnectors(_ context.Context, project, location string) ([]vpcdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + connectorsColl + "/"
	all := m.connectors.SortedValues()
	out := make([]vpcdriver.Resource, 0, len(all))

	for i := range all {
		key := resourceName(all[i].Project, all[i].Location, all[i].ID)
		if strings.HasPrefix(key, prefix) {
			out = append(out, cloneResource(&all[i]))
		}
	}

	return out, nil
}

// PatchConnector applies a masked update and returns the completed LRO. Only the
// masked top-level body fields are written; a field outside the mask is left
// untouched. An empty mask replaces every field present in the request body.
func (m *Mock) PatchConnector(_ context.Context, cfg *vpcdriver.Config, mask []string) (
	*vpcdriver.Resource, *vpcdriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(cfg.Project, cfg.Location, cfg.ID)

	r, ok := m.connectors.Get(key)
	if !ok {
		return nil, nil, notFoundErr(cfg.Project, cfg.Location, cfg.ID)
	}

	applyMask(&r, cfg.Fields, mask)
	r.UpdateTime = m.opts.Clock.Now().UTC()
	m.connectors.Set(key, r)

	op := m.newOp(cfg.Project, cfg.Location, "update", key)
	out := cloneResource(&r)

	return &out, op, nil
}

// DeleteConnector removes a connector and returns the completed LRO.
func (m *Mock) DeleteConnector(_ context.Context, project, location, id string) (*vpcdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(project, location, id)
	if !m.connectors.Has(key) {
		return nil, notFoundErr(project, location, id)
	}

	m.connectors.Delete(key)

	return m.newOp(project, location, "delete", key), nil
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op
// id an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*vpcdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &vpcdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// notFoundErr builds the NOT_FOUND error carrying the full resource name, as
// real Serverless VPC Access does for a Get/Patch/Delete of a missing connector.
func notFoundErr(project, location, id string) error {
	return cerrors.Newf(cerrors.NotFound, "connector %q not found", resourceName(project, location, id))
}

// applyMask folds the masked top-level body fields from desired into the stored
// connector. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every
// field present in desired (lenient full-body update).
func applyMask(r *vpcdriver.Resource, desired map[string]json.RawMessage, mask []string) {
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
