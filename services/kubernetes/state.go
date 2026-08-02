package kubernetes

import (
	"net/http"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stackshy/cloudemu/v2/config"
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

	// clock sources every timestamp the data plane stamps (creationTimestamp,
	// pod start/condition times). A FakeClock makes all of them deterministic;
	// defaults to config.RealClock.
	clock config.Clock

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
	// pdbs lives under policy/v1 — keyed by "<namespace>/<name>".
	pdbs map[string]*policyv1.PodDisruptionBudget

	// endpoints — keyed by "<namespace>/<name>". Real apiserver populates
	// Subsets[].Addresses from Pods that match the Service selector via the
	// endpoints controller. Wave 2 doesn't ship a controller, so endpoints
	// objects are auto-created (empty) when their backing Service is created
	// and torn down when it's deleted.
	endpoints map[string]*corev1.Endpoints

	// nextClusterIP is the monotonic counter used to hand out Service
	// ClusterIPs from 10.96.0.0/12. Incremented under mu.Lock by
	// allocateClusterIP. Real apiserver uses an in-memory bitmap; the
	// monotonic counter is enough for tests.
	nextClusterIP uint32

	// nextPodIP is the monotonic counter for synthetic Pod IPs out of
	// 10.244.0.0/16 (the kubeadm/Flannel default pod CIDR), handed out by the
	// reconciler when it brings a controller's Pods up Running.
	nextPodIP uint32

	// reg is the generic resource registry backing the kinds that don't have a
	// hand-written handler (ReplicaSet, StatefulSet, DaemonSet, …).
	reg *registry

	// Per-resource Watch broadcasters. Handlers publish on Create/Update/
	// Patch/Delete; ?watch=true requests subscribe via streamWatch.
	wNamespaces      *broadcaster
	wConfigMaps      *broadcaster
	wPods            *broadcaster
	wSecrets         *broadcaster
	wServiceAccounts *broadcaster
	wServices        *broadcaster
	wDeployments     *broadcaster
	wEndpoints       *broadcaster
}

// firstClusterIPOffset is the first integer offset above 10.96.0.0 that the
// service ClusterIP allocator hands out (so the first allocated IP is
// 10.96.0.1). 10.96.0.0/12 is the kubeadm default service CIDR — we keep
// the same convention so allocations look familiar in tests.
const firstClusterIPOffset uint32 = 1

// newClusterState returns state pre-populated with the three implicit
// namespaces (default, kube-system, kube-public) and a "default"
// ServiceAccount in each, matching the bootstrap state of a fresh real
// cluster.
func newClusterState(clock config.Clock) *ClusterState {
	if clock == nil {
		clock = config.RealClock{}
	}

	s := &ClusterState{
		clock:            clock,
		namespaces:       make(map[string]*corev1.Namespace),
		configMaps:       make(map[string]*corev1.ConfigMap),
		pods:             make(map[string]*corev1.Pod),
		secrets:          make(map[string]*corev1.Secret),
		serviceAccounts:  make(map[string]*corev1.ServiceAccount),
		services:         make(map[string]*corev1.Service),
		deployments:      make(map[string]*appsv1.Deployment),
		pdbs:             make(map[string]*policyv1.PodDisruptionBudget),
		endpoints:        make(map[string]*corev1.Endpoints),
		nextClusterIP:    firstClusterIPOffset,
		nextPodIP:        1,
		reg:              newRegistry(registeredResources()),
		wNamespaces:      newBroadcaster(),
		wConfigMaps:      newBroadcaster(),
		wPods:            newBroadcaster(),
		wSecrets:         newBroadcaster(),
		wServiceAccounts: newBroadcaster(),
		wServices:        newBroadcaster(),
		wDeployments:     newBroadcaster(),
		wEndpoints:       newBroadcaster(),
	}

	for _, name := range []string{"default", "kube-system", "kube-public"} {
		s.namespaces[name] = s.newNamespaceObject(name)
		// Real apiserver auto-creates a "default" ServiceAccount in every
		// namespace. We do the same so `kubectl get sa default` works in
		// the bootstrap namespaces.
		sa := s.newServiceAccountObject(name, "default")
		s.serviceAccounts[serviceAccountKey(name, "default")] = sa
	}

	// Bootstrap the single synthetic Node every reconciler-materialized Pod is
	// scheduled onto (spec.nodeName=cloudemu-node-0). Without it `kubectl get
	// nodes` is empty on a fresh cluster and Pods/DaemonSets reference a Node
	// object that doesn't exist.
	if store := s.reg.getStore("", "v1", "nodes"); store != nil {
		node := newNodeObject()
		store.items[objKey("", node.GetName())] = node
	}

	return s
}

// now returns the current time from the cluster's clock as a metav1.Time. Every
// data-plane timestamp flows through here so a FakeClock renders them all
// deterministic for tests.
func (s *ClusterState) now() metav1.Time {
	return metav1.NewTime(s.clock.Now())
}

// ServeHTTP dispatches a Kubernetes REST request into the per-resource
// handlers. The request's URL has already been stripped of the /k8s/<uid>
// prefix by APIServer.ServeHTTP, so r.URL.Path here starts with /api/v1/...
// or /apis/<group>/<version>/...
func (s *ClusterState) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Discovery first: /api, /apis and the group-version lists are not
	// resource paths and parseRoute cannot represent them.
	if s.serveDiscovery(w, r) {
		return
	}

	route := parseRoute(r.URL.Path)
	if route == nil {
		writeNotFound(w, "k8s api: unrecognized path "+r.URL.Path)

		return
	}

	// Subresources (/status, /scale, …) are handled separately so the object
	// handlers below never mis-parse them as a write against the parent.
	// Registry-backed kinds serve their own subresources; typed kinds go
	// through serveSubresource.
	if route.Subresource != "" {
		if st := s.reg.lookup(route); st != nil {
			s.serveRegistry(w, r, route, st)

			return
		}

		s.serveSubresource(w, r, route)

		return
	}

	s.dispatchResource(w, r, route)
}

// dispatchResource routes an object (non-subresource) request to the typed
// handler for its resource, falling back to the registry for registry-backed
// kinds.
//
//nolint:gocyclo // flat per-resource dispatch switch; one arm per typed kind.
func (s *ClusterState) dispatchResource(w http.ResponseWriter, r *http.Request, route *Route) {
	switch route.Resource {
	case "namespaces":
		s.serveNamespaces(w, r, route)
	case "configmaps":
		s.serveConfigMaps(w, r, route)
	case resourcePods:
		s.servePods(w, r, route)
	case "secrets":
		s.serveSecrets(w, r, route)
	case "serviceaccounts":
		s.serveServiceAccounts(w, r, route)
	case "services":
		s.serveServices(w, r, route)
	case resourceDeployments:
		s.serveDeployments(w, r, route)
	case "poddisruptionbudgets":
		s.servePDBs(w, r, route)
	case "endpoints":
		s.serveEndpoints(w, r, route)
	default:
		if st := s.reg.lookup(route); st != nil {
			s.serveRegistry(w, r, route, st)

			return
		}

		writeNotFound(w, "k8s api: resource not implemented: "+route.Resource)
	}
}
