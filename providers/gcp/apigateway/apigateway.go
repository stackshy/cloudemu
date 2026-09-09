// Package apigateway provides an in-memory mock of the Google API Gateway
// control plane (apigateway.googleapis.com). It models apis, their api configs,
// and gateways and the long-running operations their mutating RPCs return. It is
// control-plane only: IAM verbs and any real request routing (the data plane)
// are out of scope.
package apigateway

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
	agdriver "github.com/stackshy/cloudemu/v2/services/apigatewaygcp/driver"
)

var _ agdriver.APIGateway = (*Mock)(nil)

const (
	apisColl     = "apis"
	configsColl  = "configs"
	gatewaysColl = "gateways"

	// apiConfigField is the gateway body key that references the api config the
	// gateway serves, as a full resource name.
	apiConfigField = "apiConfig"
)

// Mock is the in-memory API Gateway control-plane implementation. Each resource
// collection is a separate store keyed by its full GCP resource name, so a
// trailing-slash-bounded prefix scan cascades an api delete to its configs.
type Mock struct {
	mu sync.RWMutex

	apis       *memstore.Store[agdriver.Resource]
	configs    *memstore.Store[agdriver.Resource]
	gateways   *memstore.Store[agdriver.Resource]
	operations *memstore.Store[agdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new API Gateway mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		apis:       memstore.New[agdriver.Resource](),
		configs:    memstore.New[agdriver.Resource](),
		gateways:   memstore.New[agdriver.Resource](),
		operations: memstore.New[agdriver.Operation](),
		opts:       opts,
	}
}

// apiName builds the full resource name of an api (or an api collection prefix
// when id is empty).
func apiName(project, location, id string) string {
	base := "projects/" + project + "/locations/" + location + "/" + apisColl + "/"
	if id == "" {
		return base
	}

	return base + id
}

// configName builds the full resource name of an api config under its parent
// api.
func configName(project, location, api, id string) string {
	return apiName(project, location, api) + "/" + configsColl + "/" + id
}

// gatewayName builds the full resource name of a gateway.
func gatewayName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + gatewaysColl + "/" + id
}

func notFoundErr(kind, name string) error {
	return cerrors.Newf(cerrors.NotFound, "%s %q not found", kind, name)
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *agdriver.Operation {
	scope := "projects/" + project + "/locations/" + location
	op := agdriver.Operation{
		Name:       fmt.Sprintf("%s/operations/operation-%d-%s", scope, m.opSeq.Add(1), idgen.UUID()),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// store returns a stored resource clone for a create response, minting the
// timestamps from the clock. The caller holds the write lock.
func (m *Mock) store(s *memstore.Store[agdriver.Resource], key string, res *agdriver.Resource) agdriver.Resource {
	now := m.opts.Clock.Now().UTC()
	res.CreateTime = now
	res.UpdateTime = now
	res.Fields = cloneRawMap(res.Fields)
	s.Set(key, *res)

	return cloneResource(res)
}

// getFrom returns a resource by key, cloned.
func (m *Mock) getFrom(s *memstore.Store[agdriver.Resource], kind, key string) (*agdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := s.Get(key)
	if !ok {
		return nil, notFoundErr(kind, key)
	}

	out := cloneResource(&r)

	return &out, nil
}

// listPrefixed returns every resource whose key starts with prefix, ordered by
// resource name.
func (m *Mock) listPrefixed(s *memstore.Store[agdriver.Resource], prefix string) []agdriver.Resource {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := s.SortedValues()
	out := make([]agdriver.Resource, 0, len(all))

	for i := range all {
		key := m.keyOf(s, &all[i])
		if strings.HasPrefix(key, prefix) {
			out = append(out, cloneResource(&all[i]))
		}
	}

	return out
}

// keyOf reconstructs the store key for a resource from its identity components.
func (m *Mock) keyOf(s *memstore.Store[agdriver.Resource], r *agdriver.Resource) string {
	switch s {
	case m.configs:
		return configName(r.Project, r.Location, r.API, r.ID)
	case m.gateways:
		return gatewayName(r.Project, r.Location, r.ID)
	default:
		return apiName(r.Project, r.Location, r.ID)
	}
}

// patchIn applies a masked update to a stored resource and returns the completed
// LRO. Only the masked top-level body fields are written; a field outside the
// mask is left untouched. An empty mask replaces every field present in the
// request body.
func (m *Mock) patchIn(
	s *memstore.Store[agdriver.Resource], kind, key string, cfg *agdriver.Config, mask []string,
) (*agdriver.Resource, *agdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := s.Get(key)
	if !ok {
		return nil, nil, notFoundErr(kind, key)
	}

	applyMask(&r, cfg.Fields, mask)
	r.UpdateTime = m.opts.Clock.Now().UTC()
	s.Set(key, r)

	op := m.newOp(cfg.Project, cfg.Location, "update", key)
	out := cloneResource(&r)

	return &out, op, nil
}

// delFrom removes a resource by key and returns the completed LRO.
func (m *Mock) delFrom(s *memstore.Store[agdriver.Resource], kind, project, location, key string) (
	*agdriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !s.Has(key) {
		return nil, notFoundErr(kind, key)
	}

	s.Delete(key)

	return m.newOp(project, location, "delete", key), nil
}

// CreateAPI provisions a new api.
func (m *Mock) CreateAPI(_ context.Context, cfg *agdriver.Config) (*agdriver.Resource, *agdriver.Operation, error) {
	if err := requireIDLocation(cfg, "apiId"); err != nil {
		return nil, nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := apiName(cfg.Project, cfg.Location, cfg.ID)
	if m.apis.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "api %q already exists", key)
	}

	out := m.store(m.apis, key, &agdriver.Resource{
		Project: cfg.Project, Location: cfg.Location, ID: cfg.ID, Fields: cfg.Fields,
	})

	return &out, m.newOp(cfg.Project, cfg.Location, "create", key), nil
}

// GetAPI returns an api by identity.
func (m *Mock) GetAPI(_ context.Context, project, location, id string) (*agdriver.Resource, error) {
	return m.getFrom(m.apis, "api", apiName(project, location, id))
}

// ListAPIs returns every api in a project+location.
func (m *Mock) ListAPIs(_ context.Context, project, location string) ([]agdriver.Resource, error) {
	return m.listPrefixed(m.apis, apiName(project, location, "")), nil
}

// PatchAPI applies a masked update to an api.
func (m *Mock) PatchAPI(_ context.Context, cfg *agdriver.Config, mask []string) (
	*agdriver.Resource, *agdriver.Operation, error,
) {
	return m.patchIn(m.apis, "api", apiName(cfg.Project, cfg.Location, cfg.ID), cfg, mask)
}

// DeleteAPI removes an api and cascades to its api configs. The cascade prefix
// is trailing-slash-bounded so deleting api "a1" never touches "a10"'s configs.
func (m *Mock) DeleteAPI(_ context.Context, project, location, id string) (*agdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := apiName(project, location, id)
	if !m.apis.Has(key) {
		return nil, notFoundErr("api", key)
	}

	m.apis.Delete(key)
	deletePrefixed(m.configs, key+"/"+configsColl+"/")

	return m.newOp(project, location, "delete", key), nil
}

// CreateAPIConfig provisions a new api config under an existing api.
func (m *Mock) CreateAPIConfig(_ context.Context, cfg *agdriver.Config) (
	*agdriver.Resource, *agdriver.Operation, error,
) {
	if err := requireIDLocation(cfg, "apiConfigId"); err != nil {
		return nil, nil, err
	}

	if cfg.API == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "api is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	parent := apiName(cfg.Project, cfg.Location, cfg.API)
	if !m.apis.Has(parent) {
		return nil, nil, notFoundErr("api", parent)
	}

	key := configName(cfg.Project, cfg.Location, cfg.API, cfg.ID)
	if m.configs.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "apiConfig %q already exists", key)
	}

	out := m.store(m.configs, key, &agdriver.Resource{
		Project: cfg.Project, Location: cfg.Location, API: cfg.API, ID: cfg.ID, Fields: cfg.Fields,
	})

	return &out, m.newOp(cfg.Project, cfg.Location, "create", key), nil
}

// GetAPIConfig returns an api config by identity.
func (m *Mock) GetAPIConfig(_ context.Context, project, location, api, id string) (*agdriver.Resource, error) {
	return m.getFrom(m.configs, "apiConfig", configName(project, location, api, id))
}

// ListAPIConfigs returns every api config under an api.
func (m *Mock) ListAPIConfigs(_ context.Context, project, location, api string) ([]agdriver.Resource, error) {
	return m.listPrefixed(m.configs, apiName(project, location, api)+"/"+configsColl+"/"), nil
}

// PatchAPIConfig applies a masked update to an api config.
func (m *Mock) PatchAPIConfig(_ context.Context, cfg *agdriver.Config, mask []string) (
	*agdriver.Resource, *agdriver.Operation, error,
) {
	return m.patchIn(m.configs, "apiConfig", configName(cfg.Project, cfg.Location, cfg.API, cfg.ID), cfg, mask)
}

// DeleteAPIConfig removes a single api config.
func (m *Mock) DeleteAPIConfig(_ context.Context, project, location, api, id string) (*agdriver.Operation, error) {
	return m.delFrom(m.configs, "apiConfig", project, location, configName(project, location, api, id))
}

// CreateGateway provisions a new gateway. When the body references an apiConfig,
// the referenced config must exist, matching real API Gateway (a NOT_FOUND
// otherwise).
func (m *Mock) CreateGateway(_ context.Context, cfg *agdriver.Config) (
	*agdriver.Resource, *agdriver.Operation, error,
) {
	if err := requireIDLocation(cfg, "gatewayId"); err != nil {
		return nil, nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkAPIConfigRef(cfg.Fields); err != nil {
		return nil, nil, err
	}

	key := gatewayName(cfg.Project, cfg.Location, cfg.ID)
	if m.gateways.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "gateway %q already exists", key)
	}

	out := m.store(m.gateways, key, &agdriver.Resource{
		Project: cfg.Project, Location: cfg.Location, ID: cfg.ID, Fields: cfg.Fields,
	})

	return &out, m.newOp(cfg.Project, cfg.Location, "create", key), nil
}

// GetGateway returns a gateway by identity.
func (m *Mock) GetGateway(_ context.Context, project, location, id string) (*agdriver.Resource, error) {
	return m.getFrom(m.gateways, "gateway", gatewayName(project, location, id))
}

// ListGateways returns every gateway in a project+location.
func (m *Mock) ListGateways(_ context.Context, project, location string) ([]agdriver.Resource, error) {
	return m.listPrefixed(m.gateways, "projects/"+project+"/locations/"+location+"/"+gatewaysColl+"/"), nil
}

// PatchGateway applies a masked update to a gateway. A masked apiConfig change
// is validated against the configs store.
func (m *Mock) PatchGateway(_ context.Context, cfg *agdriver.Config, mask []string) (
	*agdriver.Resource, *agdriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if masked(mask, apiConfigField) {
		if err := m.checkAPIConfigRef(cfg.Fields); err != nil {
			return nil, nil, err
		}
	}

	key := gatewayName(cfg.Project, cfg.Location, cfg.ID)

	r, ok := m.gateways.Get(key)
	if !ok {
		return nil, nil, notFoundErr("gateway", key)
	}

	applyMask(&r, cfg.Fields, mask)
	r.UpdateTime = m.opts.Clock.Now().UTC()
	m.gateways.Set(key, r)

	op := m.newOp(cfg.Project, cfg.Location, "update", key)
	out := cloneResource(&r)

	return &out, op, nil
}

// DeleteGateway removes a single gateway.
func (m *Mock) DeleteGateway(_ context.Context, project, location, id string) (*agdriver.Operation, error) {
	return m.delFrom(m.gateways, "gateway", project, location, gatewayName(project, location, id))
}

// checkAPIConfigRef rejects a gateway body whose apiConfig references a config
// that does not exist. An absent/empty reference is left to the wire layer's
// required-field handling. The caller holds the lock.
func (m *Mock) checkAPIConfigRef(fields map[string]json.RawMessage) error {
	raw, ok := fields[apiConfigField]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var ref string
	if json.Unmarshal(raw, &ref) != nil || ref == "" {
		return nil
	}

	if !m.configs.Has(ref) {
		return notFoundErr("apiConfig", ref)
	}

	return nil
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op
// id an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*agdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &agdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// requireIDLocation validates the id and location present on every create.
func requireIDLocation(cfg *agdriver.Config, idParam string) error {
	if cfg.ID == "" {
		return cerrors.New(cerrors.InvalidArgument, idParam+" is required")
	}

	if cfg.Location == "" {
		return cerrors.New(cerrors.InvalidArgument, "location is required")
	}

	return nil
}

// applyMask folds the masked top-level body fields from desired into the stored
// resource. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every
// field present in desired (lenient full-body update).
func applyMask(r *agdriver.Resource, desired map[string]json.RawMessage, mask []string) {
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

// masked reports whether field is targeted by the updateMask: an empty mask is a
// lenient full update, otherwise the field's leading path segment must be listed.
func masked(mask []string, field string) bool {
	if len(mask) == 0 {
		return true
	}

	for _, p := range mask {
		seg := p
		if i := strings.IndexByte(p, '.'); i >= 0 {
			seg = p[:i]
		}

		if seg == field {
			return true
		}
	}

	return false
}

// deletePrefixed removes every entry in s whose key starts with prefix.
func deletePrefixed(s *memstore.Store[agdriver.Resource], prefix string) {
	for _, k := range s.Keys() {
		if strings.HasPrefix(k, prefix) {
			s.Delete(k)
		}
	}
}
