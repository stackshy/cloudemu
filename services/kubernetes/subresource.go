package kubernetes

import (
	"encoding/json"
	"io"
	"net/http"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// serveSubresource dispatches subresource requests for the typed handlers
// (registry-backed kinds serve their own subresources in serveRegistry). Only
// the typed Deployment exposes /scale and /status; anything else is a 404,
// matching a real apiserver's response for a nonexistent subresource.
func (s *ClusterState) serveSubresource(w http.ResponseWriter, r *http.Request, route *Route) {
	if route.APIGroup == apiGroupApps && route.Resource == resourceDeployments {
		switch route.Subresource {
		case subresourceScale:
			s.deploymentScale(w, r, route.Namespace, route.Name)

			return
		case subresourceStatus:
			s.deploymentStatus(w, r, route.Namespace, route.Name)

			return
		}
	}

	writeNotFound(w, "k8s api: subresource not implemented: "+route.Resource+"/"+route.Name+"/"+route.Subresource)
}

func (s *ClusterState) deploymentScale(w http.ResponseWriter, r *http.Request, namespace, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dep, ok := s.deployments[deploymentKey(namespace, name)]
	if !ok {
		writeNotFound(w, "k8s api: deployment not found: "+deploymentKey(namespace, name))

		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, deploymentScaleObject(dep))
	case http.MethodPut, http.MethodPatch:
		replicas, done := s.readScaleReplicas(w, r, dep)
		if done {
			return
		}

		dep.Spec.Replicas = &replicas
		dep.ResourceVersion = bumpResourceVersion(dep.ResourceVersion)
		s.reconcileDeploymentLocked(dep)
		s.wDeployments.publish(EventModified, namespace, *dep.DeepCopy())
		writeJSON(w, http.StatusOK, deploymentScaleObject(dep))
	default:
		writeMethodNotAllowed(w, "k8s api: deployments/scale: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) deploymentStatus(w http.ResponseWriter, r *http.Request, namespace, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dep, ok := s.deployments[deploymentKey(namespace, name)]
	if !ok {
		writeNotFound(w, "k8s api: deployment not found: "+deploymentKey(namespace, name))

		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, dep.DeepCopy())
	case http.MethodPut:
		var in appsv1.Deployment
		if !readJSON(w, r, &in) {
			return
		}

		dep.Status = in.Status
		dep.ResourceVersion = bumpResourceVersion(dep.ResourceVersion)
		s.wDeployments.publish(EventModified, namespace, *dep.DeepCopy())
		writeJSON(w, http.StatusOK, dep.DeepCopy())
	case http.MethodPatch:
		patched, pok := applyJSONPatch(w, r, dep)
		if !pok {
			return
		}
		// Only the status stanza is persisted through /status.
		dep.Status = patched.Status
		dep.ResourceVersion = bumpResourceVersion(dep.ResourceVersion)
		s.wDeployments.publish(EventModified, namespace, *dep.DeepCopy())
		writeJSON(w, http.StatusOK, dep.DeepCopy())
	default:
		writeMethodNotAllowed(w, "k8s api: deployments/status: method not allowed: "+r.Method)
	}
}

// deploymentScaleObject projects a Deployment onto the autoscaling/v1 Scale
// shape kubectl scale and the HPA read/write.
func deploymentScaleObject(dep *appsv1.Deployment) *autoscalingv1.Scale {
	var replicas int32 = 1
	if dep.Spec.Replicas != nil {
		replicas = *dep.Spec.Replicas
	}

	return &autoscalingv1.Scale{
		TypeMeta: metav1.TypeMeta{Kind: "Scale", APIVersion: "autoscaling/v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            dep.Name,
			Namespace:       dep.Namespace,
			UID:             dep.UID,
			ResourceVersion: dep.ResourceVersion,
		},
		Spec:   autoscalingv1.ScaleSpec{Replicas: replicas},
		Status: autoscalingv1.ScaleStatus{Replicas: dep.Status.Replicas},
	}
}

// readScaleReplicas parses the desired replica count from a PUT (full Scale) or
// a PATCH (merged onto the current Scale). The bool is true when a wire error
// was already written.
func (*ClusterState) readScaleReplicas(w http.ResponseWriter, r *http.Request, dep *appsv1.Deployment) (int32, bool) {
	if r.Method == http.MethodPut {
		var in autoscalingv1.Scale
		if !readJSON(w, r, &in) {
			return 0, true
		}

		return in.Spec.Replicas, false
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeBadRequest(w, "k8s api: read scale patch: "+err.Error())

		return 0, true
	}

	cur, err := json.Marshal(deploymentScaleObject(dep))
	if err != nil {
		writeBadRequest(w, "k8s api: marshal scale: "+err.Error())

		return 0, true
	}

	merged, err := mergePatch(cur, body)
	if err != nil {
		writeBadRequest(w, "k8s api: apply scale patch: "+err.Error())

		return 0, true
	}

	var scale autoscalingv1.Scale
	if err := json.Unmarshal(merged, &scale); err != nil {
		writeBadRequest(w, "k8s api: decode patched scale: "+err.Error())

		return 0, true
	}

	return scale.Spec.Replicas, false
}
