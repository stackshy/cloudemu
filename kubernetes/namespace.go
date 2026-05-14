package kubernetes

import (
	"net/http"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// serveNamespaces dispatches /api/v1/namespaces[/name] requests. Namespaces
// are cluster-scoped — route.Namespace is always empty here.
func (s *ClusterState) serveNamespaces(w http.ResponseWriter, r *http.Request, route *Route) {
	if route.APIGroup != "" || route.APIVersion != apiVersionV1 {
		writeNotFound(w, "k8s api: namespaces are only served at /api/v1")

		return
	}

	if route.Name == "" {
		s.serveNamespaceCollection(w, r)

		return
	}

	s.serveNamespaceItem(w, r, route.Name)
}

func (s *ClusterState) serveNamespaceCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listNamespaces(w)
	case http.MethodPost:
		s.createNamespace(w, r)
	default:
		writeMethodNotAllowed(w, "k8s api: namespaces collection: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) serveNamespaceItem(w http.ResponseWriter, r *http.Request, name string) {
	switch r.Method {
	case http.MethodGet:
		s.getNamespace(w, name)
	case http.MethodPut:
		s.updateNamespace(w, r, name)
	case http.MethodPatch:
		s.patchNamespace(w, r, name)
	case http.MethodDelete:
		s.deleteNamespace(w, name)
	default:
		writeMethodNotAllowed(w, "k8s api: namespace item: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) createNamespace(w http.ResponseWriter, r *http.Request) {
	var in corev1.Namespace
	if !readJSON(w, r, &in) {
		return
	}

	if in.Name == "" {
		writeBadRequest(w, "k8s api: namespace: metadata.name is required")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.namespaces[in.Name]; ok {
		writeAlreadyExists(w, "k8s api: namespace already exists: "+in.Name)

		return
	}

	ns := newNamespaceObject(in.Name)
	ns.Labels = in.Labels
	ns.Annotations = in.Annotations
	s.namespaces[in.Name] = ns

	writeJSON(w, http.StatusCreated, ns)
}

func (s *ClusterState) listNamespaces(w http.ResponseWriter) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.namespaces))
	for n := range s.namespaces {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]corev1.Namespace, 0, len(names))
	for _, n := range names {
		items = append(items, *s.namespaces[n])
	}

	writeJSON(w, http.StatusOK, &corev1.NamespaceList{
		TypeMeta: metav1.TypeMeta{Kind: "NamespaceList", APIVersion: "v1"},
		Items:    items,
	})
}

func (s *ClusterState) getNamespace(w http.ResponseWriter, name string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ns, ok := s.namespaces[name]
	if !ok {
		writeNotFound(w, "k8s api: namespace not found: "+name)

		return
	}

	writeJSON(w, http.StatusOK, ns)
}

func (s *ClusterState) updateNamespace(w http.ResponseWriter, r *http.Request, name string) {
	var in corev1.Namespace
	if !readJSON(w, r, &in) {
		return
	}

	if in.Name != name {
		writeBadRequest(w, "k8s api: namespace name in body does not match URL")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.namespaces[name]
	if !ok {
		writeNotFound(w, "k8s api: namespace not found: "+name)

		return
	}

	in.UID = cur.UID
	in.CreationTimestamp = cur.CreationTimestamp
	in.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	in.TypeMeta = cur.TypeMeta

	if in.Status.Phase == "" {
		in.Status.Phase = corev1.NamespaceActive
	}

	s.namespaces[name] = &in
	writeJSON(w, http.StatusOK, &in)
}

func (s *ClusterState) patchNamespace(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.namespaces[name]
	if !ok {
		writeNotFound(w, "k8s api: namespace not found: "+name)

		return
	}

	patched, ok := applyJSONPatch(w, r, cur)
	if !ok {
		return
	}

	ns, castOK := patched.(*corev1.Namespace)
	if !castOK {
		writeBadRequest(w, "k8s api: patched object is not a Namespace")

		return
	}

	ns.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	s.namespaces[name] = ns
	writeJSON(w, http.StatusOK, ns)
}

func (s *ClusterState) deleteNamespace(w http.ResponseWriter, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ns, ok := s.namespaces[name]
	if !ok {
		writeNotFound(w, "k8s api: namespace not found: "+name)

		return
	}

	delete(s.namespaces, name)

	// Cascading delete: drop any namespaced resources that lived inside.
	prefix := name + "/"
	for k := range s.configMaps {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			delete(s.configMaps, k)
		}
	}

	writeJSON(w, http.StatusOK, ns)
}

// newNamespaceObject builds a fresh Namespace with the implicit fields a
// real apiserver fills in on creation (UID, creationTimestamp, status).
func newNamespaceObject(name string) *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			UID:               types.UID(newUID()),
			CreationTimestamp: metav1.NewTime(time.Now()),
			ResourceVersion:   "1",
		},
		Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
}
