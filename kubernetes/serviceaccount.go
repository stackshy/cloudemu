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

// serveServiceAccounts dispatches /api/v1/{namespaces/{ns}/serviceaccounts|
// serviceaccounts} requests.
//
// Per-resource files share the dispatch shape on purpose; each resource keeps
// its quirks (Service ClusterIP, Secret StringData merge) close to its type.
//
//nolint:dupl // see comment above.
func (s *ClusterState) serveServiceAccounts(w http.ResponseWriter, r *http.Request, route *Route) {
	if route.APIGroup != "" || route.APIVersion != apiVersionV1 {
		writeNotFound(w, "k8s api: serviceaccounts are only served at /api/v1")

		return
	}

	if route.Namespace == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, "k8s api: serviceaccounts cluster-wide: method not allowed: "+r.Method)

			return
		}

		if r.URL.Query().Get("watch") == watchQueryValue {
			s.watchServiceAccounts(w, r, "")

			return
		}

		s.listServiceAccountsAllNamespaces(w)

		return
	}

	if !s.namespaceExists(route.Namespace) {
		writeNotFound(w, "k8s api: namespace not found: "+route.Namespace)

		return
	}

	if route.Name == "" {
		s.serveServiceAccountCollection(w, r, route.Namespace)

		return
	}

	s.serveServiceAccountItem(w, r, route.Namespace, route.Name)
}

func (s *ClusterState) serveServiceAccountCollection(w http.ResponseWriter, r *http.Request, namespace string) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("watch") == watchQueryValue {
			s.watchServiceAccounts(w, r, namespace)

			return
		}

		s.listServiceAccounts(w, namespace)
	case http.MethodPost:
		s.createServiceAccount(w, r, namespace)
	default:
		writeMethodNotAllowed(w, "k8s api: serviceaccounts collection: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) watchServiceAccounts(w http.ResponseWriter, r *http.Request, namespace string) {
	s.mu.RLock()
	items := s.collectServiceAccountsLocked(namespace)
	s.mu.RUnlock()
	streamWatch(r.Context(), w, s.wServiceAccounts, namespace, items)
}

func (s *ClusterState) serveServiceAccountItem(w http.ResponseWriter, r *http.Request, namespace, name string) {
	switch r.Method {
	case http.MethodGet:
		s.getServiceAccount(w, namespace, name)
	case http.MethodPut:
		s.updateServiceAccount(w, r, namespace, name)
	case http.MethodPatch:
		s.patchServiceAccount(w, r, namespace, name)
	case http.MethodDelete:
		s.deleteServiceAccount(w, namespace, name)
	default:
		writeMethodNotAllowed(w, "k8s api: serviceaccount item: method not allowed: "+r.Method)
	}
}

//nolint:dupl // namespaced-create CRUD pattern; copy-paste is clearer than a generic helper.
func (s *ClusterState) createServiceAccount(w http.ResponseWriter, r *http.Request, namespace string) {
	var in corev1.ServiceAccount
	if !readJSON(w, r, &in) {
		return
	}

	if in.Name == "" {
		writeBadRequest(w, "k8s api: serviceaccount: metadata.name is required")

		return
	}

	in.Namespace = namespace

	key := serviceAccountKey(namespace, in.Name)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.serviceAccounts[key]; ok {
		writeAlreadyExists(w, "k8s api: serviceaccount already exists: "+key)

		return
	}

	stamp(&in.ObjectMeta)
	in.TypeMeta = metav1.TypeMeta{Kind: "ServiceAccount", APIVersion: "v1"}

	sa := in
	s.serviceAccounts[key] = &sa
	s.wServiceAccounts.publish(EventAdded, namespace, *sa.DeepCopy())
	writeJSON(w, http.StatusCreated, &sa)
}

func (s *ClusterState) listServiceAccounts(w http.ResponseWriter, namespace string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.collectServiceAccountsLocked(namespace)
	writeJSON(w, http.StatusOK, &corev1.ServiceAccountList{
		TypeMeta: metav1.TypeMeta{Kind: "ServiceAccountList", APIVersion: "v1"},
		Items:    items,
	})
}

func (s *ClusterState) listServiceAccountsAllNamespaces(w http.ResponseWriter) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.collectServiceAccountsLocked("")
	writeJSON(w, http.StatusOK, &corev1.ServiceAccountList{
		TypeMeta: metav1.TypeMeta{Kind: "ServiceAccountList", APIVersion: "v1"},
		Items:    items,
	})
}

func (s *ClusterState) collectServiceAccountsLocked(namespace string) []corev1.ServiceAccount {
	keys := make([]string, 0, len(s.serviceAccounts))

	for k := range s.serviceAccounts {
		if namespace == "" || strings.HasPrefix(k, namespace+"/") {
			keys = append(keys, k)
		}
	}

	sort.Strings(keys)

	items := make([]corev1.ServiceAccount, 0, len(keys))
	for _, k := range keys {
		items = append(items, *s.serviceAccounts[k].DeepCopy())
	}

	return items
}

func (s *ClusterState) getServiceAccount(w http.ResponseWriter, namespace, name string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sa, ok := s.serviceAccounts[serviceAccountKey(namespace, name)]
	if !ok {
		writeNotFound(w, "k8s api: serviceaccount not found: "+namespace+"/"+name)

		return
	}

	writeJSON(w, http.StatusOK, sa.DeepCopy())
}

//nolint:dupl // namespaced-update CRUD pattern; copy-paste is clearer than a generic helper.
func (s *ClusterState) updateServiceAccount(w http.ResponseWriter, r *http.Request, namespace, name string) {
	var in corev1.ServiceAccount
	if !readJSON(w, r, &in) {
		return
	}

	if in.Name != name {
		writeBadRequest(w, "k8s api: serviceaccount name in body does not match URL")

		return
	}

	key := serviceAccountKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.serviceAccounts[key]
	if !ok {
		writeNotFound(w, "k8s api: serviceaccount not found: "+key)

		return
	}

	in.Namespace = namespace
	in.UID = cur.UID
	in.CreationTimestamp = cur.CreationTimestamp
	in.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	in.TypeMeta = cur.TypeMeta

	sa := in
	s.serviceAccounts[key] = &sa
	s.wServiceAccounts.publish(EventModified, namespace, *sa.DeepCopy())
	writeJSON(w, http.StatusOK, &sa)
}

// Patch flow is identical across namespaced resources; sharing would force a
// runtime type-switch and obscure the resource-specific store access.
//
//nolint:dupl // see comment above.
func (s *ClusterState) patchServiceAccount(w http.ResponseWriter, r *http.Request, namespace, name string) {
	key := serviceAccountKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.serviceAccounts[key]
	if !ok {
		writeNotFound(w, "k8s api: serviceaccount not found: "+key)

		return
	}

	patched, ok := applyJSONPatch(w, r, cur)
	if !ok {
		return
	}

	patched.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	s.serviceAccounts[key] = patched
	s.wServiceAccounts.publish(EventModified, namespace, *patched.DeepCopy())
	writeJSON(w, http.StatusOK, patched)
}

func (s *ClusterState) deleteServiceAccount(w http.ResponseWriter, namespace, name string) {
	key := serviceAccountKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	sa, ok := s.serviceAccounts[key]
	if !ok {
		writeNotFound(w, "k8s api: serviceaccount not found: "+key)

		return
	}

	delete(s.serviceAccounts, key)
	s.wServiceAccounts.publish(EventDeleted, namespace, *sa.DeepCopy())
	writeJSON(w, http.StatusOK, sa.DeepCopy())
}

func serviceAccountKey(namespace, name string) string {
	return namespace + "/" + name
}

// newServiceAccountObject builds a fresh ServiceAccount with the implicit
// fields a real apiserver fills in on create. Token Secrets used to be
// auto-created here (pre-1.24); Wave 2 follows current behavior and leaves
// .secrets empty.
func newServiceAccountObject(namespace, name string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{Kind: "ServiceAccount", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			UID:               types.UID(newUID()),
			CreationTimestamp: metav1.NewTime(time.Now()),
			ResourceVersion:   "1",
		},
	}
}
