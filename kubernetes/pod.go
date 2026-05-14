package kubernetes

import (
	"net/http"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// servePods dispatches /api/v1/{namespaces/{ns}/pods|pods} requests.
//
// Per-resource files share the dispatch shape on purpose; each resource keeps
// its quirks (Service ClusterIP, Secret StringData merge) close to its type.
//
//nolint:dupl // see comment above.
func (s *ClusterState) servePods(w http.ResponseWriter, r *http.Request, route *Route) {
	if route.APIGroup != "" || route.APIVersion != apiVersionV1 {
		writeNotFound(w, "k8s api: pods are only served at /api/v1")

		return
	}

	if route.Namespace == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, "k8s api: pods cluster-wide: method not allowed: "+r.Method)

			return
		}

		s.listPodsAllNamespaces(w)

		return
	}

	if !s.namespaceExists(route.Namespace) {
		writeNotFound(w, "k8s api: namespace not found: "+route.Namespace)

		return
	}

	if route.Name == "" {
		s.servePodCollection(w, r, route.Namespace)

		return
	}

	s.servePodItem(w, r, route.Namespace, route.Name)
}

func (s *ClusterState) servePodCollection(w http.ResponseWriter, r *http.Request, namespace string) {
	switch r.Method {
	case http.MethodGet:
		s.listPods(w, namespace)
	case http.MethodPost:
		s.createPod(w, r, namespace)
	default:
		writeMethodNotAllowed(w, "k8s api: pods collection: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) servePodItem(w http.ResponseWriter, r *http.Request, namespace, name string) {
	switch r.Method {
	case http.MethodGet:
		s.getPod(w, namespace, name)
	case http.MethodPut:
		s.updatePod(w, r, namespace, name)
	case http.MethodPatch:
		s.patchPod(w, r, namespace, name)
	case http.MethodDelete:
		s.deletePod(w, namespace, name)
	default:
		writeMethodNotAllowed(w, "k8s api: pod item: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) createPod(w http.ResponseWriter, r *http.Request, namespace string) {
	var in corev1.Pod
	if !readJSON(w, r, &in) {
		return
	}

	if in.Name == "" {
		writeBadRequest(w, "k8s api: pod: metadata.name is required")

		return
	}

	in.Namespace = namespace

	key := podKey(namespace, in.Name)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.pods[key]; ok {
		writeAlreadyExists(w, "k8s api: pod already exists: "+key)

		return
	}

	stamp(&in.ObjectMeta)
	in.TypeMeta = metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"}

	// No scheduler in Wave 2 — every Pod is born Pending and stays there
	// until something (a future Phase 4 controller, or the test author)
	// explicitly mutates Status.
	if in.Status.Phase == "" {
		in.Status.Phase = corev1.PodPending
	}

	pod := in
	s.pods[key] = &pod
	writeJSON(w, http.StatusCreated, &pod)
}

func (s *ClusterState) listPods(w http.ResponseWriter, namespace string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.collectPodsLocked(namespace)
	writeJSON(w, http.StatusOK, &corev1.PodList{
		TypeMeta: metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"},
		Items:    items,
	})
}

func (s *ClusterState) listPodsAllNamespaces(w http.ResponseWriter) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.collectPodsLocked("")
	writeJSON(w, http.StatusOK, &corev1.PodList{
		TypeMeta: metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"},
		Items:    items,
	})
}

func (s *ClusterState) collectPodsLocked(namespace string) []corev1.Pod {
	keys := make([]string, 0, len(s.pods))

	for k := range s.pods {
		if namespace == "" || strings.HasPrefix(k, namespace+"/") {
			keys = append(keys, k)
		}
	}

	sort.Strings(keys)

	items := make([]corev1.Pod, 0, len(keys))
	for _, k := range keys {
		items = append(items, *s.pods[k].DeepCopy())
	}

	return items
}

func (s *ClusterState) getPod(w http.ResponseWriter, namespace, name string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pod, ok := s.pods[podKey(namespace, name)]
	if !ok {
		writeNotFound(w, "k8s api: pod not found: "+namespace+"/"+name)

		return
	}

	writeJSON(w, http.StatusOK, pod.DeepCopy())
}

func (s *ClusterState) updatePod(w http.ResponseWriter, r *http.Request, namespace, name string) {
	var in corev1.Pod
	if !readJSON(w, r, &in) {
		return
	}

	if in.Name != name {
		writeBadRequest(w, "k8s api: pod name in body does not match URL")

		return
	}

	key := podKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.pods[key]
	if !ok {
		writeNotFound(w, "k8s api: pod not found: "+key)

		return
	}

	in.Namespace = namespace
	in.UID = cur.UID
	in.CreationTimestamp = cur.CreationTimestamp
	in.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	in.TypeMeta = cur.TypeMeta

	pod := in
	s.pods[key] = &pod
	writeJSON(w, http.StatusOK, &pod)
}

// Patch flow is identical across namespaced resources; sharing would force a
// runtime type-switch and obscure the resource-specific store access.
//
//nolint:dupl // see comment above.
func (s *ClusterState) patchPod(w http.ResponseWriter, r *http.Request, namespace, name string) {
	key := podKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.pods[key]
	if !ok {
		writeNotFound(w, "k8s api: pod not found: "+key)

		return
	}

	patched, ok := applyJSONPatch(w, r, cur)
	if !ok {
		return
	}

	patched.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	s.pods[key] = patched
	writeJSON(w, http.StatusOK, patched)
}

func (s *ClusterState) deletePod(w http.ResponseWriter, namespace, name string) {
	key := podKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	pod, ok := s.pods[key]
	if !ok {
		writeNotFound(w, "k8s api: pod not found: "+key)

		return
	}

	delete(s.pods, key)
	writeJSON(w, http.StatusOK, pod.DeepCopy())
}

func podKey(namespace, name string) string {
	return namespace + "/" + name
}
