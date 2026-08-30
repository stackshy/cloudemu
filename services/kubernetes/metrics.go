package kubernetes

import (
	"net/http"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// metrics.k8s.io is an aggregated API in real Kubernetes (served by the
// metrics-server addon, not the core apiserver), so it doesn't fit the
// registry-backed resourceDef model: it has no persisted objects, only a
// point-in-time synthesis over live Pods and the synthetic Node. `kubectl
// top` is the primary consumer — without this endpoint it fails discovery
// before ever issuing a request.

const (
	apiGroupMetrics      = "metrics.k8s.io"
	apiVersionMetrics    = "v1beta1"
	metricsAPIVersion    = apiGroupMetrics + "/" + apiVersionMetrics
	metricsAPIPrefix     = "/apis/" + metricsAPIVersion
	metricsResourcePods  = "pods"
	metricsResourceNodes = "nodes"
	metricsWindow        = "60s"
	podMetricCPUUsage    = "250m"
	podMetricMemUsage    = "64Mi"
	nodeMetricCPUUsage   = "500m"
	nodeMetricMemUsage   = "128Mi"
)

// serveMetrics answers every /apis/metrics.k8s.io/v1beta1/... request: the
// group-version's own APIResourceList, and the pods/nodes metrics endpoints
// kubectl top reads. Values are fixed synthetic constants — there is no real
// resource usage to sample in an in-memory emulator.
func (s *ClusterState) serveMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, "k8s api: metrics.k8s.io: method not allowed: "+r.Method)

		return
	}

	parts := splitPath(strings.TrimPrefix(r.URL.Path, metricsAPIPrefix))

	if s.serveMetricsTopLevel(w, parts) {
		return
	}

	if namespace, name, ok := parseNamespacedPodMetricsPath(parts); ok {
		if name == "" {
			s.servePodMetricsList(w, namespace)
		} else {
			s.servePodMetricsItem(w, namespace, name)
		}

		return
	}

	writeNotFound(w, "k8s api: metrics.k8s.io: unrecognized path "+r.URL.Path)
}

// serveMetricsTopLevel handles every shape except the namespaced Pod metrics
// paths: the group-version discovery document, the all-namespaces Pod
// metrics list, and the single synthetic Node's metrics (list or item).
// Reports whether it served parts.
func (s *ClusterState) serveMetricsTopLevel(w http.ResponseWriter, parts []string) bool {
	switch {
	case len(parts) == 0:
		writeJSON(w, http.StatusOK, metricsAPIResourceList())
	case len(parts) == 1 && parts[0] == metricsResourcePods:
		s.servePodMetricsList(w, "")
	case len(parts) == 1 && parts[0] == metricsResourceNodes:
		s.serveNodeMetricsList(w)
	case len(parts) == 2 && parts[0] == metricsResourceNodes:
		s.serveNodeMetricsItem(w, parts[1])
	default:
		return false
	}

	return true
}

// metricsPathSegsPodList and metricsPathSegsPodItem are the segment counts of
// "namespaces/{ns}/pods" and "namespaces/{ns}/pods/{name}" respectively.
const (
	metricsPathSegsPodList = 3
	metricsPathSegsPodItem = 4
)

// parseNamespacedPodMetricsPath matches the "namespaces/{ns}/pods" and
// "namespaces/{ns}/pods/{name}" path shapes. name is "" for the list form.
func parseNamespacedPodMetricsPath(parts []string) (namespace, name string, ok bool) {
	if len(parts) == 0 || parts[0] != namespacesSegment {
		return "", "", false
	}

	switch len(parts) {
	case metricsPathSegsPodList:
		if parts[2] != metricsResourcePods {
			return "", "", false
		}

		return parts[1], "", true
	case metricsPathSegsPodItem:
		if parts[2] != metricsResourcePods {
			return "", "", false
		}

		return parts[1], parts[3], true
	default:
		return "", "", false
	}
}

// metricsAPIResourceList answers GET /apis/metrics.k8s.io/v1beta1 — the
// group-version discovery document naming the two resources this endpoint
// serves.
func metricsAPIResourceList() map[string]any {
	return map[string]any{
		"kind":         "APIResourceList",
		"apiVersion":   "v1",
		"groupVersion": metricsAPIVersion,
		"resources": []map[string]any{
			{"name": metricsResourcePods, "namespaced": true, "kind": "PodMetrics", "verbs": []string{"get", "list"}},
			{"name": metricsResourceNodes, "namespaced": false, "kind": "NodeMetrics", "verbs": []string{"get", "list"}},
		},
	}
}

func (s *ClusterState) servePodMetricsList(w http.ResponseWriter, namespace string) {
	s.mu.RLock()
	items := s.collectPodMetricsLocked(namespace, s.now())
	s.mu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion": metricsAPIVersion,
		"kind":       "PodMetricsList",
		"items":      items,
	})
}

func (s *ClusterState) servePodMetricsItem(w http.ResponseWriter, namespace, name string) {
	s.mu.RLock()
	pod, ok := s.pods[podKey(namespace, name)]

	var obj map[string]any
	if ok {
		obj = podMetricsObject(pod, s.now())
	}
	s.mu.RUnlock()

	if !ok {
		writeNotFound(w, "k8s api: pod not found: "+namespace+"/"+name)

		return
	}

	writeJSON(w, http.StatusOK, obj)
}

// collectPodMetricsLocked returns PodMetrics for every live Pod in namespace
// ("" = all namespaces), sorted by "<namespace>/<name>" for a stable list
// order. Callers hold s.mu (at least RLock).
func (s *ClusterState) collectPodMetricsLocked(namespace string, now metav1.Time) []map[string]any {
	keys := make([]string, 0, len(s.pods))

	for k, pod := range s.pods {
		if namespace == "" || pod.Namespace == namespace {
			keys = append(keys, k)
		}
	}

	sort.Strings(keys)

	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, podMetricsObject(s.pods[k], now))
	}

	return out
}

// podMetricsObject synthesizes a PodMetrics object with one fixed-usage entry
// per container in pod.
func podMetricsObject(pod *corev1.Pod, now metav1.Time) map[string]any {
	containers := make([]map[string]any, 0, len(pod.Spec.Containers))

	for i := range pod.Spec.Containers {
		c := &pod.Spec.Containers[i]
		containers = append(containers, map[string]any{
			"name":  c.Name,
			"usage": map[string]any{"cpu": podMetricCPUUsage, "memory": podMetricMemUsage},
		})
	}

	return map[string]any{
		"apiVersion": metricsAPIVersion,
		"kind":       "PodMetrics",
		"metadata":   map[string]any{"name": pod.Name, "namespace": pod.Namespace},
		"timestamp":  now,
		"window":     metricsWindow,
		"containers": containers,
	}
}

func (s *ClusterState) serveNodeMetricsList(w http.ResponseWriter) {
	s.mu.RLock()
	now := s.now()

	nodes := s.nodesLocked()
	items := make([]map[string]any, 0, len(nodes))

	for i := range nodes {
		items = append(items, nodeMetricsObject(nodes[i].name, now))
	}
	s.mu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion": metricsAPIVersion,
		"kind":       "NodeMetricsList",
		"items":      items,
	})
}

func (s *ClusterState) serveNodeMetricsItem(w http.ResponseWriter, name string) {
	s.mu.RLock()
	now := s.now()

	nodes := s.nodesLocked()
	found := false

	for i := range nodes {
		if nodes[i].name == name {
			found = true

			break
		}
	}
	s.mu.RUnlock()

	if !found {
		writeNotFound(w, "k8s api: node not found: "+name)

		return
	}

	writeJSON(w, http.StatusOK, nodeMetricsObject(name, now))
}

// nodeMetricsObject synthesizes fixed usage for one synthetic Node.
func nodeMetricsObject(name string, now metav1.Time) map[string]any {
	return map[string]any{
		"apiVersion": metricsAPIVersion,
		"kind":       "NodeMetrics",
		"metadata":   map[string]any{"name": name},
		"timestamp":  now,
		"window":     metricsWindow,
		"usage":      map[string]any{"cpu": nodeMetricCPUUsage, "memory": nodeMetricMemUsage},
	}
}
