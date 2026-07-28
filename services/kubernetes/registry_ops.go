package kubernetes

import (
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// garbageCollectLocked deletes every object controlled by owner — the
// background-propagation cascade a real apiserver's garbage collector performs
// when an owner is deleted. It recurses (Deployment -> ReplicaSet -> Pods) and
// covers both registry-backed children and the typed Pod store. Callers hold
// s.mu.
func (s *ClusterState) garbageCollectLocked(owner types.UID) {
	for _, st := range s.reg.stores {
		for key, obj := range st.items {
			if !ownedBy(obj.GetOwnerReferences(), owner) {
				continue
			}

			childUID := obj.GetUID()
			delete(st.items, key)
			st.bumpRVLocked()
			st.watch.publish(EventDeleted, obj.GetNamespace(), *obj.DeepCopy())
			s.garbageCollectLocked(childUID)
		}
	}

	touched := map[string]bool{}

	for key, pod := range s.pods {
		if ownedBy(pod.OwnerReferences, owner) {
			delete(s.pods, key)
			touched[pod.Namespace] = true
			s.wPods.publish(EventDeleted, pod.Namespace, *pod.DeepCopy())
		}
	}

	// The endpoints controller must drop the addresses of the Pods we just
	// garbage-collected, or a Service keeps pointing at gone Pods.
	for ns := range touched {
		s.resyncEndpointsForNamespaceLocked(ns)
	}
}

func ownedBy(refs []metav1.OwnerReference, owner types.UID) bool {
	for _, ref := range refs {
		if ref.UID == owner {
			return true
		}
	}

	return false
}

// registrySubresource serves the /status and /scale subresources for a
// registry-backed kind that declares them.
func (s *ClusterState) registrySubresource(w http.ResponseWriter, r *http.Request, route *Route, st *registryStore) {
	switch route.Subresource {
	case "status":
		if !st.def.hasStatus {
			writeNotFound(w, "k8s api: "+st.def.kind+" has no status subresource")

			return
		}

		s.registryStatus(w, r, st, route.Namespace, route.Name)
	case "scale":
		if !st.def.hasScale {
			writeNotFound(w, "k8s api: "+st.def.kind+" has no scale subresource")

			return
		}

		s.registryScale(w, r, st, route.Namespace, route.Name)
	default:
		writeNotFound(w, "k8s api: subresource not implemented: "+route.Subresource)
	}
}

func (s *ClusterState) registryStatus(w http.ResponseWriter, r *http.Request, st *registryStore, namespace, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := st.items[objKey(namespace, name)]
	if !ok {
		writeNotFound(w, "k8s api: "+st.def.kind+" not found: "+objKey(namespace, name))

		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, cur.DeepCopy())
	case http.MethodPut:
		in := &unstructured.Unstructured{}
		if !readJSON(w, r, in) {
			return
		}
		// Only the status stanza is persisted through /status.
		status, _, _ := unstructured.NestedFieldCopy(in.Object, "status")
		_ = unstructured.SetNestedField(cur.Object, status, "status")
		st.stampRVLocked(cur)
		st.watch.publish(EventModified, cur.GetNamespace(), *cur.DeepCopy())
		writeJSON(w, http.StatusOK, cur.DeepCopy())
	case http.MethodPatch:
		patched, pok := s.applyUnstructuredPatch(w, r, cur)
		if !pok {
			return
		}
		// Keep spec/metadata from cur; only status moves through this endpoint.
		status, _, _ := unstructured.NestedFieldCopy(patched.Object, "status")
		_ = unstructured.SetNestedField(cur.Object, status, "status")
		st.stampRVLocked(cur)
		st.watch.publish(EventModified, cur.GetNamespace(), *cur.DeepCopy())
		writeJSON(w, http.StatusOK, cur.DeepCopy())
	default:
		writeMethodNotAllowed(w, "k8s api: status subresource: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) registryScale(w http.ResponseWriter, r *http.Request, st *registryStore, namespace, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := st.items[objKey(namespace, name)]
	if !ok {
		writeNotFound(w, "k8s api: "+st.def.kind+" not found: "+objKey(namespace, name))

		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, scaleFor(cur))
	case http.MethodPut, http.MethodPatch:
		replicas, perr := s.scaleReplicasFromRequest(w, r, cur)
		if perr {
			return
		}

		_ = unstructured.SetNestedField(cur.Object, replicas, "spec", "replicas")
		cur.SetGeneration(cur.GetGeneration() + 1)
		st.stampRVLocked(cur)

		if st.def.reconcile != nil {
			st.def.reconcile(s, cur)
		}

		st.watch.publish(EventModified, cur.GetNamespace(), *cur.DeepCopy())
		writeJSON(w, http.StatusOK, scaleFor(cur))
	default:
		writeMethodNotAllowed(w, "k8s api: scale subresource: method not allowed: "+r.Method)
	}
}

// scaleFor builds the autoscaling/v1 Scale representation of obj from its
// spec.replicas — the shape kubectl scale and HPA read/write.
func scaleFor(obj *unstructured.Unstructured) *unstructured.Unstructured {
	replicas, _, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas")

	scale := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "autoscaling/v1",
		"kind":       "Scale",
		"metadata": map[string]any{
			"name":              obj.GetName(),
			"namespace":         obj.GetNamespace(),
			"uid":               string(obj.GetUID()),
			"resourceVersion":   obj.GetResourceVersion(),
			"creationTimestamp": nil,
		},
		"spec":   map[string]any{"replicas": replicas},
		"status": map[string]any{"replicas": replicas},
	}}

	return scale
}

// scaleReplicasFromRequest reads the desired replica count from a PUT (a full
// Scale object) or a PATCH (merged onto the current Scale). The bool return is
// true when a wire error was already written and the caller must early-return.
func (s *ClusterState) scaleReplicasFromRequest(w http.ResponseWriter, r *http.Request, cur *unstructured.Unstructured) (int64, bool) {
	if r.Method == http.MethodPut {
		in := &unstructured.Unstructured{}
		if !readJSON(w, r, in) {
			return 0, true
		}

		replicas, _, _ := unstructured.NestedInt64(in.Object, "spec", "replicas")

		return replicas, false
	}

	// PATCH: merge the body onto the current Scale, then read replicas.
	merged, ok := s.applyUnstructuredPatch(w, r, scaleFor(cur))
	if !ok {
		return 0, true
	}

	replicas, _, _ := unstructured.NestedInt64(merged.Object, "spec", "replicas")

	return replicas, false
}
