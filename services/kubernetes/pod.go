package kubernetes

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// servePods dispatches /api/v1/{namespaces/{ns}/pods|pods} requests.
//
// Per-resource files share the dispatch shape on purpose; each resource keeps
// its quirks (Service ClusterIP, Secret StringData merge) close to its type.
//
//nolint:dupl // per-resource dispatch shape; see comment above.
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

		if r.URL.Query().Get("watch") == watchQueryValue {
			s.watchPods(w, r, "")

			return
		}

		s.listPodsAllNamespaces(w, r)

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
		if r.URL.Query().Get("watch") == watchQueryValue {
			s.watchPods(w, r, namespace)

			return
		}

		s.listPods(w, r, namespace)
	case http.MethodPost:
		s.createPod(w, r, namespace)
	default:
		writeMethodNotAllowed(w, "k8s api: pods collection: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) watchPods(w http.ResponseWriter, r *http.Request, namespace string) {
	sel, fields := parseListSelectors(r)
	serveWatch(s, w, r, s.wPods, namespace,
		func() []corev1.Pod { return s.collectPodsLocked(namespace) },
		func(p corev1.Pod) bool { return sel.Matches(labels.Set(p.Labels)) && podMatchesFields(&p, fields) })
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
		s.deletePod(w, r, namespace, name)
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

	// LimitRange defaulting/validation runs before dry-run so the echoed object
	// reflects applied defaults.
	if status := s.applyLimitRange(namespace, &in); status != nil {
		writeJSON(w, int(status.Code), status)

		return
	}

	s.stamp(&in.ObjectMeta)
	in.TypeMeta = metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"}

	if isDryRun(r) {
		writeJSON(w, http.StatusCreated, &in)

		return
	}

	// Quota is checked AND reserved only on a real (non-dry-run) create, so a
	// dry-run never consumes quota.
	if status := s.checkAndReserveQuota(namespace, "Pod", resourcePods); status != nil {
		writeJSON(w, int(status.Code), status)

		return
	}

	pod := in
	// cloudemu has no kubelet; a directly-created Pod is driven Running (with a
	// synthetic IP and ready containers) so it behaves like a scheduled Pod. A
	// caller-supplied terminal phase (Succeeded/Failed) is preserved.
	s.markPodRunningLocked(&pod)
	s.pods[key] = &pod
	// A new Pod may satisfy a Service selector — refresh endpoints.
	s.resyncEndpointsForNamespaceLocked(namespace)
	s.wPods.publish(EventAdded, namespace, *pod.DeepCopy())
	writeJSON(w, http.StatusCreated, &pod)
}

func (s *ClusterState) listPods(w http.ResponseWriter, r *http.Request, namespace string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := filterPods(s.collectPodsLocked(namespace), r)
	items, cont := listPage(items, r)
	writeJSON(w, http.StatusOK, &corev1.PodList{
		TypeMeta: metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"},
		ListMeta: metav1.ListMeta{Continue: cont},
		Items:    items,
	})
}

func (s *ClusterState) listPodsAllNamespaces(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := filterPods(s.collectPodsLocked(""), r)
	items, cont := listPage(items, r)
	writeJSON(w, http.StatusOK, &corev1.PodList{
		TypeMeta: metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"},
		ListMeta: metav1.ListMeta{Continue: cont},
		Items:    items,
	})
}

// filterPods applies the request's labelSelector and the metadata.name /
// status.phase fieldSelectors to a Pod list (kubectl get pods -l / --field-
// selector). An empty/absent selector returns everything.
func filterPods(pods []corev1.Pod, r *http.Request) []corev1.Pod {
	sel, fields := parseListSelectors(r)

	out := make([]corev1.Pod, 0, len(pods))

	for i := range pods {
		p := &pods[i]
		if sel.Matches(labels.Set(p.Labels)) && podMatchesFields(p, fields) {
			out = append(out, *p)
		}
	}

	return out
}

func podMatchesFields(p *corev1.Pod, fields map[string]string) bool {
	for k, v := range fields {
		switch k {
		case fieldMetadataName:
			if p.Name != v {
				return false
			}
		case fieldMetadataNamespace:
			if p.Namespace != v {
				return false
			}
		case fieldStatusPhase:
			if string(p.Status.Phase) != v {
				return false
			}
		case fieldSpecNodeName:
			// Every reconciler-materialized Pod is scheduled to the single
			// synthetic node, so this is a common and answerable selector.
			if p.Spec.NodeName != v {
				return false
			}
		default:
			return false
		}
	}

	return true
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
	// deletionTimestamp is server-owned — preserve it across a PUT.
	in.DeletionTimestamp = cur.DeletionTimestamp

	if isDryRun(r) {
		writeJSON(w, http.StatusOK, &in)

		return
	}

	// Last finalizer removed on a Terminating Pod → complete the delete.
	if finalizersDrained(&in.ObjectMeta) {
		delete(s.pods, key)
		s.resyncEndpointsForNamespaceLocked(namespace)
		s.wPods.publish(EventDeleted, namespace, *in.DeepCopy())
		writeJSON(w, http.StatusOK, &in)

		return
	}

	pod := in
	// A spec-only PUT (no status) must not drop the Pod out of Running — keep it
	// materialized like createPod does.
	if pod.Status.Phase == "" {
		s.markPodRunningLocked(&pod)
	}

	s.pods[key] = &pod
	// Labels may have changed to (no longer) match a Service selector.
	s.resyncEndpointsForNamespaceLocked(namespace)
	s.wPods.publish(EventModified, namespace, *pod.DeepCopy())
	writeJSON(w, http.StatusOK, &pod)
}

// Patch flow is identical across namespaced resources; sharing would force a
// runtime type-switch and obscure the resource-specific store access.
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

	if isDryRun(r) {
		writeJSON(w, http.StatusOK, patched)

		return
	}

	// A patch removing the last finalizer from a Terminating Pod completes the
	// delete (patch inherits cur's deletionTimestamp).
	if finalizersDrained(&patched.ObjectMeta) {
		delete(s.pods, key)
		s.resyncEndpointsForNamespaceLocked(namespace)
		s.wPods.publish(EventDeleted, namespace, *patched.DeepCopy())
		writeJSON(w, http.StatusOK, patched)

		return
	}

	s.pods[key] = patched
	// A patch may have changed labels that match a Service selector.
	s.resyncEndpointsForNamespaceLocked(namespace)
	s.wPods.publish(EventModified, namespace, *patched.DeepCopy())
	writeJSON(w, http.StatusOK, patched)
}

func (s *ClusterState) deletePod(w http.ResponseWriter, r *http.Request, namespace, name string) {
	key := podKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	pod, ok := s.pods[key]
	if !ok {
		writeNotFound(w, "k8s api: pod not found: "+key)

		return
	}

	if isDryRun(r) {
		writeJSON(w, http.StatusOK, pod.DeepCopy())

		return
	}

	// Finalizer-gated deletion: a Pod with finalizers goes Terminating and is
	// removed only when the last finalizer is dropped via update/patch.
	if s.markForDeletion(&pod.ObjectMeta) {
		pod.ResourceVersion = bumpResourceVersion(pod.ResourceVersion)
		s.wPods.publish(EventModified, namespace, *pod.DeepCopy())
		writeJSON(w, http.StatusOK, pod.DeepCopy())

		return
	}

	delete(s.pods, key)
	// A Service may have been pointing at this Pod — refresh its endpoints.
	s.resyncEndpointsForNamespaceLocked(namespace)
	s.wPods.publish(EventDeleted, namespace, *pod.DeepCopy())
	writeJSON(w, http.StatusOK, pod.DeepCopy())
}

func podKey(namespace, name string) string {
	return namespace + "/" + name
}

// servePodSubresource serves the pod subresources kubectl reaches for. `log`
// returns synthetic container output (there are no real containers, but a
// clean 200 with a deterministic line keeps `kubectl logs` and log-scraping
// clients working). `exec`/`attach`/`portforward` require a streaming protocol
// upgrade the emulator does not implement and return a typed 501 Status so
// client-go surfaces a clear error rather than a raw connection failure.
func (s *ClusterState) servePodSubresource(w http.ResponseWriter, r *http.Request, route *Route) {
	s.mu.RLock()
	pod, ok := s.pods[podKey(route.Namespace, route.Name)]

	var container string
	if ok {
		container = firstContainerName(pod)
	}
	s.mu.RUnlock()

	if !ok {
		writeNotFound(w, "k8s api: pod not found: "+podKey(route.Namespace, route.Name))

		return
	}

	switch route.Subresource {
	case subresourcePodLog:
		servePodLog(w, r, route, container)
	case subresourcePodExec, subresourcePodAttach, subresourcePodPortForward:
		writeStreamingUnsupported(w, route)
	case subresourceEviction:
		s.evictPod(w, r, route.Namespace, route.Name)
	default:
		writeNotFound(w, "k8s api: subresource not implemented: pods/"+route.Name+"/"+route.Subresource)
	}
}

// servePodLog writes a deterministic synthetic log line for the requested
// container. Streaming query params (follow, tail, previous) are accepted and
// ignored — the response is a single flush, which kubectl handles fine.
func servePodLog(w http.ResponseWriter, r *http.Request, route *Route, defaultContainer string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, "k8s api: pods/log: method not allowed: "+r.Method)

		return
	}

	container := r.URL.Query().Get("container")
	if container == "" {
		container = defaultContainer
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// Response is text/plain (not HTML) and the identifiers are path-derived, so
	// reflecting them carries no XSS risk.
	//nolint:gosec // G705: text/plain log echo, not an HTML sink.
	_, _ = fmt.Fprintf(w, "cloudemu: synthetic log stream for pod %s/%s container %q\n",
		route.Namespace, route.Name, container)
}

// writeStreamingUnsupported returns a typed 501 for the pod subresources that
// need a SPDY/WebSocket upgrade cloudemu does not implement.
func writeStreamingUnsupported(w http.ResponseWriter, route *Route) {
	writeStatus(w, http.StatusNotImplemented, metav1.StatusReason("NotImplemented"),
		"k8s api: pods/"+route.Subresource+" requires a streaming connection upgrade cloudemu does not implement")
}

func firstContainerName(pod *corev1.Pod) string {
	if len(pod.Spec.Containers) > 0 {
		return pod.Spec.Containers[0].Name
	}

	return ""
}
