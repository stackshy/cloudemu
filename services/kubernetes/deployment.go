package kubernetes

import (
	"net/http"
	"reflect"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// apiGroupApps is the API group Deployments (and other workload controllers)
// live under: /apis/apps/v1/...
const apiGroupApps = "apps"

// resourceDeployments is the plural resource segment for Deployments.
const resourceDeployments = "deployments"

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

		if r.URL.Query().Get("watch") == watchQueryValue {
			s.watchDeployments(w, r, "")

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
		if r.URL.Query().Get("watch") == watchQueryValue {
			s.watchDeployments(w, r, namespace)

			return
		}

		s.listDeployments(w, namespace)
	case http.MethodPost:
		s.createDeployment(w, r, namespace)
	default:
		writeMethodNotAllowed(w, "k8s api: deployments collection: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) watchDeployments(w http.ResponseWriter, r *http.Request, namespace string) {
	sel, fields := parseListSelectors(r)
	serveWatch(s, w, r, s.wDeployments, namespace,
		func() []appsv1.Deployment { return s.collectDeploymentsLocked(namespace) },
		func(d appsv1.Deployment) bool {
			return sel.Matches(labels.Set(d.Labels)) && metaFieldsMatch(d.Name, d.Namespace, fields)
		})
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
	in.Generation = 1

	dep := in
	s.deployments[key] = &dep
	// Reconcile: materialize Running Pods and populate status + Service
	// endpoints so the deployment is actually "up", not just stored.
	s.reconcileDeploymentLocked(&dep)
	s.wDeployments.publish(EventAdded, namespace, *dep.DeepCopy())
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
	in.Generation = generationFor(cur.Generation, &in.Spec, &cur.Spec)

	dep := in
	s.deployments[key] = &dep
	s.reconcileDeploymentLocked(&dep)
	s.wDeployments.publish(EventModified, namespace, *dep.DeepCopy())
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
	patched.Generation = generationFor(cur.Generation, &patched.Spec, &cur.Spec)

	s.deployments[key] = patched
	s.reconcileDeploymentLocked(patched)
	s.wDeployments.publish(EventModified, namespace, *patched.DeepCopy())
	writeJSON(w, http.StatusOK, patched)
}

// generationFor advances metadata.generation only when the spec actually
// changed, matching apiserver semantics (and the registry path) so an
// observedGeneration comparison is meaningful.
func generationFor(cur int64, newSpec, oldSpec *appsv1.DeploymentSpec) int64 {
	if reflect.DeepEqual(newSpec, oldSpec) {
		return cur
	}

	return cur + 1
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
	// Cascade: garbage-collect the Pods this Deployment owns.
	s.garbageCollectLocked(dep.UID)
	s.wDeployments.publish(EventDeleted, namespace, *dep.DeepCopy())
	writeJSON(w, http.StatusOK, dep.DeepCopy())
}

func deploymentKey(namespace, name string) string {
	return namespace + "/" + name
}
