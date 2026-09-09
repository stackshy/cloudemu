// Package servicedirectory provides an in-memory mock of the Google Cloud
// Service Directory control plane (servicedirectory.googleapis.com/v1). It
// models the three nested registration resources — namespaces, their services,
// and each service's endpoints — with synchronous REST CRUD (no long-running
// operations).
//
// Each resource's uid is a stable server-assigned UUID4, minted once at create
// and returned unchanged on every read so a Terraform refresh never drifts.
// Deleting a parent cascades to its descendants: removing a namespace removes
// its services and their endpoints; removing a service removes its endpoints,
// matching real Service Directory.
package servicedirectory

import (
	"context"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	sddriver "github.com/stackshy/cloudemu/v2/services/servicedirectory/driver"
)

var _ sddriver.ServiceDirectory = (*Mock)(nil)

const (
	namespacesColl = "namespaces"
	servicesColl   = "services"
	endpointsColl  = "endpoints"

	// maxPort is the inclusive upper bound Service Directory accepts for an
	// endpoint port; the lower bound is 0 (unset).
	maxPort = 65535

	// mask field paths a Patch honors.
	maskLabels      = "labels"
	maskAnnotations = "annotations"
	maskAddress     = "address"
	maskPort        = "port"
	maskNetwork     = "network"
)

// Mock is the in-memory Service Directory control-plane implementation. The
// three collections are separate stores, each keyed by its full GCP resource
// name, so a prefix scan cascades a parent delete to its descendants.
type Mock struct {
	mu sync.RWMutex

	namespaces *memstore.Store[sddriver.Namespace]
	services   *memstore.Store[sddriver.Service]
	endpoints  *memstore.Store[sddriver.Endpoint]

	opts *config.Options
}

// New creates a new Service Directory mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		namespaces: memstore.New[sddriver.Namespace](),
		services:   memstore.New[sddriver.Service](),
		endpoints:  memstore.New[sddriver.Endpoint](),
		opts:       opts,
	}
}

func nsName(project, location, ns string) string {
	return "projects/" + project + "/locations/" + location + "/" + namespacesColl + "/" + ns
}

func svcName(project, location, ns, svc string) string {
	return nsName(project, location, ns) + "/" + servicesColl + "/" + svc
}

func epName(project, location, ns, svc, ep string) string {
	return svcName(project, location, ns, svc) + "/" + endpointsColl + "/" + ep
}

func notFound(kind, name string) error {
	return cerrors.Newf(cerrors.NotFound, "%s %q not found", kind, name)
}

// CreateNamespace provisions a new namespace with a freshly minted uid.
func (m *Mock) CreateNamespace(_ context.Context, cfg *sddriver.NamespaceConfig) (*sddriver.Namespace, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "namespaceId is required")
	}

	if cfg.Location == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "location is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := nsName(cfg.Project, cfg.Location, cfg.ID)
	if m.namespaces.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "namespace %q already exists", key)
	}

	ns := sddriver.Namespace{
		Project: cfg.Project, Location: cfg.Location, ID: cfg.ID,
		UID:    idgen.UUID(),
		Labels: cloneStrMap(cfg.Labels),
	}
	m.namespaces.Set(key, ns)

	out := cloneNamespace(&ns)

	return &out, nil
}

// GetNamespace returns a namespace by identity, cloned.
func (m *Mock) GetNamespace(_ context.Context, project, location, id string) (*sddriver.Namespace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ns, ok := m.namespaces.Get(nsName(project, location, id))
	if !ok {
		return nil, notFound("namespace", nsName(project, location, id))
	}

	out := cloneNamespace(&ns)

	return &out, nil
}

// ListNamespaces returns every namespace in a project+location, id-ordered.
func (m *Mock) ListNamespaces(_ context.Context, project, location string) ([]sddriver.Namespace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := "projects/" + project + "/locations/" + location + "/" + namespacesColl + "/"
	all := m.namespaces.SortedValues()
	out := make([]sddriver.Namespace, 0, len(all))

	for i := range all {
		if strings.HasPrefix(nsName(all[i].Project, all[i].Location, all[i].ID), prefix) {
			out = append(out, cloneNamespace(&all[i]))
		}
	}

	return out, nil
}

// PatchNamespace applies a masked update. Only labels is mutable; an empty mask
// replaces labels from cfg (lenient full update).
func (m *Mock) PatchNamespace(_ context.Context, cfg *sddriver.NamespaceConfig, mask []string) (*sddriver.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := nsName(cfg.Project, cfg.Location, cfg.ID)

	ns, ok := m.namespaces.Get(key)
	if !ok {
		return nil, notFound("namespace", key)
	}

	if masked(mask, maskLabels) {
		ns.Labels = cloneStrMap(cfg.Labels)
	}

	m.namespaces.Set(key, ns)

	out := cloneNamespace(&ns)

	return &out, nil
}

// DeleteNamespace removes a namespace and cascades to its services and their
// endpoints.
func (m *Mock) DeleteNamespace(_ context.Context, project, location, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := nsName(project, location, id)
	if !m.namespaces.Has(key) {
		return notFound("namespace", key)
	}

	m.namespaces.Delete(key)
	deletePrefixed(m.services, key+"/"+servicesColl+"/")
	deletePrefixed(m.endpoints, key+"/"+servicesColl+"/")

	return nil
}

// CreateService provisions a new service under an existing namespace.
func (m *Mock) CreateService(_ context.Context, cfg *sddriver.ServiceConfig) (*sddriver.Service, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "serviceId is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	parent := nsName(cfg.Project, cfg.Location, cfg.Namespace)
	if !m.namespaces.Has(parent) {
		return nil, notFound("namespace", parent)
	}

	key := svcName(cfg.Project, cfg.Location, cfg.Namespace, cfg.ID)
	if m.services.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "service %q already exists", key)
	}

	svc := sddriver.Service{
		Project: cfg.Project, Location: cfg.Location, Namespace: cfg.Namespace, ID: cfg.ID,
		UID:         idgen.UUID(),
		Annotations: cloneStrMap(cfg.Annotations),
	}
	m.services.Set(key, svc)

	out := cloneService(&svc)

	return &out, nil
}

// GetService returns a service by identity, cloned.
func (m *Mock) GetService(_ context.Context, project, location, namespace, id string) (*sddriver.Service, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := svcName(project, location, namespace, id)

	svc, ok := m.services.Get(key)
	if !ok {
		return nil, notFound("service", key)
	}

	out := cloneService(&svc)

	return &out, nil
}

// ListServices returns every service under a namespace, id-ordered.
func (m *Mock) ListServices(_ context.Context, project, location, namespace string) ([]sddriver.Service, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := nsName(project, location, namespace) + "/" + servicesColl + "/"
	all := m.services.SortedValues()
	out := make([]sddriver.Service, 0, len(all))

	for i := range all {
		key := svcName(all[i].Project, all[i].Location, all[i].Namespace, all[i].ID)
		if strings.HasPrefix(key, prefix) {
			out = append(out, cloneService(&all[i]))
		}
	}

	return out, nil
}

// PatchService applies a masked update. Only annotations is mutable.
func (m *Mock) PatchService(_ context.Context, cfg *sddriver.ServiceConfig, mask []string) (*sddriver.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := svcName(cfg.Project, cfg.Location, cfg.Namespace, cfg.ID)

	svc, ok := m.services.Get(key)
	if !ok {
		return nil, notFound("service", key)
	}

	if masked(mask, maskAnnotations) {
		svc.Annotations = cloneStrMap(cfg.Annotations)
	}

	m.services.Set(key, svc)

	out := cloneService(&svc)

	return &out, nil
}

// DeleteService removes a service and cascades to its endpoints.
func (m *Mock) DeleteService(_ context.Context, project, location, namespace, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := svcName(project, location, namespace, id)
	if !m.services.Has(key) {
		return notFound("service", key)
	}

	m.services.Delete(key)
	deletePrefixed(m.endpoints, key+"/"+endpointsColl+"/")

	return nil
}

// CreateEndpoint provisions a new endpoint under an existing service.
func (m *Mock) CreateEndpoint(_ context.Context, cfg *sddriver.EndpointConfig) (*sddriver.Endpoint, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "endpointId is required")
	}

	if cfg.Port < 0 || cfg.Port > maxPort {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "port %d is out of range [0, %d]", cfg.Port, maxPort)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	parent := svcName(cfg.Project, cfg.Location, cfg.Namespace, cfg.Service)
	if !m.services.Has(parent) {
		return nil, notFound("service", parent)
	}

	key := epName(cfg.Project, cfg.Location, cfg.Namespace, cfg.Service, cfg.ID)
	if m.endpoints.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "endpoint %q already exists", key)
	}

	ep := endpointFromConfig(cfg)
	m.endpoints.Set(key, ep)

	out := cloneEndpoint(&ep)

	return &out, nil
}

// GetEndpoint returns an endpoint by identity, cloned.
func (m *Mock) GetEndpoint(_ context.Context, project, location, namespace, service, id string) (*sddriver.Endpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := epName(project, location, namespace, service, id)

	ep, ok := m.endpoints.Get(key)
	if !ok {
		return nil, notFound("endpoint", key)
	}

	out := cloneEndpoint(&ep)

	return &out, nil
}

// ListEndpoints returns every endpoint under a service, id-ordered.
func (m *Mock) ListEndpoints(_ context.Context, project, location, namespace, service string) ([]sddriver.Endpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := svcName(project, location, namespace, service) + "/" + endpointsColl + "/"
	all := m.endpoints.SortedValues()
	out := make([]sddriver.Endpoint, 0, len(all))

	for i := range all {
		key := epName(all[i].Project, all[i].Location, all[i].Namespace, all[i].Service, all[i].ID)
		if strings.HasPrefix(key, prefix) {
			out = append(out, cloneEndpoint(&all[i]))
		}
	}

	return out, nil
}

// PatchEndpoint applies a masked update over the mutable fields (address, port,
// annotations, network). An empty mask replaces all of them from cfg.
func (m *Mock) PatchEndpoint(_ context.Context, cfg *sddriver.EndpointConfig, mask []string) (*sddriver.Endpoint, error) {
	if masked(mask, maskPort) && (cfg.Port < 0 || cfg.Port > maxPort) {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "port %d is out of range [0, %d]", cfg.Port, maxPort)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := epName(cfg.Project, cfg.Location, cfg.Namespace, cfg.Service, cfg.ID)

	ep, ok := m.endpoints.Get(key)
	if !ok {
		return nil, notFound("endpoint", key)
	}

	applyEndpointMask(&ep, cfg, mask)
	m.endpoints.Set(key, ep)

	out := cloneEndpoint(&ep)

	return &out, nil
}

// DeleteEndpoint removes a single endpoint.
func (m *Mock) DeleteEndpoint(_ context.Context, project, location, namespace, service, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := epName(project, location, namespace, service, id)
	if !m.endpoints.Has(key) {
		return notFound("endpoint", key)
	}

	m.endpoints.Delete(key)

	return nil
}

// endpointFromConfig builds a stored endpoint from a create config, minting its
// stable uid.
func endpointFromConfig(cfg *sddriver.EndpointConfig) sddriver.Endpoint {
	return sddriver.Endpoint{
		Project: cfg.Project, Location: cfg.Location, Namespace: cfg.Namespace,
		Service: cfg.Service, ID: cfg.ID,
		UID:         idgen.UUID(),
		Address:     cfg.Address,
		Port:        cfg.Port,
		Annotations: cloneStrMap(cfg.Annotations),
		Network:     cfg.Network,
	}
}

// applyEndpointMask folds the masked mutable fields from cfg into ep. An empty
// mask replaces every mutable field (lenient full update).
func applyEndpointMask(ep *sddriver.Endpoint, cfg *sddriver.EndpointConfig, mask []string) {
	full := len(mask) == 0

	if full || masked(mask, maskAddress) {
		ep.Address = cfg.Address
	}

	if full || masked(mask, maskPort) {
		ep.Port = cfg.Port
	}

	if full || masked(mask, maskAnnotations) {
		ep.Annotations = cloneStrMap(cfg.Annotations)
	}

	if full || masked(mask, maskNetwork) {
		ep.Network = cfg.Network
	}
}

// masked reports whether field is targeted by the updateMask: an empty mask is
// a lenient full update (every field), otherwise the field must be listed (its
// leading path segment matching).
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
func deletePrefixed[V any](s *memstore.Store[V], prefix string) {
	for _, k := range s.Keys() {
		if strings.HasPrefix(k, prefix) {
			s.Delete(k)
		}
	}
}
