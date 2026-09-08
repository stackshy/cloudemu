// Package gkehub provides an in-memory mock of the GKE Hub / Fleet control plane
// (gkehub.googleapis.com/v1). It models memberships, features, and fleets and
// the long-running operations their mutating RPCs return. It is control-plane
// only: generateConnectManifest, IAM verbs, membership bindings, scopes,
// rbacrolebindings, and any real cluster registration / fleet data plane are out
// of scope.
package gkehub

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
	gdriver "github.com/stackshy/cloudemu/v2/services/gkehub/driver"
)

var _ gdriver.GKEHub = (*Mock)(nil)

const (
	membershipsColl = "memberships"
	featuresColl    = "features"
	fleetsColl      = "fleets"
)

// Mock is the in-memory GKE Hub control-plane implementation. All three resource
// collections are keyed by their full GCP resource name.
type Mock struct {
	mu sync.RWMutex

	memberships *memstore.Store[gdriver.Resource]
	features    *memstore.Store[gdriver.Resource]
	fleets      *memstore.Store[gdriver.Resource]
	operations  *memstore.Store[gdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new GKE Hub mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		memberships: memstore.New[gdriver.Resource](),
		features:    memstore.New[gdriver.Resource](),
		fleets:      memstore.New[gdriver.Resource](),
		operations:  memstore.New[gdriver.Operation](),
		opts:        opts,
	}
}

// resourceName builds the full resource name for a collection.
func resourceName(coll, project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + coll + "/" + id
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *gdriver.Operation {
	scope := "projects/" + project + "/locations/" + location
	op := gdriver.Operation{
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
func (m *Mock) create(store *memstore.Store[gdriver.Resource], coll string, cfg *gdriver.Config) (
	*gdriver.Resource, *gdriver.Operation, error,
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
	res := gdriver.Resource{
		Project:    cfg.Project,
		Location:   cfg.Location,
		ID:         cfg.ID,
		UID:        idgen.UUID(),
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
func (m *Mock) get(store *memstore.Store[gdriver.Resource], coll, project, location, id string) (
	*gdriver.Resource, error,
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
func (m *Mock) list(store *memstore.Store[gdriver.Resource], coll, project, location string) (
	[]gdriver.Resource, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + coll + "/"
	all := store.SortedValues()
	out := make([]gdriver.Resource, 0, len(all))

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
func (m *Mock) patch(store *memstore.Store[gdriver.Resource], coll string, cfg *gdriver.Config, mask []string) (
	*gdriver.Resource, *gdriver.Operation, error,
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
func (m *Mock) del(store *memstore.Store[gdriver.Resource], coll, project, location, id string) (
	*gdriver.Operation, error,
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

// CreateMembership provisions a new membership.
func (m *Mock) CreateMembership(_ context.Context, cfg *gdriver.Config) (*gdriver.Resource, *gdriver.Operation, error) {
	return m.create(m.memberships, membershipsColl, cfg)
}

// GetMembership returns a membership by identity.
func (m *Mock) GetMembership(_ context.Context, project, location, id string) (*gdriver.Resource, error) {
	return m.get(m.memberships, membershipsColl, project, location, id)
}

// ListMemberships returns every membership in a project+location.
func (m *Mock) ListMemberships(_ context.Context, project, location string) ([]gdriver.Resource, error) {
	return m.list(m.memberships, membershipsColl, project, location)
}

// PatchMembership applies a masked update to a membership.
func (m *Mock) PatchMembership(_ context.Context, cfg *gdriver.Config, mask []string) (
	*gdriver.Resource, *gdriver.Operation, error,
) {
	return m.patch(m.memberships, membershipsColl, cfg, mask)
}

// DeleteMembership removes a membership.
func (m *Mock) DeleteMembership(_ context.Context, project, location, id string) (*gdriver.Operation, error) {
	return m.del(m.memberships, membershipsColl, project, location, id)
}

// CreateFeature provisions a new feature.
func (m *Mock) CreateFeature(_ context.Context, cfg *gdriver.Config) (*gdriver.Resource, *gdriver.Operation, error) {
	return m.create(m.features, featuresColl, cfg)
}

// GetFeature returns a feature by identity.
func (m *Mock) GetFeature(_ context.Context, project, location, id string) (*gdriver.Resource, error) {
	return m.get(m.features, featuresColl, project, location, id)
}

// ListFeatures returns every feature in a project+location.
func (m *Mock) ListFeatures(_ context.Context, project, location string) ([]gdriver.Resource, error) {
	return m.list(m.features, featuresColl, project, location)
}

// PatchFeature applies a masked update to a feature.
func (m *Mock) PatchFeature(_ context.Context, cfg *gdriver.Config, mask []string) (
	*gdriver.Resource, *gdriver.Operation, error,
) {
	return m.patch(m.features, featuresColl, cfg, mask)
}

// DeleteFeature removes a feature.
func (m *Mock) DeleteFeature(_ context.Context, project, location, id string) (*gdriver.Operation, error) {
	return m.del(m.features, featuresColl, project, location, id)
}

// CreateFleet provisions a new fleet (the singleton "default").
func (m *Mock) CreateFleet(_ context.Context, cfg *gdriver.Config) (*gdriver.Resource, *gdriver.Operation, error) {
	return m.create(m.fleets, fleetsColl, cfg)
}

// GetFleet returns a fleet by identity.
func (m *Mock) GetFleet(_ context.Context, project, location, id string) (*gdriver.Resource, error) {
	return m.get(m.fleets, fleetsColl, project, location, id)
}

// ListFleets returns every fleet in a project+location.
func (m *Mock) ListFleets(_ context.Context, project, location string) ([]gdriver.Resource, error) {
	return m.list(m.fleets, fleetsColl, project, location)
}

// PatchFleet applies a masked update to a fleet.
func (m *Mock) PatchFleet(_ context.Context, cfg *gdriver.Config, mask []string) (
	*gdriver.Resource, *gdriver.Operation, error,
) {
	return m.patch(m.fleets, fleetsColl, cfg, mask)
}

// DeleteFleet removes a fleet.
func (m *Mock) DeleteFleet(_ context.Context, project, location, id string) (*gdriver.Operation, error) {
	return m.del(m.fleets, fleetsColl, project, location, id)
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op
// id an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*gdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &gdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// notFoundErr builds the NOT_FOUND error carrying the full resource name, as
// real GKE Hub does for a Get/Patch/Delete of a missing resource.
func notFoundErr(coll, project, location, id string) error {
	return cerrors.Newf(cerrors.NotFound, "%s %q not found", coll, resourceName(coll, project, location, id))
}

// applyMask folds the masked top-level body fields from desired into the stored
// resource. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every
// field present in desired (lenient full-body update).
func applyMask(r *gdriver.Resource, desired map[string]json.RawMessage, mask []string) {
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

		// Field masks are snake_case per proto convention (Terraform sends
		// "display_name"), while the stored JSON body keys are camelCase.
		field = snakeToCamel(field)

		if v, ok := desired[field]; ok {
			r.Fields[field] = append(json.RawMessage(nil), v...)
			continue
		}

		delete(r.Fields, field)
	}
}

// snakeToCamel converts a snake_case field-mask segment to the lowerCamelCase
// JSON body key. A segment that is already camelCase (no underscore) is returned
// unchanged, so the conversion is idempotent.
func snakeToCamel(s string) string {
	if !strings.Contains(s, "_") {
		return s
	}

	var b strings.Builder

	upperNext := false

	for _, r := range s {
		switch {
		case r == '_':
			upperNext = true
		case upperNext:
			upperNext = false

			b.WriteString(strings.ToUpper(string(r)))
		default:
			b.WriteRune(r)
		}
	}

	return b.String()
}
