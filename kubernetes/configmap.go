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

// serveConfigMaps dispatches /api/v1/{namespaces/{ns}/configmaps|configmaps}
// requests.
//
// Per-resource files share the dispatch shape on purpose; each resource keeps
// its quirks (Service ClusterIP, Secret StringData merge) close to its type.
//
//nolint:dupl // see comment above.
func (s *ClusterState) serveConfigMaps(w http.ResponseWriter, r *http.Request, route *Route) {
	if route.APIGroup != "" || route.APIVersion != apiVersionV1 {
		writeNotFound(w, "k8s api: configmaps are only served at /api/v1")

		return
	}

	if route.Namespace == "" {
		// All-namespaces collection — only GET is meaningful.
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, "k8s api: configmaps cluster-wide: method not allowed: "+r.Method)

			return
		}

		if r.URL.Query().Get("watch") == watchQueryValue {
			s.watchConfigMaps(w, r, "")

			return
		}

		s.listConfigMapsAllNamespaces(w)

		return
	}

	if !s.namespaceExists(route.Namespace) {
		writeNotFound(w, "k8s api: namespace not found: "+route.Namespace)

		return
	}

	if route.Name == "" {
		s.serveConfigMapCollection(w, r, route.Namespace)

		return
	}

	s.serveConfigMapItem(w, r, route.Namespace, route.Name)
}

func (s *ClusterState) serveConfigMapCollection(w http.ResponseWriter, r *http.Request, namespace string) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("watch") == watchQueryValue {
			s.watchConfigMaps(w, r, namespace)

			return
		}

		s.listConfigMaps(w, namespace)
	case http.MethodPost:
		s.createConfigMap(w, r, namespace)
	default:
		writeMethodNotAllowed(w, "k8s api: configmaps collection: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) watchConfigMaps(w http.ResponseWriter, r *http.Request, namespace string) {
	s.mu.RLock()
	sub := s.wConfigMaps.subscribe(namespace)
	items := s.collectConfigMapsLocked(namespace)
	s.mu.RUnlock()
	streamWatch(r.Context(), w, sub, items)
}

func (s *ClusterState) serveConfigMapItem(w http.ResponseWriter, r *http.Request, namespace, name string) {
	switch r.Method {
	case http.MethodGet:
		s.getConfigMap(w, namespace, name)
	case http.MethodPut:
		s.updateConfigMap(w, r, namespace, name)
	case http.MethodPatch:
		s.patchConfigMap(w, r, namespace, name)
	case http.MethodDelete:
		s.deleteConfigMap(w, namespace, name)
	default:
		writeMethodNotAllowed(w, "k8s api: configmap item: method not allowed: "+r.Method)
	}
}

//nolint:dupl // namespaced-create CRUD pattern; copy-paste is clearer than a generic helper.
func (s *ClusterState) createConfigMap(w http.ResponseWriter, r *http.Request, namespace string) {
	var in corev1.ConfigMap
	if !readJSON(w, r, &in) {
		return
	}

	if in.Name == "" {
		writeBadRequest(w, "k8s api: configmap: metadata.name is required")

		return
	}

	// Mirror real apiserver: namespace in the URL wins over a stale body.
	in.Namespace = namespace

	key := configMapKey(namespace, in.Name)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.configMaps[key]; ok {
		writeAlreadyExists(w, "k8s api: configmap already exists: "+key)

		return
	}

	stamp(&in.ObjectMeta)
	in.TypeMeta = metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"}

	cm := in
	s.configMaps[key] = &cm
	s.wConfigMaps.publish(EventAdded, namespace, *cm.DeepCopy())
	writeJSON(w, http.StatusCreated, &cm)
}

func (s *ClusterState) listConfigMaps(w http.ResponseWriter, namespace string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.collectConfigMapsLocked(namespace)
	writeJSON(w, http.StatusOK, &corev1.ConfigMapList{
		TypeMeta: metav1.TypeMeta{Kind: "ConfigMapList", APIVersion: "v1"},
		Items:    items,
	})
}

func (s *ClusterState) listConfigMapsAllNamespaces(w http.ResponseWriter) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.collectConfigMapsLocked("")
	writeJSON(w, http.StatusOK, &corev1.ConfigMapList{
		TypeMeta: metav1.TypeMeta{Kind: "ConfigMapList", APIVersion: "v1"},
		Items:    items,
	})
}

func (s *ClusterState) collectConfigMapsLocked(namespace string) []corev1.ConfigMap {
	keys := make([]string, 0, len(s.configMaps))

	for k := range s.configMaps {
		if namespace == "" || strings.HasPrefix(k, namespace+"/") {
			keys = append(keys, k)
		}
	}

	sort.Strings(keys)

	items := make([]corev1.ConfigMap, 0, len(keys))
	for _, k := range keys {
		// DeepCopy so callers can't reach back into our stored Data map via
		// the returned ConfigMap.
		items = append(items, *s.configMaps[k].DeepCopy())
	}

	return items
}

func (s *ClusterState) getConfigMap(w http.ResponseWriter, namespace, name string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cm, ok := s.configMaps[configMapKey(namespace, name)]
	if !ok {
		writeNotFound(w, "k8s api: configmap not found: "+namespace+"/"+name)

		return
	}

	writeJSON(w, http.StatusOK, cm.DeepCopy())
}

func (s *ClusterState) updateConfigMap(w http.ResponseWriter, r *http.Request, namespace, name string) {
	var in corev1.ConfigMap
	if !readJSON(w, r, &in) {
		return
	}

	// Match updateNamespace's behavior: real apiserver requires
	// metadata.name to be present and equal to the URL name.
	if in.Name != name {
		writeBadRequest(w, "k8s api: configmap name in body does not match URL")

		return
	}

	key := configMapKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.configMaps[key]
	if !ok {
		writeNotFound(w, "k8s api: configmap not found: "+key)

		return
	}

	in.Name = name
	in.Namespace = namespace
	in.UID = cur.UID
	in.CreationTimestamp = cur.CreationTimestamp
	in.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	in.TypeMeta = cur.TypeMeta

	cm := in
	s.configMaps[key] = &cm
	s.wConfigMaps.publish(EventModified, namespace, *cm.DeepCopy())
	writeJSON(w, http.StatusOK, &cm)
}

// Patch flow is identical across namespaced resources; sharing would force a
// runtime type-switch and obscure the resource-specific store access.
//
//nolint:dupl // see comment above.
func (s *ClusterState) patchConfigMap(w http.ResponseWriter, r *http.Request, namespace, name string) {
	key := configMapKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.configMaps[key]
	if !ok {
		writeNotFound(w, "k8s api: configmap not found: "+key)

		return
	}

	patched, ok := applyJSONPatch(w, r, cur)
	if !ok {
		return
	}

	patched.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	s.configMaps[key] = patched
	s.wConfigMaps.publish(EventModified, namespace, *patched.DeepCopy())
	writeJSON(w, http.StatusOK, patched)
}

func (s *ClusterState) deleteConfigMap(w http.ResponseWriter, namespace, name string) {
	key := configMapKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	cm, ok := s.configMaps[key]
	if !ok {
		writeNotFound(w, "k8s api: configmap not found: "+key)

		return
	}

	delete(s.configMaps, key)
	s.wConfigMaps.publish(EventDeleted, namespace, *cm.DeepCopy())
	writeJSON(w, http.StatusOK, cm.DeepCopy())
}

func (s *ClusterState) namespaceExists(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.namespaces[name]

	return ok
}

func configMapKey(namespace, name string) string {
	return namespace + "/" + name
}

// stamp fills in the implicit fields a real apiserver writes on Create:
// UID, creationTimestamp, resourceVersion.
func stamp(om *metav1.ObjectMeta) {
	om.UID = types.UID(newUID())
	om.CreationTimestamp = metav1.NewTime(time.Now())
	om.ResourceVersion = "1"
}
