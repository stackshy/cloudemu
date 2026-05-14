package kubernetes

import (
	"net/http"
	"sort"
	"strings"
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
		if r.URL.Query().Get("watch") == "true" {
			s.watchNamespaces(w, r)

			return
		}

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

func (s *ClusterState) watchNamespaces(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()

	items := make([]corev1.Namespace, 0, len(s.namespaces))
	for _, n := range s.namespaces {
		items = append(items, *n.DeepCopy())
	}

	s.mu.RUnlock()

	streamWatch(r.Context(), w, s.wNamespaces, "", items)
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

	// Real apiserver auto-creates a "default" ServiceAccount in every new
	// namespace. Mirror that so `kubectl --namespace=<new>` finds an SA.
	sa := newServiceAccountObject(in.Name, "default")
	s.serviceAccounts[serviceAccountKey(in.Name, "default")] = sa

	s.wNamespaces.publish(EventAdded, "", *ns.DeepCopy())
	s.wServiceAccounts.publish(EventAdded, in.Name, *sa.DeepCopy())

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
		// DeepCopy so callers can't reach back into our stored maps via the
		// returned Labels/Annotations references.
		items = append(items, *s.namespaces[n].DeepCopy())
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

	writeJSON(w, http.StatusOK, ns.DeepCopy())
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
	s.wNamespaces.publish(EventModified, "", *in.DeepCopy())
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

	patched.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	s.namespaces[name] = patched
	s.wNamespaces.publish(EventModified, "", *patched.DeepCopy())
	writeJSON(w, http.StatusOK, patched)
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
	s.wNamespaces.publish(EventDeleted, "", *ns.DeepCopy())

	// Cascading delete: drop every namespaced resource keyed under this
	// namespace. Each helper publishes a DELETED event so Watch subscribers
	// see the cascade alongside the namespace going away.
	prefix := name + "/"
	cascadeDeleteWithEvents(s.configMaps, prefix, name, s.wConfigMaps)
	cascadeDeleteWithEvents(s.pods, prefix, name, s.wPods)
	cascadeDeleteWithEvents(s.secrets, prefix, name, s.wSecrets)
	cascadeDeleteWithEvents(s.serviceAccounts, prefix, name, s.wServiceAccounts)
	cascadeDeleteWithEvents(s.services, prefix, name, s.wServices)
	cascadeDeleteWithEvents(s.deployments, prefix, name, s.wDeployments)
	cascadeDeleteWithEvents(s.endpoints, prefix, name, s.wEndpoints)

	writeJSON(w, http.StatusOK, ns.DeepCopy())
}

// cascadeDeleteWithEvents drops every entry in m whose key starts with
// prefix and publishes a DELETED Watch event for each removed object.
func cascadeDeleteWithEvents[V any](m map[string]*V, prefix, ns string, b *broadcaster) {
	for k, v := range m {
		if strings.HasPrefix(k, prefix) {
			b.publish(EventDeleted, ns, *v)
			delete(m, k)
		}
	}
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
