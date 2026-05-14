package kubernetes

import (
	"net/http"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

// ClusterState is the in-memory backing store for one Kubernetes cluster's
// data plane. Every cluster registered with an APIServer gets its own
// ClusterState — two EKS clusters in the same test never see each other's
// resources.
//
// Resources are kept in plain Go maps under a single RWMutex. The K8s API
// is read-heavy in the typical test scenario (Reflector list + watch), and
// the surface is small enough that finer-grained locking would just add
// complexity without measurable gain.
type ClusterState struct {
	mu sync.RWMutex

	// namespaces is cluster-scoped — keyed by namespace name.
	namespaces map[string]*corev1.Namespace

	// configMaps is namespaced — keyed by "<namespace>/<name>".
	configMaps map[string]*corev1.ConfigMap

	// pods is namespaced — keyed by "<namespace>/<name>".
	pods map[string]*corev1.Pod

	// secrets is namespaced — keyed by "<namespace>/<name>".
	secrets map[string]*corev1.Secret

	// serviceAccounts is namespaced — keyed by "<namespace>/<name>".
	serviceAccounts map[string]*corev1.ServiceAccount

	// services is namespaced — keyed by "<namespace>/<name>".
	services map[string]*corev1.Service

	// deployments lives under apps/v1 — keyed by "<namespace>/<name>".
	deployments map[string]*appsv1.Deployment

	// nextClusterIP is the monotonic counter used to hand out Service
	// ClusterIPs from 10.96.0.0/12. Incremented under mu.Lock by
	// allocateClusterIP. Real apiserver uses an in-memory bitmap; the
	// monotonic counter is enough for tests.
	nextClusterIP uint32
}

// firstClusterIPOffset is the first integer offset above 10.96.0.0 that the
// service ClusterIP allocator hands out (so the first allocated IP is
// 10.96.0.1). 10.96.0.0/12 is the kubeadm default service CIDR — we keep
// the same convention so allocations look familiar in tests.
const firstClusterIPOffset uint32 = 1

// newClusterState returns an empty state with the implicit "default" and
// "kube-system" namespaces already present, matching the bootstrap state of
// a fresh real cluster.
func newClusterState() *ClusterState {
	s := &ClusterState{
		namespaces:      make(map[string]*corev1.Namespace),
		configMaps:      make(map[string]*corev1.ConfigMap),
		pods:            make(map[string]*corev1.Pod),
		secrets:         make(map[string]*corev1.Secret),
		serviceAccounts: make(map[string]*corev1.ServiceAccount),
		services:        make(map[string]*corev1.Service),
		deployments:     make(map[string]*appsv1.Deployment),
		nextClusterIP:   firstClusterIPOffset,
	}

	for _, name := range []string{"default", "kube-system", "kube-public"} {
		s.namespaces[name] = newNamespaceObject(name)
		// Real apiserver auto-creates a "default" ServiceAccount in every
		// namespace. We do the same so `kubectl get sa default` works in
		// the bootstrap namespaces.
		sa := newServiceAccountObject(name, "default")
		s.serviceAccounts[serviceAccountKey(name, "default")] = sa
	}

	return s
}

// ServeHTTP dispatches a Kubernetes REST request into the per-resource
// handlers. The request's URL has already been stripped of the /k8s/<uid>
// prefix by APIServer.ServeHTTP, so r.URL.Path here starts with /api/v1/...
// or /apis/<group>/<version>/...
func (s *ClusterState) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route := parseRoute(r.URL.Path)
	if route == nil {
		writeNotFound(w, "k8s api: unrecognized path "+r.URL.Path)

		return
	}

	switch route.Resource {
	case "namespaces":
		s.serveNamespaces(w, r, route)
	case "configmaps":
		s.serveConfigMaps(w, r, route)
	case "pods":
		s.servePods(w, r, route)
	case "secrets":
		s.serveSecrets(w, r, route)
	case "serviceaccounts":
		s.serveServiceAccounts(w, r, route)
	case "services":
		s.serveServices(w, r, route)
	case "deployments":
		s.serveDeployments(w, r, route)
	default:
		writeNotFound(w, "k8s api: resource not implemented: "+route.Resource)
	}
}
