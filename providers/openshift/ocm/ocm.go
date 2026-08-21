// Package ocm emulates the Red Hat OpenShift Cluster Manager (OCM) cluster
// management surface that the `rosa` CLI drives — api.openshift.com's
// /api/clusters_mgmt/v1/clusters. Each created cluster registers an
// OpenShift-flavored ClusterState with a shared kubernetes.APIServer (when
// wired), so the api.url OCM reports points at a real in-memory OpenShift data
// plane that `oc` operates against.
//
// ROSA on a real cluster is a heavy, asynchronous provision (AWS roles, OIDC,
// installer); the emulator converges instantly — a created cluster is `ready`
// immediately — because its value is exercising the OCM API contract and the
// resulting data plane, not simulating install time.
package ocm

import (
	"context"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// defaultOCPVersion is the emulated OpenShift release (kube 1.29), matching the
// data plane's identity singletons.
const defaultOCPVersion = "4.16.0"

// Cluster is a stored OCM/ROSA cluster.
type Cluster struct {
	ID            string
	Name          string
	State         string
	CloudProvider string
	Region        string
	Version       string
	APIURL        string
	ConsoleURL    string
	Product       string
	CreatedAt     time.Time
}

// ClusterInput is the create payload decoded from the OCM cluster body.
type ClusterInput struct {
	Name          string
	CloudProvider string
	Region        string
	Version       string
	Product       string
}

// Mock is the OCM cluster-management emulator.
type Mock struct {
	mu sync.RWMutex

	clusters *memstore.Store[Cluster]

	opts *config.Options

	k8sAPI  *kubernetes.APIServer
	k8sUIDs map[string]string // cluster ID -> data-plane UID
}

// New creates an OCM mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters: memstore.New[Cluster](),
		opts:     opts,
		k8sUIDs:  make(map[string]string),
	}
}

// SetK8sAPI wires the shared Kubernetes data-plane server.
func (m *Mock) SetK8sAPI(api *kubernetes.APIServer) {
	m.mu.Lock()
	m.k8sAPI = api
	m.mu.Unlock()
}

func (m *Mock) now() time.Time {
	if m.opts != nil && m.opts.Clock != nil {
		return m.opts.Clock.Now().UTC()
	}

	return time.Now().UTC()
}

// CreateCluster provisions an OCM cluster. It registers an OpenShift-flavored
// data plane and reports the cluster `ready` with an api.url pointing at it.
//
//nolint:gocritic // hugeParam: input mirrors the OCM request body; value is intentional.
func (m *Mock) CreateCluster(_ context.Context, input ClusterInput) (*Cluster, error) {
	if input.Name == "" {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "ocm: cluster name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	id := idgen.GenerateID("")

	version := input.Version
	if version == "" {
		version = defaultOCPVersion
	}

	product := input.Product
	if product == "" {
		product = "rosa"
	}

	cloud := input.CloudProvider
	if cloud == "" {
		cloud = "aws"
	}

	apiURL := "https://api." + input.Name + ".cloudemu.openshiftapps.com:6443"

	if m.k8sAPI != nil {
		uid, _ := m.k8sAPI.RegisterClusterWithFlavor(kubernetes.FlavorOpenShift)
		m.k8sUIDs[id] = uid

		if base := m.k8sAPI.BaseURL(); base != "" {
			apiURL = base + "/k8s/" + uid
		}
	}

	cluster := Cluster{
		ID:            id,
		Name:          input.Name,
		State:         "ready",
		CloudProvider: cloud,
		Region:        input.Region,
		Version:       version,
		APIURL:        apiURL,
		ConsoleURL:    "https://console-openshift-console.apps." + input.Name + ".cloudemu.openshiftapps.com",
		Product:       product,
		CreatedAt:     m.now(),
	}

	m.clusters.Set(id, cluster)

	out := cluster

	return &out, nil
}

// GetCluster returns a cluster by ID.
func (m *Mock) GetCluster(_ context.Context, id string) (*Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cluster, ok := m.clusters.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "ocm: cluster not found: %s", id)
	}

	out := cluster

	return &out, nil
}

// ListClusters returns all clusters sorted by ID.
func (m *Mock) ListClusters(_ context.Context) []Cluster {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.clusters.SortedValues()
}

// DeleteCluster tears down a cluster and its data plane. The cluster transitions
// to `uninstalling` and is removed; OCM returns 204.
func (m *Mock) DeleteCluster(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.clusters.Has(id) {
		return cerrors.Newf(cerrors.NotFound, "ocm: cluster not found: %s", id)
	}

	if uid, ok := m.k8sUIDs[id]; ok && m.k8sAPI != nil {
		m.k8sAPI.DeregisterCluster(uid)
		delete(m.k8sUIDs, id)
	}

	m.clusters.Delete(id)

	return nil
}

// Kubeconfig returns the admin kubeconfig for a cluster (the OCM
// clusters/{id}/credentials response), pointing at the OpenShift data plane when
// wired.
func (m *Mock) Kubeconfig(id string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cluster, ok := m.clusters.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "ocm: cluster not found: %s", id)
	}

	if m.k8sAPI != nil {
		if uid, wired := m.k8sUIDs[id]; wired {
			if base := m.k8sAPI.BaseURL(); base != "" {
				return kubernetes.RenderKubeconfig(base, uid, cluster.Name), nil
			}
		}
	}

	return nil, cerrors.Newf(cerrors.FailedPrecondition, "ocm: no data plane wired for cluster %s", id)
}
