// Package certificatemanager provides an in-memory mock of the Google
// Certificate Manager control plane (certificatemanager.googleapis.com/v1). It
// models certificates, certificate maps, and DNS authorizations and the
// long-running operations their mutating RPCs return. It is control-plane only:
// certificateMapEntries, certificateIssuanceConfigs, trustConfigs, IAM verbs,
// and any real certificate issuance (the data plane) are out of scope.
package certificatemanager

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
	cmdriver "github.com/stackshy/cloudemu/v2/services/certificatemanager/driver"
)

var _ cmdriver.CertificateManager = (*Mock)(nil)

const (
	certificatesColl      = "certificates"
	certificateMapsColl   = "certificateMaps"
	dnsAuthorizationsColl = "dnsAuthorizations"
)

// Mock is the in-memory Certificate Manager control-plane implementation. Each
// resource collection is keyed by its full GCP resource name.
type Mock struct {
	mu sync.RWMutex

	certificates *memstore.Store[cmdriver.Resource]
	maps         *memstore.Store[cmdriver.Resource]
	dnsAuths     *memstore.Store[cmdriver.Resource]
	operations   *memstore.Store[cmdriver.Operation]

	opSeq atomic.Uint64
	opts  *config.Options
}

// New creates a new Certificate Manager mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		certificates: memstore.New[cmdriver.Resource](),
		maps:         memstore.New[cmdriver.Resource](),
		dnsAuths:     memstore.New[cmdriver.Resource](),
		operations:   memstore.New[cmdriver.Operation](),
		opts:         opts,
	}
}

// resourceName builds the full resource name for a collection.
func resourceName(coll, project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/" + coll + "/" + id
}

// newOp records a completed operation scoped to the project+location it acted in
// and returns it. The caller holds the write lock.
func (m *Mock) newOp(project, location, opType, target string) *cmdriver.Operation {
	scope := "projects/" + project + "/locations/" + location
	op := cmdriver.Operation{
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
func (m *Mock) create(store *memstore.Store[cmdriver.Resource], coll string, cfg *cmdriver.Config) (
	*cmdriver.Resource, *cmdriver.Operation, error,
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
	res := cmdriver.Resource{
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
func (m *Mock) get(store *memstore.Store[cmdriver.Resource], coll, project, location, id string) (
	*cmdriver.Resource, error,
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
func (m *Mock) list(store *memstore.Store[cmdriver.Resource], coll, project, location string) (
	[]cmdriver.Resource, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + coll + "/"
	all := store.SortedValues()
	out := make([]cmdriver.Resource, 0, len(all))

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
func (m *Mock) patch(store *memstore.Store[cmdriver.Resource], coll string, cfg *cmdriver.Config, mask []string) (
	*cmdriver.Resource, *cmdriver.Operation, error,
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
func (m *Mock) del(store *memstore.Store[cmdriver.Resource], coll, project, location, id string) (
	*cmdriver.Operation, error,
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

// CreateCertificate provisions a new certificate.
func (m *Mock) CreateCertificate(_ context.Context, cfg *cmdriver.Config) (
	*cmdriver.Resource, *cmdriver.Operation, error,
) {
	return m.create(m.certificates, certificatesColl, cfg)
}

// GetCertificate returns a certificate by identity.
func (m *Mock) GetCertificate(_ context.Context, project, location, id string) (*cmdriver.Resource, error) {
	return m.get(m.certificates, certificatesColl, project, location, id)
}

// ListCertificates returns every certificate in a project+location.
func (m *Mock) ListCertificates(_ context.Context, project, location string) ([]cmdriver.Resource, error) {
	return m.list(m.certificates, certificatesColl, project, location)
}

// PatchCertificate applies a masked update to a certificate.
func (m *Mock) PatchCertificate(_ context.Context, cfg *cmdriver.Config, mask []string) (
	*cmdriver.Resource, *cmdriver.Operation, error,
) {
	return m.patch(m.certificates, certificatesColl, cfg, mask)
}

// DeleteCertificate removes a certificate.
func (m *Mock) DeleteCertificate(_ context.Context, project, location, id string) (*cmdriver.Operation, error) {
	return m.del(m.certificates, certificatesColl, project, location, id)
}

// CreateCertificateMap provisions a new certificate map.
func (m *Mock) CreateCertificateMap(_ context.Context, cfg *cmdriver.Config) (
	*cmdriver.Resource, *cmdriver.Operation, error,
) {
	return m.create(m.maps, certificateMapsColl, cfg)
}

// GetCertificateMap returns a certificate map by identity.
func (m *Mock) GetCertificateMap(_ context.Context, project, location, id string) (*cmdriver.Resource, error) {
	return m.get(m.maps, certificateMapsColl, project, location, id)
}

// ListCertificateMaps returns every certificate map in a project+location.
func (m *Mock) ListCertificateMaps(_ context.Context, project, location string) ([]cmdriver.Resource, error) {
	return m.list(m.maps, certificateMapsColl, project, location)
}

// PatchCertificateMap applies a masked update to a certificate map.
func (m *Mock) PatchCertificateMap(_ context.Context, cfg *cmdriver.Config, mask []string) (
	*cmdriver.Resource, *cmdriver.Operation, error,
) {
	return m.patch(m.maps, certificateMapsColl, cfg, mask)
}

// DeleteCertificateMap removes a certificate map.
func (m *Mock) DeleteCertificateMap(_ context.Context, project, location, id string) (*cmdriver.Operation, error) {
	return m.del(m.maps, certificateMapsColl, project, location, id)
}

// CreateDNSAuthorization provisions a new DNS authorization.
func (m *Mock) CreateDNSAuthorization(_ context.Context, cfg *cmdriver.Config) (
	*cmdriver.Resource, *cmdriver.Operation, error,
) {
	return m.create(m.dnsAuths, dnsAuthorizationsColl, cfg)
}

// GetDNSAuthorization returns a DNS authorization by identity.
func (m *Mock) GetDNSAuthorization(_ context.Context, project, location, id string) (*cmdriver.Resource, error) {
	return m.get(m.dnsAuths, dnsAuthorizationsColl, project, location, id)
}

// ListDNSAuthorizations returns every DNS authorization in a project+location.
func (m *Mock) ListDNSAuthorizations(_ context.Context, project, location string) ([]cmdriver.Resource, error) {
	return m.list(m.dnsAuths, dnsAuthorizationsColl, project, location)
}

// PatchDNSAuthorization applies a masked update to a DNS authorization.
func (m *Mock) PatchDNSAuthorization(_ context.Context, cfg *cmdriver.Config, mask []string) (
	*cmdriver.Resource, *cmdriver.Operation, error,
) {
	return m.patch(m.dnsAuths, dnsAuthorizationsColl, cfg, mask)
}

// DeleteDNSAuthorization removes a DNS authorization.
func (m *Mock) DeleteDNSAuthorization(_ context.Context, project, location, id string) (*cmdriver.Operation, error) {
	return m.del(m.dnsAuths, dnsAuthorizationsColl, project, location, id)
}

// GetOperation returns a (done) long-running operation by name. An unknown name
// is reported as a done operation: the mock completes synchronously, so any op
// id an SDK or Terraform poll asks for has already finished.
func (m *Mock) GetOperation(_ context.Context, name string) (*cmdriver.Operation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	op, ok := m.operations.Get(name)
	if !ok {
		return &cmdriver.Operation{Name: name, Done: true}, nil
	}

	out := op

	return &out, nil
}

// notFoundErr builds the NOT_FOUND error carrying the full resource name, as
// real Certificate Manager does for a Get/Patch/Delete of a missing resource.
func notFoundErr(coll, project, location, id string) error {
	return cerrors.Newf(cerrors.NotFound, "%s %q not found", coll, resourceName(coll, project, location, id))
}

// applyMask folds the masked top-level body fields from desired into the stored
// resource. A mask path's first segment names the top-level field to replace; a
// masked field absent from desired is deleted. An empty mask replaces every
// field present in desired (lenient full-body update).
func applyMask(r *cmdriver.Resource, desired map[string]json.RawMessage, mask []string) {
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
