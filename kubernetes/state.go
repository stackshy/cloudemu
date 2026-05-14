package kubernetes

import (
	"net/http"
	"sync"

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
}

// newClusterState returns an empty state with the implicit "default" and
// "kube-system" namespaces already present, matching the bootstrap state of
// a fresh real cluster.
func newClusterState() *ClusterState {
	s := &ClusterState{
		namespaces: make(map[string]*corev1.Namespace),
		configMaps: make(map[string]*corev1.ConfigMap),
		pods:       make(map[string]*corev1.Pod),
		secrets:    make(map[string]*corev1.Secret),
	}

	for _, name := range []string{"default", "kube-system", "kube-public"} {
		s.namespaces[name] = newNamespaceObject(name)
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
	default:
		writeNotFound(w, "k8s api: resource not implemented: "+route.Resource)
	}
}
