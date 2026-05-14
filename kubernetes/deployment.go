package kubernetes

import (
	"net/http"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// apiGroupApps is the API group Deployments (and other workload controllers)
// live under: /apis/apps/v1/...
const apiGroupApps = "apps"

// serveDeployments dispatches /apis/apps/v1/{namespaces/{ns}/deployments|
// deployments} requests. Deployments are the first apps/v1 resource so the
// route group check is different from the core/v1 handlers.
func (s *ClusterState) serveDeployments(w http.ResponseWriter, r *http.Request, route *Route) {
	if route.APIGroup != apiGroupApps || route.APIVersion != apiVersionV1 {
		writeNotFound(w, "k8s api: deployments are only served at /apis/apps/v1")

		return
	}

	if route.Namespace == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, "k8s api: deployments cluster-wide: method not allowed: "+r.Method)

			return
		}

		s.listDeploymentsAllNamespaces(w)

		return
	}

	if !s.namespaceExists(route.Namespace) {
		writeNotFound(w, "k8s api: namespace not found: "+route.Namespace)

		return
	}

	if route.Name == "" {
		s.serveDeploymentCollection(w, r, route.Namespace)

		return
	}

	s.serveDeploymentItem(w, r, route.Namespace, route.Name)
}

func (s *ClusterState) serveDeploymentCollection(w http.ResponseWriter, r *http.Request, namespace string) {
	switch r.Method {
	case http.MethodGet:
		s.listDeployments(w, namespace)
	case http.MethodPost:
		s.createDeployment(w, r, namespace)
	default:
		writeMethodNotAllowed(w, "k8s api: deployments collection: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) serveDeploymentItem(w http.ResponseWriter, r *http.Request, namespace, name string) {
	switch r.Method {
	case http.MethodGet:
		s.getDeployment(w, namespace, name)
	case http.MethodPut:
		s.updateDeployment(w, r, namespace, name)
	case http.MethodPatch:
		s.patchDeployment(w, r, namespace, name)
	case http.MethodDelete:
		s.deleteDeployment(w, namespace, name)
	default:
		writeMethodNotAllowed(w, "k8s api: deployment item: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) createDeployment(w http.ResponseWriter, r *http.Request, namespace string) {
	var in appsv1.Deployment
	if !readJSON(w, r, &in) {
		return
	}

	if in.Name == "" {
		writeBadRequest(w, "k8s api: deployment: metadata.name is required")

		return
	}

	in.Namespace = namespace

	key := deploymentKey(namespace, in.Name)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.deployments[key]; ok {
		writeAlreadyExists(w, "k8s api: deployment already exists: "+key)

		return
	}

	stamp(&in.ObjectMeta)
	in.TypeMeta = metav1.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"}

	// No controller-manager in Wave 2 — mirror spec.replicas straight onto
	// status so basic "is my deployment 'ready'?" checks pass. Real
	// apiserver leaves status empty; the deployment controller fills it in
	// after observing ReplicaSets and Pods.
	mirrorDeploymentReplicas(&in)

	dep := in
	s.deployments[key] = &dep
	writeJSON(w, http.StatusCreated, &dep)
}

func (s *ClusterState) listDeployments(w http.ResponseWriter, namespace string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.collectDeploymentsLocked(namespace)
	writeJSON(w, http.StatusOK, &appsv1.DeploymentList{
		TypeMeta: metav1.TypeMeta{Kind: "DeploymentList", APIVersion: "apps/v1"},
		Items:    items,
	})
}

func (s *ClusterState) listDeploymentsAllNamespaces(w http.ResponseWriter) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := s.collectDeploymentsLocked("")
	writeJSON(w, http.StatusOK, &appsv1.DeploymentList{
		TypeMeta: metav1.TypeMeta{Kind: "DeploymentList", APIVersion: "apps/v1"},
		Items:    items,
	})
}

func (s *ClusterState) collectDeploymentsLocked(namespace string) []appsv1.Deployment {
	keys := make([]string, 0, len(s.deployments))

	for k := range s.deployments {
		if namespace == "" || strings.HasPrefix(k, namespace+"/") {
			keys = append(keys, k)
		}
	}

	sort.Strings(keys)

	items := make([]appsv1.Deployment, 0, len(keys))
	for _, k := range keys {
		items = append(items, *s.deployments[k].DeepCopy())
	}

	return items
}

func (s *ClusterState) getDeployment(w http.ResponseWriter, namespace, name string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dep, ok := s.deployments[deploymentKey(namespace, name)]
	if !ok {
		writeNotFound(w, "k8s api: deployment not found: "+namespace+"/"+name)

		return
	}

	writeJSON(w, http.StatusOK, dep.DeepCopy())
}

func (s *ClusterState) updateDeployment(w http.ResponseWriter, r *http.Request, namespace, name string) {
	var in appsv1.Deployment
	if !readJSON(w, r, &in) {
		return
	}

	if in.Name != name {
		writeBadRequest(w, "k8s api: deployment name in body does not match URL")

		return
	}

	key := deploymentKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.deployments[key]
	if !ok {
		writeNotFound(w, "k8s api: deployment not found: "+key)

		return
	}

	in.Namespace = namespace
	in.UID = cur.UID
	in.CreationTimestamp = cur.CreationTimestamp
	in.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	in.TypeMeta = cur.TypeMeta

	mirrorDeploymentReplicas(&in)

	dep := in
	s.deployments[key] = &dep
	writeJSON(w, http.StatusOK, &dep)
}

func (s *ClusterState) patchDeployment(w http.ResponseWriter, r *http.Request, namespace, name string) {
	key := deploymentKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := s.deployments[key]
	if !ok {
		writeNotFound(w, "k8s api: deployment not found: "+key)

		return
	}

	patched, ok := applyJSONPatch(w, r, cur)
	if !ok {
		return
	}

	patched.ResourceVersion = bumpResourceVersion(cur.ResourceVersion)
	mirrorDeploymentReplicas(patched)
	s.deployments[key] = patched
	writeJSON(w, http.StatusOK, patched)
}

func (s *ClusterState) deleteDeployment(w http.ResponseWriter, namespace, name string) {
	key := deploymentKey(namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	dep, ok := s.deployments[key]
	if !ok {
		writeNotFound(w, "k8s api: deployment not found: "+key)

		return
	}

	delete(s.deployments, key)
	writeJSON(w, http.StatusOK, dep.DeepCopy())
}

func deploymentKey(namespace, name string) string {
	return namespace + "/" + name
}

// mirrorDeploymentReplicas copies spec.replicas onto status. There is no
// controller in Wave 2 to drive a real reconcile loop, so we synthesize the
// terminal-state status fields tests typically assert on. Spec.Replicas of
// nil is treated as 1 (real k8s default).
func mirrorDeploymentReplicas(d *appsv1.Deployment) {
	var replicas int32 = 1
	if d.Spec.Replicas != nil {
		replicas = *d.Spec.Replicas
	}

	d.Status.Replicas = replicas
	d.Status.ReadyReplicas = replicas
	d.Status.AvailableReplicas = replicas
	d.Status.UpdatedReplicas = replicas
	d.Status.ObservedGeneration = d.Generation
}
