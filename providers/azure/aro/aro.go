// Package aro emulates Azure Red Hat OpenShift (ARO) —
// Microsoft.RedHatOpenShift/openShiftClusters. It mirrors the AKS mock's shape:
// each cluster registers an OpenShift-flavored ClusterState with a shared
// kubernetes.APIServer (when wired), so the kubeconfig it hands back points at a
// real in-memory OpenShift data plane that `oc` and client-go drive end-to-end.
// Without a wired data plane, the kubeconfig points at a NOT-IMPLEMENTED
// sentinel, matching the AKS Wave-1 fallback.
package aro

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/k8spki"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

const (
	// ARMProvider / ResourceType are the ARM namespace and resource the server
	// handler matches to route requests here.
	ARMProvider  = "Microsoft.RedHatOpenShift"
	ResourceType = "openShiftClusters"

	// defaultOCPVersion is the emulated OpenShift release (kube 1.29), matching
	// the data plane's identity singletons.
	defaultOCPVersion = "4.16.0"

	// aroAppsDomain is the synthetic ingress domain ARO console/API URLs hang off.
	aroAppsDomain = "aroapp.io"
)

// OpenShiftCluster is a stored ARO cluster.
type OpenShiftCluster struct {
	ID                string
	Name              string
	ResourceGroup     string
	Subscription      string
	Location          string
	ProvisioningState string
	Version           string
	ConsoleURL        string
	APIServerURL      string
	Tags              map[string]string
	CreatedAt         time.Time
}

// ClusterInput is the create/update payload the server handler decodes from ARM.
type ClusterInput struct {
	Subscription  string
	ResourceGroup string
	Name          string
	Location      string
	Version       string
	Tags          map[string]string
}

// Mock is the ARO control-plane emulator.
type Mock struct {
	mu sync.RWMutex

	// clusters key = "{subscription}/{rg}/{name}".
	clusters *memstore.Store[OpenShiftCluster]

	opts *config.Options

	// k8sAPI is the shared in-memory Kubernetes data-plane server. When set,
	// CreateOrUpdateCluster registers an OpenShift-flavored ClusterState and
	// Kubeconfig points at it; when nil, the sentinel fallback is used.
	k8sAPI *kubernetes.APIServer
	// k8sUIDs maps the cluster key → the data-plane UID registered for it.
	k8sUIDs map[string]string
}

// New creates an ARO mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		clusters: memstore.New[OpenShiftCluster](),
		opts:     opts,
		k8sUIDs:  make(map[string]string),
	}
}

// SetK8sAPI wires the shared Kubernetes data-plane server. Clusters created
// after this call back their kubeconfig with a real OpenShift data plane.
func (m *Mock) SetK8sAPI(api *kubernetes.APIServer) {
	m.mu.Lock()
	m.k8sAPI = api
	m.mu.Unlock()
}

func clusterKey(subscription, rg, name string) string {
	return subscription + "/" + rg + "/" + name
}

// ClusterResourceID returns the ARM resource ID for an ARO cluster.
func ClusterResourceID(subscription, rg, name string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/%s/%s/%s",
		subscription, rg, ARMProvider, ResourceType, name)
}

func copyTags(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}

	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}

	return out
}

func (m *Mock) now() time.Time {
	if m.opts != nil && m.opts.Clock != nil {
		return m.opts.Clock.Now().UTC()
	}

	return time.Now().UTC()
}

// CreateOrUpdateCluster provisions (or updates) an ARO cluster. On first
// creation it registers an OpenShift-flavored data-plane ClusterState so the
// cluster's kubeconfig is immediately usable; the cluster comes up Succeeded.
//
//nolint:gocritic // hugeParam: input mirrors the ARM request body; value is intentional.
func (m *Mock) CreateOrUpdateCluster(_ context.Context, input ClusterInput) (*OpenShiftCluster, error) {
	if input.Name == "" {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "aro: cluster name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(input.Subscription, input.ResourceGroup, input.Name)

	version := input.Version
	if version == "" {
		version = defaultOCPVersion
	}

	// Register a data plane on first sighting only (CreateOrUpdate is idempotent
	// on the same name).
	if m.k8sAPI != nil {
		if _, ok := m.k8sUIDs[key]; !ok {
			uid, _ := m.k8sAPI.RegisterClusterWithFlavor(kubernetes.FlavorOpenShift)
			m.k8sUIDs[key] = uid
		}
	}

	existing, found := m.clusters.Get(key)
	created := m.now()

	if found {
		created = existing.CreatedAt
	}

	cluster := OpenShiftCluster{
		ID:                ClusterResourceID(input.Subscription, input.ResourceGroup, input.Name),
		Name:              input.Name,
		ResourceGroup:     input.ResourceGroup,
		Subscription:      input.Subscription,
		Location:          input.Location,
		ProvisioningState: "Succeeded",
		Version:           version,
		ConsoleURL:        fmt.Sprintf("https://console-openshift-console.apps.%s.%s/", input.Name, aroAppsDomain),
		APIServerURL:      fmt.Sprintf("https://api.%s.%s:6443/", input.Name, aroAppsDomain),
		Tags:              copyTags(input.Tags),
		CreatedAt:         created,
	}

	m.clusters.Set(key, cluster)

	out := cluster

	return &out, nil
}

// GetCluster returns a cluster by resource group and name.
func (m *Mock) GetCluster(_ context.Context, subscription, rg, name string) (*OpenShiftCluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cluster, ok := m.clusters.Get(clusterKey(subscription, rg, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "aro: cluster not found: %s", name)
	}

	out := cluster

	return &out, nil
}

// ListClustersByResourceGroup lists clusters in a resource group.
func (m *Mock) ListClustersByResourceGroup(_ context.Context, subscription, rg string) []OpenShiftCluster {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []OpenShiftCluster

	for _, c := range m.clusters.SortedValues() { //nolint:gocritic // rangeValCopy: read-only filter, copy is fine.
		if c.Subscription == subscription && c.ResourceGroup == rg {
			out = append(out, c)
		}
	}

	return out
}

// ListClusters lists all clusters in a subscription.
func (m *Mock) ListClusters(_ context.Context, subscription string) []OpenShiftCluster {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []OpenShiftCluster

	for _, c := range m.clusters.SortedValues() { //nolint:gocritic // rangeValCopy: read-only filter, copy is fine.
		if c.Subscription == subscription {
			out = append(out, c)
		}
	}

	return out
}

// DeleteCluster removes a cluster and deregisters its data plane.
func (m *Mock) DeleteCluster(_ context.Context, subscription, rg, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := clusterKey(subscription, rg, name)

	if !m.clusters.Has(key) {
		return cerrors.Newf(cerrors.NotFound, "aro: cluster not found: %s", name)
	}

	if uid, ok := m.k8sUIDs[key]; ok && m.k8sAPI != nil {
		m.k8sAPI.DeregisterCluster(uid)
		delete(m.k8sUIDs, key)
	}

	m.clusters.Delete(key)

	return nil
}

// Kubeconfig returns the admin kubeconfig for a cluster (the ARM
// listAdminCredentials response). When a data plane is wired it points at the
// per-cluster OpenShift API under /k8s/<uid>; otherwise a NOT-IMPLEMENTED
// sentinel, mirroring AKS.
func (m *Mock) Kubeconfig(subscription, rg, name string) []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.k8sAPI != nil {
		if uid, ok := m.k8sUIDs[clusterKey(subscription, rg, name)]; ok {
			if base := m.k8sAPI.BaseURL(); base != "" {
				return kubernetes.RenderKubeconfig(base, uid, name)
			}
		}
	}

	return fmt.Appendf(nil, `apiVersion: v1
kind: Config
clusters:
- name: %s
  cluster:
    server: https://ARO-DATAPLANE-NOT-IMPLEMENTED.cloudemu.local
    certificate-authority-data: %s
contexts:
- name: %s
  context:
    cluster: %s
    user: %s
current-context: %s
`, name, k8spki.CertificatePEM(), name, name, name, name)
}
