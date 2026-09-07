// Package gkebackup provides an in-memory mock of the Google Backup for GKE
// control plane (gkebackup.googleapis.com/v1). It models backup plans and
// restore plans and the long-running operations their mutating RPCs return. It
// is control-plane only: the Backups/Restores nested under a plan (a
// data-plane-ish sub-resource) and IAM policy verbs are out of scope.
package gkebackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	gkbdriver "github.com/stackshy/cloudemu/v2/services/gkebackup/driver"
)

var _ gkbdriver.GKEBackup = (*Mock)(nil)

const (
	backupPlansColl  = "backupPlans"
	restorePlansColl = "restorePlans"

	// stateReady is the state a plan is minted with; the mock has no cluster or
	// backup data plane, so a plan is operational the moment it is created.
	stateReady = "READY"
	// stateDeactivated is the state a backupPlan reports while its deactivated
	// flag is set, matching the real API.
	stateDeactivated = "DEACTIVATED"
)

// Mock is the in-memory Backup for GKE control-plane implementation. Each
// resource collection is keyed by its full GCP resource name.
type Mock struct {
	mu sync.RWMutex

	backupPlans  *memstore.Store[gkbdriver.Resource]
	restorePlans *memstore.Store[gkbdriver.Resource]
	operations   *memstore.Store[gkbdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Backup for GKE mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		backupPlans:  memstore.New[gkbdriver.Resource](),
		restorePlans: memstore.New[gkbdriver.Resource](),
		operations:   memstore.New[gkbdriver.Operation](),
		opts:         opts,
	}
}

// resourceName builds the full resource name for a collection.
func resourceName(coll, project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + coll + "/" + id
}

// backupPlanState reports the output-only state of a backupPlan: DEACTIVATED
// while its deactivated flag is set, READY otherwise.
func backupPlanState(fields map[string]json.RawMessage) string {
	var deactivated bool
	if raw, ok := fields["deactivated"]; ok {
		_ = json.Unmarshal(raw, &deactivated)
	}

	if deactivated {
		return stateDeactivated
	}

	return stateReady
}

// restorePlanState reports the output-only state of a restorePlan. A restorePlan
// has no deactivated flag, so it is always READY in the mock.
func restorePlanState(map[string]json.RawMessage) string { return stateReady }

// deterministicUID derives the stable output-only uid from a resource's full
// name, so it is identical on the create response and every later read (a
// Terraform refresh reads it back and would drift on a random value).
func deterministicUID(name string) string {
	sum := sha256.Sum256([]byte(name))
	h := hex.EncodeToString(sum[:])

	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// computeEtag derives the etag from the resource's uid, state, and body content,
// so it is byte-stable across reads and changes whenever a mutation changes the
// content or state (real Backup for GKE recomputes etag on every write).
func computeEtag(uid, state string, fields map[string]json.RawMessage) string {
	h := sha256.New()
	h.Write([]byte(uid))
	h.Write([]byte{0})
	h.Write([]byte(state))

	for _, k := range sortedKeys(fields) {
		h.Write([]byte{0})
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(fields[k])
	}

	return hex.EncodeToString(h.Sum(nil))[:24]
}

// sortedKeys returns the keys of a raw body map in sorted order, so etag content
// hashing is deterministic regardless of map iteration order.
func sortedKeys(fields map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *gkbdriver.Operation {
	scope := "projects/" + project + "/locations/" + location
	op := gkbdriver.Operation{
		Name:       fmt.Sprintf("%s/operations/operation-%d-%s", scope, m.opSeq.Add(1), idgen.UUID()),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// create provisions a new resource in the given collection with computed output
// fields (uid, etag, state, timestamps) derived deterministically, and returns
// the completed LRO.
func (m *Mock) create(
	store *memstore.Store[gkbdriver.Resource], coll string,
	cfg *gkbdriver.Config, stateFn func(map[string]json.RawMessage) string,
) (*gkbdriver.Resource, *gkbdriver.Operation, error) {
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
	fields := cloneRawMap(cfg.Fields)
	state := stateFn(fields)
	uid := deterministicUID(key)
	res := gkbdriver.Resource{
		Project:    cfg.Project,
		Location:   cfg.Location,
		ID:         cfg.ID,
		UID:        uid,
		State:      state,
		Etag:       computeEtag(uid, state, fields),
		CreateTime: now,
		UpdateTime: now,
		Fields:     fields,
	}
	store.Set(key, res)

	op := m.newOp(cfg.Project, cfg.Location, "create", key)
	out := cloneResource(&res)

	return &out, op, nil
}

// get returns a resource by its identity, cloned.
func (m *Mock) get(store *memstore.Store[gkbdriver.Resource], coll, project, location, id string) (
	*gkbdriver.Resource, error,
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
func (m *Mock) list(store *memstore.Store[gkbdriver.Resource], coll, project, location string) (
	[]gkbdriver.Resource, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + coll + "/"
	all := store.SortedValues()
	out := make([]gkbdriver.Resource, 0, len(all))

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
// An empty mask replaces every field present in the request body. State is
// re-derived from the resulting fields and etag is recomputed.
func (m *Mock) patch(
	store *memstore.Store[gkbdriver.Resource], coll string,
	cfg *gkbdriver.Config, mask []string, stateFn func(map[string]json.RawMessage) string,
) (*gkbdriver.Resource, *gkbdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(coll, cfg.Project, cfg.Location, cfg.ID)

	r, ok := store.Get(key)
	if !ok {
		return nil, nil, notFoundErr(coll, cfg.Project, cfg.Location, cfg.ID)
	}

	applyMask(&r, cfg.Fields, mask)
	r.State = stateFn(r.Fields)
	r.Etag = computeEtag(r.UID, r.State, r.Fields)
	r.UpdateTime = m.opts.Clock.Now().UTC()
	store.Set(key, r)

	op := m.newOp(cfg.Project, cfg.Location, "update", key)
	out := cloneResource(&r)

	return &out, op, nil
}

// del removes a resource and returns the completed LRO.
func (m *Mock) del(store *memstore.Store[gkbdriver.Resource], coll, project, location, id string) (
	*gkbdriver.Operation, error,
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

// CreateBackupPlan provisions a new backup plan.
func (m *Mock) CreateBackupPlan(_ context.Context, cfg *gkbdriver.Config) (
	*gkbdriver.Resource, *gkbdriver.Operation, error,
) {
	return m.create(m.backupPlans, backupPlansColl, cfg, backupPlanState)
}

// GetBackupPlan returns a backup plan by identity.
func (m *Mock) GetBackupPlan(_ context.Context, project, location, id string) (*gkbdriver.Resource, error) {
	return m.get(m.backupPlans, backupPlansColl, project, location, id)
}

// ListBackupPlans returns every backup plan in a project+location.
func (m *Mock) ListBackupPlans(_ context.Context, project, location string) ([]gkbdriver.Resource, error) {
	return m.list(m.backupPlans, backupPlansColl, project, location)
}

// PatchBackupPlan applies a masked update to a backup plan.
func (m *Mock) PatchBackupPlan(_ context.Context, cfg *gkbdriver.Config, mask []string) (
	*gkbdriver.Resource, *gkbdriver.Operation, error,
) {
	return m.patch(m.backupPlans, backupPlansColl, cfg, mask, backupPlanState)
}

// DeleteBackupPlan removes a backup plan.
func (m *Mock) DeleteBackupPlan(_ context.Context, project, location, id string) (*gkbdriver.Operation, error) {
	return m.del(m.backupPlans, backupPlansColl, project, location, id)
}

// CreateRestorePlan provisions a new restore plan.
func (m *Mock) CreateRestorePlan(_ context.Context, cfg *gkbdriver.Config) (
	*gkbdriver.Resource, *gkbdriver.Operation, error,
) {
	return m.create(m.restorePlans, restorePlansColl, cfg, restorePlanState)
}

// GetRestorePlan returns a restore plan by identity.
func (m *Mock) GetRestorePlan(_ context.Context, project, location, id string) (*gkbdriver.Resource, error) {
	return m.get(m.restorePlans, restorePlansColl, project, location, id)
}

// ListRestorePlans returns every restore plan in a project+location.
func (m *Mock) ListRestorePlans(_ context.Context, project, location string) ([]gkbdriver.Resource, error) {
	return m.list(m.restorePlans, restorePlansColl, project, location)
}

// PatchRestorePlan applies a masked update to a restore plan.
func (m *Mock) PatchRestorePlan(_ context.Context, cfg *gkbdriver.Config, mask []string) (
	*gkbdriver.Resource, *gkbdriver.Operation, error,
) {
	return m.patch(m.restorePlans, restorePlansColl, cfg, mask, restorePlanState)
}

// DeleteRestorePlan removes a restore plan.
func (m *Mock) DeleteRestorePlan(_ context.Context, project, location, id string) (*gkbdriver.Operation, error) {
	return m.del(m.restorePlans, restorePlansColl, project, location, id)
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op
// id an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*gkbdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &gkbdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// notFoundErr builds the NOT_FOUND error carrying the full resource name, as
// real Backup for GKE does for a Get/Patch/Delete of a missing resource.
func notFoundErr(coll, project, location, id string) error {
	return cerrors.Newf(cerrors.NotFound, "%s %q not found", coll, resourceName(coll, project, location, id))
}

// applyMask folds the masked top-level body fields from desired into the stored
// resource. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every
// field present in desired (lenient full-body update).
func applyMask(r *gkbdriver.Resource, desired map[string]json.RawMessage, mask []string) {
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
