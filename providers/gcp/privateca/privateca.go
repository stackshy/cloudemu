// Package privateca provides an in-memory mock of the Google Certificate
// Authority Service control plane (privateca.googleapis.com/v1). It models CA
// pools, certificate authorities, certificate templates, and certificates and the
// long-running operations their mutating RPCs return. It is control-plane only
// and performs no real X.509 crypto: PEM material is deterministic placeholder
// data minted once at create and stored, so it is byte-stable across reads. The
// certificate-authority lifecycle verbs (enable/disable/undelete/activate/fetch)
// drive the real CA State machine; certificates carry a revoke verb.
package privateca

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
	pcadriver "github.com/stackshy/cloudemu/v2/services/privateca/driver"
)

var _ pcadriver.PrivateCA = (*Mock)(nil)

const (
	caPoolsColl        = "caPools"
	authoritiesColl    = "certificateAuthorities"
	templatesColl      = "certificateTemplates"
	certificatesColl   = "certificates"
	defaultTier        = "ENTERPRISE"
	typeSelfSigned     = "SELF_SIGNED"
	typeSubordinate    = "SUBORDINATE"
	stateEnabled       = "ENABLED"
	stateDisabled      = "DISABLED"
	stateStaged        = "STAGED"
	stateAwaitingUser  = "AWAITING_USER_ACTIVATION"
	stateDeleted       = "DELETED"
	stateRevokedMarker = "REVOKED"
)

// Mock is the in-memory Certificate Authority Service control-plane
// implementation. Each resource collection is keyed by its full GCP resource
// name.
type Mock struct {
	mu sync.RWMutex

	pools        *memstore.Store[pcadriver.Resource]
	authorities  *memstore.Store[pcadriver.Resource]
	templates    *memstore.Store[pcadriver.Resource]
	certificates *memstore.Store[pcadriver.Resource]
	operations   *memstore.Store[pcadriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Certificate Authority Service mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		pools:        memstore.New[pcadriver.Resource](),
		authorities:  memstore.New[pcadriver.Resource](),
		templates:    memstore.New[pcadriver.Resource](),
		certificates: memstore.New[pcadriver.Resource](),
		operations:   memstore.New[pcadriver.Operation](),
		opts:         opts,
	}
}

// resourceKey builds the full resource name for a collection. caPool is set only
// for the collections nested under a caPool (certificateAuthorities,
// certificates) and empty otherwise.
func resourceKey(coll, project, location, caPool, id string) string {
	base := "projects/" + project + "/locations/" + location
	if caPool != "" {
		base += "/caPools/" + caPool
	}

	return base + "/" + coll + "/" + id
}

// listPrefix builds the resource-name prefix that scopes a List to a project +
// location (+ caPool for a nested collection).
func listPrefix(coll, project, location, caPool string) string {
	base := "projects/" + project + "/locations/" + location
	if caPool != "" {
		base += "/caPools/" + caPool
	}

	return base + "/" + coll + "/"
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *pcadriver.Operation {
	scope := "projects/" + project + "/locations/" + location
	op := pcadriver.Operation{
		Name:       fmt.Sprintf("%s/operations/operation-%d-%s", scope, m.opSeq.Add(1), idgen.UUID()),
		Done:       true,
		TargetName: target,
		Type:       opType,
	}
	m.operations.Set(op.Name, op)

	return &op
}

// create provisions a new resource in the given collection with computed output
// fields derived deterministically, and returns the completed LRO. seed, when
// non-nil, mints collection-specific computed values (a CA's state / PEM) into the
// resource under the write lock before it is stored.
func (m *Mock) create(
	store *memstore.Store[pcadriver.Resource], coll string, cfg *pcadriver.Config,
	seed func(r *pcadriver.Resource) error,
) (*pcadriver.Resource, *pcadriver.Operation, error) {
	if cfg.ID == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, coll+" id is required")
	}

	if cfg.Location == "" {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "location is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceKey(coll, cfg.Project, cfg.Location, cfg.CaPool, cfg.ID)
	if store.Has(key) {
		return nil, nil, cerrors.Newf(cerrors.AlreadyExists, "%s %q already exists", coll, cfg.ID)
	}

	now := m.opts.Clock.Now().UTC()
	res := pcadriver.Resource{
		Project:    cfg.Project,
		Location:   cfg.Location,
		CaPool:     cfg.CaPool,
		ID:         cfg.ID,
		CreateTime: now,
		UpdateTime: now,
		Fields:     cloneRawMap(cfg.Fields),
	}

	if seed != nil {
		if err := seed(&res); err != nil {
			return nil, nil, err
		}
	}

	store.Set(key, res)

	op := m.newOp(cfg.Project, cfg.Location, "create", key)
	out := cloneResource(&res)

	return &out, op, nil
}

// get returns a resource by its identity, cloned.
func (m *Mock) get(store *memstore.Store[pcadriver.Resource], coll, project, location, caPool, id string) (
	*pcadriver.Resource, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := store.Get(resourceKey(coll, project, location, caPool, id))
	if !ok {
		return nil, notFoundErr(coll, project, location, caPool, id)
	}

	out := cloneResource(&r)

	return &out, nil
}

// list returns every resource in a scope, ordered by resource name.
func (m *Mock) list(store *memstore.Store[pcadriver.Resource], coll, project, location, caPool string) (
	[]pcadriver.Resource, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := listPrefix(coll, project, location, caPool)
	all := store.SortedValues()
	out := make([]pcadriver.Resource, 0, len(all))

	for i := range all {
		key := resourceKey(coll, all[i].Project, all[i].Location, all[i].CaPool, all[i].ID)
		if strings.HasPrefix(key, prefix) {
			out = append(out, cloneResource(&all[i]))
		}
	}

	return out, nil
}

// patch applies a masked update and returns the completed LRO. Only the masked
// top-level body fields are written; a field outside the mask is left untouched.
// An empty mask replaces every field present in the request body.
func (m *Mock) patch(store *memstore.Store[pcadriver.Resource], coll string, cfg *pcadriver.Config, mask []string) (
	*pcadriver.Resource, *pcadriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceKey(coll, cfg.Project, cfg.Location, cfg.CaPool, cfg.ID)

	r, ok := store.Get(key)
	if !ok {
		return nil, nil, notFoundErr(coll, cfg.Project, cfg.Location, cfg.CaPool, cfg.ID)
	}

	applyMask(&r, cfg.Fields, mask)
	r.UpdateTime = m.opts.Clock.Now().UTC()
	store.Set(key, r)

	op := m.newOp(cfg.Project, cfg.Location, "update", key)
	out := cloneResource(&r)

	return &out, op, nil
}

// del removes a resource and returns the completed LRO. It is the hard-delete
// used by caPools and certificateTemplates; certificateAuthorities soft-delete
// via delAuthority instead.
func (m *Mock) del(store *memstore.Store[pcadriver.Resource], coll, project, location, id string) (
	*pcadriver.Operation, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceKey(coll, project, location, "", id)
	if !store.Has(key) {
		return nil, notFoundErr(coll, project, location, "", id)
	}

	store.Delete(key)

	return m.newOp(project, location, "delete", key), nil
}

// CreateCaPool provisions a new CA pool, defaulting its tier to ENTERPRISE.
func (m *Mock) CreateCaPool(_ context.Context, cfg *pcadriver.Config) (
	*pcadriver.Resource, *pcadriver.Operation, error,
) {
	return m.create(m.pools, caPoolsColl, cfg, seedCaPool)
}

// GetCaPool returns a CA pool by identity.
func (m *Mock) GetCaPool(_ context.Context, project, location, id string) (*pcadriver.Resource, error) {
	return m.get(m.pools, caPoolsColl, project, location, "", id)
}

// ListCaPools returns every CA pool in a project+location.
func (m *Mock) ListCaPools(_ context.Context, project, location string) ([]pcadriver.Resource, error) {
	return m.list(m.pools, caPoolsColl, project, location, "")
}

// PatchCaPool applies a masked update to a CA pool.
func (m *Mock) PatchCaPool(_ context.Context, cfg *pcadriver.Config, mask []string) (
	*pcadriver.Resource, *pcadriver.Operation, error,
) {
	return m.patch(m.pools, caPoolsColl, cfg, mask)
}

// DeleteCaPool removes a CA pool.
func (m *Mock) DeleteCaPool(_ context.Context, project, location, id string) (*pcadriver.Operation, error) {
	return m.del(m.pools, caPoolsColl, project, location, id)
}

// CreateCertificateTemplate provisions a new certificate template.
func (m *Mock) CreateCertificateTemplate(_ context.Context, cfg *pcadriver.Config) (
	*pcadriver.Resource, *pcadriver.Operation, error,
) {
	return m.create(m.templates, templatesColl, cfg, nil)
}

// GetCertificateTemplate returns a certificate template by identity.
func (m *Mock) GetCertificateTemplate(_ context.Context, project, location, id string) (*pcadriver.Resource, error) {
	return m.get(m.templates, templatesColl, project, location, "", id)
}

// ListCertificateTemplates returns every certificate template in a
// project+location.
func (m *Mock) ListCertificateTemplates(_ context.Context, project, location string) ([]pcadriver.Resource, error) {
	return m.list(m.templates, templatesColl, project, location, "")
}

// PatchCertificateTemplate applies a masked update to a certificate template.
func (m *Mock) PatchCertificateTemplate(_ context.Context, cfg *pcadriver.Config, mask []string) (
	*pcadriver.Resource, *pcadriver.Operation, error,
) {
	return m.patch(m.templates, templatesColl, cfg, mask)
}

// DeleteCertificateTemplate removes a certificate template.
func (m *Mock) DeleteCertificateTemplate(_ context.Context, project, location, id string) (*pcadriver.Operation, error) {
	return m.del(m.templates, templatesColl, project, location, id)
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op id
// an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*pcadriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &pcadriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// notFoundErr builds the NOT_FOUND error carrying the full resource name, as real
// CA Service does for a Get/Patch/Delete/verb of a missing resource.
func notFoundErr(coll, project, location, caPool, id string) error {
	return cerrors.Newf(cerrors.NotFound, "%s %q not found", coll, resourceKey(coll, project, location, caPool, id))
}

// seedCaPool defaults a CA pool's required immutable tier to ENTERPRISE when the
// caller omits it, matching a permissive create.
func seedCaPool(r *pcadriver.Resource) error {
	if _, ok := r.Fields["tier"]; !ok {
		if r.Fields == nil {
			r.Fields = map[string]json.RawMessage{}
		}

		r.Fields["tier"] = json.RawMessage(`"` + defaultTier + `"`)
	}

	return nil
}

// applyMask folds the masked top-level body fields from desired into the stored
// resource. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every field
// present in desired (lenient full-body update). Computed keys the wire layer owns
// (state, pemCaCertificates, …) are never in desired, so a patch cannot clobber
// them.
func applyMask(r *pcadriver.Resource, desired map[string]json.RawMessage, mask []string) {
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
