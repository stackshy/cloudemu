// Package dataplex provides an in-memory mock of the Google Cloud Dataplex
// control plane (dataplex.googleapis.com/v1). It models the lake → zone → asset
// hierarchy and the long-running operations their mutating RPCs return. It is
// control-plane only: tasks, entryGroups, entities, dataScans, environments, and
// IAM verbs are out of scope.
//
// A zone create requires its parent lake to exist; an asset create requires its
// parent lake and zone. Deleting a lake cascades to its zones and their assets;
// deleting a zone cascades to its assets. Every resource is keyed by its full GCP
// resource name, so a trailing-slash-bounded prefix scan cascades a parent delete
// to its descendants without a sibling-prefix collision (deleting lake `l1` must
// not touch `l10`'s children).
package dataplex

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
	dpdriver "github.com/stackshy/cloudemu/v2/services/dataplex/driver"
)

var _ dpdriver.Dataplex = (*Mock)(nil)

const (
	lakesColl  = "lakes"
	zonesColl  = "zones"
	assetsColl = "assets"
)

// Mock is the in-memory Dataplex control-plane implementation. Each resource
// level is keyed by its full GCP resource name.
type Mock struct {
	mu sync.RWMutex

	lakes      *memstore.Store[dpdriver.Resource]
	zones      *memstore.Store[dpdriver.Resource]
	assets     *memstore.Store[dpdriver.Resource]
	operations *memstore.Store[dpdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Dataplex mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		lakes:      memstore.New[dpdriver.Resource](),
		zones:      memstore.New[dpdriver.Resource](),
		assets:     memstore.New[dpdriver.Resource](),
		operations: memstore.New[dpdriver.Operation](),
		opts:       opts,
	}
}

// locationScope is the project+location prefix every resource name shares.
func locationScope(project, location string) string {
	return "projects/" + project + "/locations/" + location
}

func lakeName(project, location, lake string) string {
	return locationScope(project, location) + "/" + lakesColl + "/" + lake
}

func zoneName(project, location, lake, zone string) string {
	return lakeName(project, location, lake) + "/" + zonesColl + "/" + zone
}

func assetName(project, location, lake, zone, asset string) string {
	return zoneName(project, location, lake, zone) + "/" + assetsColl + "/" + asset
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *dpdriver.Operation {
	op := dpdriver.Operation{
		Name:       fmt.Sprintf("%s/operations/operation-%d-%s", locationScope(project, location), m.opSeq.Add(1), idgen.UUID()),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// storeResource builds and stores a new resource with derived create/update
// times. The caller holds the write lock and has already checked for a duplicate.
func (m *Mock) storeResource(store *memstore.Store[dpdriver.Resource], key string, res *dpdriver.Resource) dpdriver.Resource {
	now := m.opts.Clock.Now().UTC()
	res.CreateTime = now
	res.UpdateTime = now
	res.Fields = cloneRawMap(res.Fields)
	store.Set(key, *res)

	return cloneResource(res)
}

// CreateLake provisions a new lake.
func (m *Mock) CreateLake(_ context.Context, cfg *dpdriver.Config) (*dpdriver.Resource, *dpdriver.Operation, error) {
	if err := requireIDLocation(cfg, lakesColl); err != nil {
		return nil, nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := lakeName(cfg.Project, cfg.Location, cfg.ID)
	if m.lakes.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "lake %q already exists", key)
	}

	out := m.storeResource(m.lakes, key, &dpdriver.Resource{
		Project: cfg.Project, Location: cfg.Location, ID: cfg.ID, Fields: cfg.Fields,
	})

	return &out, m.newOp(cfg.Project, cfg.Location, "create", key), nil
}

// GetLake returns a lake by identity.
func (m *Mock) GetLake(_ context.Context, project, location, lake string) (*dpdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return getFrom(m.lakes, lakesColl, lakeName(project, location, lake))
}

// ListLakes returns every lake in a project+location.
func (m *Mock) ListLakes(_ context.Context, project, location string) ([]dpdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return listPrefixed(m.lakes, locationScope(project, location)+"/"+lakesColl+"/"), nil
}

// PatchLake applies a masked update to a lake.
func (m *Mock) PatchLake(_ context.Context, cfg *dpdriver.Config, mask []string) (*dpdriver.Resource, *dpdriver.Operation, error) {
	return m.patch(m.lakes, lakesColl, lakeName(cfg.Project, cfg.Location, cfg.ID), cfg, mask)
}

// DeleteLake removes a lake and cascades to its zones and their assets.
func (m *Mock) DeleteLake(_ context.Context, project, location, lake string) (*dpdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := lakeName(project, location, lake)
	if !m.lakes.Has(key) {
		return nil, notFound(lakesColl, key)
	}

	m.lakes.Delete(key)
	deletePrefixed(m.zones, key+"/"+zonesColl+"/")
	deletePrefixed(m.assets, key+"/"+zonesColl+"/")

	return m.newOp(project, location, "delete", key), nil
}

// CreateZone provisions a new zone under an existing lake.
func (m *Mock) CreateZone(_ context.Context, cfg *dpdriver.Config) (*dpdriver.Resource, *dpdriver.Operation, error) {
	if err := requireIDLocation(cfg, zonesColl); err != nil {
		return nil, nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	parent := lakeName(cfg.Project, cfg.Location, cfg.Lake)
	if !m.lakes.Has(parent) {
		return nil, nil, notFound(lakesColl, parent)
	}

	key := zoneName(cfg.Project, cfg.Location, cfg.Lake, cfg.ID)
	if m.zones.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "zone %q already exists", key)
	}

	out := m.storeResource(m.zones, key, &dpdriver.Resource{
		Project: cfg.Project, Location: cfg.Location, Lake: cfg.Lake, ID: cfg.ID, Fields: cfg.Fields,
	})

	return &out, m.newOp(cfg.Project, cfg.Location, "create", key), nil
}

// GetZone returns a zone by identity.
func (m *Mock) GetZone(_ context.Context, project, location, lake, zone string) (*dpdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return getFrom(m.zones, zonesColl, zoneName(project, location, lake, zone))
}

// ListZones returns every zone under a lake.
func (m *Mock) ListZones(_ context.Context, project, location, lake string) ([]dpdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return listPrefixed(m.zones, lakeName(project, location, lake)+"/"+zonesColl+"/"), nil
}

// PatchZone applies a masked update to a zone.
func (m *Mock) PatchZone(_ context.Context, cfg *dpdriver.Config, mask []string) (*dpdriver.Resource, *dpdriver.Operation, error) {
	return m.patch(m.zones, zonesColl, zoneName(cfg.Project, cfg.Location, cfg.Lake, cfg.ID), cfg, mask)
}

// DeleteZone removes a zone and cascades to its assets.
func (m *Mock) DeleteZone(_ context.Context, project, location, lake, zone string) (*dpdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := zoneName(project, location, lake, zone)
	if !m.zones.Has(key) {
		return nil, notFound(zonesColl, key)
	}

	m.zones.Delete(key)
	deletePrefixed(m.assets, key+"/"+assetsColl+"/")

	return m.newOp(project, location, "delete", key), nil
}

// CreateAsset provisions a new asset under an existing zone.
func (m *Mock) CreateAsset(_ context.Context, cfg *dpdriver.Config) (*dpdriver.Resource, *dpdriver.Operation, error) {
	if err := requireIDLocation(cfg, assetsColl); err != nil {
		return nil, nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	parent := zoneName(cfg.Project, cfg.Location, cfg.Lake, cfg.Zone)
	if !m.zones.Has(parent) {
		return nil, nil, notFound(zonesColl, parent)
	}

	key := assetName(cfg.Project, cfg.Location, cfg.Lake, cfg.Zone, cfg.ID)
	if m.assets.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "asset %q already exists", key)
	}

	out := m.storeResource(m.assets, key, &dpdriver.Resource{
		Project: cfg.Project, Location: cfg.Location, Lake: cfg.Lake, Zone: cfg.Zone, ID: cfg.ID, Fields: cfg.Fields,
	})

	return &out, m.newOp(cfg.Project, cfg.Location, "create", key), nil
}

// GetAsset returns an asset by identity.
func (m *Mock) GetAsset(_ context.Context, project, location, lake, zone, asset string) (*dpdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return getFrom(m.assets, assetsColl, assetName(project, location, lake, zone, asset))
}

// ListAssets returns every asset under a zone.
func (m *Mock) ListAssets(_ context.Context, project, location, lake, zone string) ([]dpdriver.Resource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return listPrefixed(m.assets, zoneName(project, location, lake, zone)+"/"+assetsColl+"/"), nil
}

// PatchAsset applies a masked update to an asset.
func (m *Mock) PatchAsset(_ context.Context, cfg *dpdriver.Config, mask []string) (*dpdriver.Resource, *dpdriver.Operation, error) {
	key := assetName(cfg.Project, cfg.Location, cfg.Lake, cfg.Zone, cfg.ID)

	return m.patch(m.assets, assetsColl, key, cfg, mask)
}

// DeleteAsset removes an asset.
func (m *Mock) DeleteAsset(_ context.Context, project, location, lake, zone, asset string) (*dpdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := assetName(project, location, lake, zone, asset)
	if !m.assets.Has(key) {
		return nil, notFound(assetsColl, key)
	}

	m.assets.Delete(key)

	return m.newOp(project, location, "delete", key), nil
}

// patch applies a masked update to a stored resource and returns the completed
// LRO. Only the masked top-level body fields are written; a field outside the
// mask is left untouched. An empty mask replaces every field in the request body.
func (m *Mock) patch(
	store *memstore.Store[dpdriver.Resource], coll, key string, cfg *dpdriver.Config, mask []string,
) (*dpdriver.Resource, *dpdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := store.Get(key)
	if !ok {
		return nil, nil, notFound(coll, key)
	}

	applyMask(&r, cfg.Fields, mask)
	r.UpdateTime = m.opts.Clock.Now().UTC()
	store.Set(key, r)

	op := m.newOp(cfg.Project, cfg.Location, "update", key)
	out := cloneResource(&r)

	return &out, op, nil
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op id
// an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*dpdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &dpdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// getFrom returns a resource by full name, cloned. The caller holds a read lock.
func getFrom(store *memstore.Store[dpdriver.Resource], coll, key string) (*dpdriver.Resource, error) {
	r, ok := store.Get(key)
	if !ok {
		return nil, notFound(coll, key)
	}

	out := cloneResource(&r)

	return &out, nil
}

// listPrefixed returns every resource whose full name starts with prefix, cloned
// and id-ordered. The caller holds a read lock.
func listPrefixed(store *memstore.Store[dpdriver.Resource], prefix string) []dpdriver.Resource {
	all := store.SortedValues()
	out := make([]dpdriver.Resource, 0, len(all))

	for i := range all {
		if strings.HasPrefix(fullName(&all[i]), prefix) {
			out = append(out, cloneResource(&all[i]))
		}
	}

	return out
}

// fullName rebuilds a resource's full GCP name from its stored components.
func fullName(r *dpdriver.Resource) string {
	switch {
	case r.Lake == "":
		return lakeName(r.Project, r.Location, r.ID)
	case r.Zone == "":
		return zoneName(r.Project, r.Location, r.Lake, r.ID)
	default:
		return assetName(r.Project, r.Location, r.Lake, r.Zone, r.ID)
	}
}

// requireIDLocation validates the identity fields every create needs.
func requireIDLocation(cfg *dpdriver.Config, coll string) error {
	if cfg.ID == "" {
		return cerrors.New(cerrors.InvalidArgument, coll+" id is required")
	}

	if cfg.Location == "" {
		return cerrors.New(cerrors.InvalidArgument, "location is required")
	}

	return nil
}

// notFound builds the NOT_FOUND error carrying the full resource name, as real
// Dataplex does for a Get/Patch/Delete of a missing resource (or a create under a
// missing parent).
func notFound(coll, key string) error {
	return cerrors.Newf(cerrors.NotFound, "%s %q not found", strings.TrimSuffix(coll, "s"), key)
}

// deletePrefixed removes every entry in s whose key starts with prefix. The
// trailing-slash-bounded prefix keeps a lake `l1` delete from touching `l10`'s
// descendants.
func deletePrefixed[V any](s *memstore.Store[V], prefix string) {
	for _, k := range s.Keys() {
		if strings.HasPrefix(k, prefix) {
			s.Delete(k)
		}
	}
}

// applyMask folds the masked top-level body fields from desired into the stored
// resource. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every field
// present in desired (lenient full-body update).
func applyMask(r *dpdriver.Resource, desired map[string]json.RawMessage, mask []string) {
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
