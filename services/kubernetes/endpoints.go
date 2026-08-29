package kubernetes

import (
	"net/http"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

// serveEndpoints dispatches /api/v1/{namespaces/{ns}/endpoints|endpoints}
// requests.
//
// Endpoints are read-only from the SDK consumer's perspective in Wave 2 —
// real apiserver lets you Create/Update them too, but the in-memory store
// auto-creates one per Service and tears it down on Service delete. We
// expose only Get / List / Watch so client-go Reflectors work.
func (s *ClusterState) serveEndpoints(w http.ResponseWriter, r *http.Request, route *Route) {
	if route.APIGroup != "" || route.APIVersion != apiVersionV1 {
		writeNotFound(w, "k8s api: endpoints are only served at /api/v1")

		return
	}

	if route.Namespace == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, "k8s api: endpoints cluster-wide: method not allowed: "+r.Method)

			return
		}

		if r.URL.Query().Get("watch") == watchQueryValue {
			s.watchEndpoints(w, r, "")

			return
		}

		s.listEndpointsAllNamespaces(w, r)

		return
	}

	if !s.namespaceExists(route.Namespace) {
		writeNotFound(w, "k8s api: namespace not found: "+route.Namespace)

		return
	}

	if route.Name == "" {
		s.serveEndpointsCollection(w, r, route.Namespace)

		return
	}

	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, "k8s api: endpoints are read-only in Wave 2: "+r.Method)

		return
	}

	s.getEndpoints(w, r, route.Namespace, route.Name)
}

func (s *ClusterState) serveEndpointsCollection(w http.ResponseWriter, r *http.Request, namespace string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, "k8s api: endpoints collection is read-only in Wave 2: "+r.Method)

		return
	}

	if r.URL.Query().Get("watch") == watchQueryValue {
		s.watchEndpoints(w, r, namespace)

		return
	}

	s.listEndpoints(w, r, namespace)
}

func (s *ClusterState) listEndpoints(w http.ResponseWriter, r *http.Request, namespace string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items, cont, ok := listPage(s.collectEndpointsLocked(namespace), w, r)
	if !ok {
		return
	}

	s.writeList(w, r, &corev1.EndpointsList{
		TypeMeta: metav1.TypeMeta{Kind: "EndpointsList", APIVersion: "v1"},
		ListMeta: metav1.ListMeta{Continue: cont},
		Items:    items,
	})
}

func (s *ClusterState) listEndpointsAllNamespaces(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items, cont, ok := listPage(s.collectEndpointsLocked(""), w, r)
	if !ok {
		return
	}

	s.writeList(w, r, &corev1.EndpointsList{
		TypeMeta: metav1.TypeMeta{Kind: "EndpointsList", APIVersion: "v1"},
		ListMeta: metav1.ListMeta{Continue: cont},
		Items:    items,
	})
}

func (s *ClusterState) collectEndpointsLocked(namespace string) []corev1.Endpoints {
	keys := make([]string, 0, len(s.endpoints))

	for k := range s.endpoints {
		if namespace == "" || strings.HasPrefix(k, namespace+"/") {
			keys = append(keys, k)
		}
	}

	sort.Strings(keys)

	items := make([]corev1.Endpoints, 0, len(keys))
	for _, k := range keys {
		items = append(items, *s.endpoints[k].DeepCopy())
	}

	return items
}

func (s *ClusterState) getEndpoints(w http.ResponseWriter, r *http.Request, namespace, name string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ep, ok := s.endpoints[endpointsKey(namespace, name)]
	if !ok {
		writeNotFound(w, "k8s api: endpoints not found: "+namespace+"/"+name)

		return
	}

	s.writeObject(w, r, ep.DeepCopy())
}

func (s *ClusterState) watchEndpoints(w http.ResponseWriter, r *http.Request, namespace string) {
	sel, fields := parseListSelectors(r)
	serveWatch(s, w, r, s.wEndpoints, namespace, "v1", "Endpoints",
		func() []corev1.Endpoints { return s.collectEndpointsLocked(namespace) },
		func(ep corev1.Endpoints) bool {
			return sel.Matches(labels.Set(ep.Labels)) && metaFieldsMatch(ep.Name, ep.Namespace, fields)
		})
}

func endpointsKey(namespace, name string) string {
	return namespace + "/" + name
}

// newEndpointsObject builds the Endpoints stub auto-created for a Service.
// Subsets is left empty — there's no scheduler / Pod-IP allocation in Wave 2.
// Real apiserver lets the endpoints controller fill Subsets in once Pods
// match the Service selector and become Ready.
func (s *ClusterState) newEndpointsObject(namespace, name string) *corev1.Endpoints {
	return &corev1.Endpoints{
		TypeMeta: metav1.TypeMeta{Kind: "Endpoints", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			UID:               types.UID(newUID()),
			CreationTimestamp: s.now(),
			ResourceVersion:   s.nextClusterRVLocked(),
		},
	}
}
