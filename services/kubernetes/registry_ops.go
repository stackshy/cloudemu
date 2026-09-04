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
	// Walk the ownership tree breadth-first. Deleting the current key from a map
	// mid-range is legal in Go, but a naive recursive descent would re-range the
	// same stores while an outer range is still live and make the result depend
	// on Go's randomized iteration order; the explicit BFS queue makes the
	// cascade order-independent. A registry child may own further registry
	// children, so intermediate owners are enqueued as they're found.
	// owners accumulates every UID whose direct children must be reaped —
	// `owner` plus each collected registry object — so Pods owned by an
	// intermediate controller (not just the root) are garbage-collected too.
	owners := map[types.UID]bool{owner: true}
	queue := []types.UID{owner}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		for _, st := range s.reg.stores {
			for key, obj := range st.items {
				if !ownedBy(obj.GetOwnerReferences(), cur) {
					continue
				}

				// A child carrying finalizers goes Terminating (like a normal
				// finalizer-gated delete), not hard-reaped. It keeps owning its own
				// children until drained, so it is not enqueued as a deleted owner.
				if s.markForDeletionUnstructured(obj) {
					s.stampRegistryRVLocked(obj)
					st.watch.publish(EventModified, obj.GetNamespace(), *obj.DeepCopy())

					continue
				}

				uid := obj.GetUID()
				owners[uid] = true

				queue = append(queue, uid)

				s.stampRegistryRVLocked(obj)
				delete(st.items, key)
				st.watch.publish(EventDeleted, obj.GetNamespace(), *obj.DeepCopy())
			}
		}
	}

	touched := map[string]bool{}

	for key, pod := range s.pods {
		if !ownedByAny(pod.OwnerReferences, owners) {
			continue
		}

		// Same finalizer gating for owned Pods: a Pod with finalizers is marked
		// Terminating and left in place until its finalizers drain.
		if s.markForDeletion(&pod.ObjectMeta) {
			pod.ResourceVersion = s.nextClusterRVLocked()
			s.wPods.publish(EventModified, pod.Namespace, *pod.DeepCopy())

			continue
		}

		delete(s.pods, key)

		touched[pod.Namespace] = true

		s.wPods.publish(EventDeleted, pod.Namespace, *pod.DeepCopy())
	}

	// The endpoints controller must drop the addresses of the Pods we just
	// garbage-collected, or a Service keeps pointing at gone Pods; and the Pod
	// quota's status.used must fall back to the live count in each namespace we
	// reaped from.
	for ns := range touched {
		s.resyncEndpointsForNamespaceLocked(ns)
		s.releaseQuotaLocked(ns, "Pod", resourcePods)
	}
}

// reapRegistryObjectLocked deletes one registry object with the same
// finalizer-gating and teardown a direct DELETE performs (registryDelete): a
// finalizer-bearing object goes Terminating (MODIFIED event), otherwise it is
// torn down (owned-child cascade + quota release) and a DELETED event is
// published. Used by the StatefulSet whenScaled=Delete PVC reap, where the
// namespace lives on so quota must stay accurate. Callers hold s.mu.
func (s *ClusterState) reapRegistryObjectLocked(store *registryStore, key string, obj *unstructured.Unstructured) {
	if s.markForDeletionUnstructured(obj) {
		s.stampRegistryRVLocked(obj)
		store.watch.publish(EventModified, obj.GetNamespace(), *obj.DeepCopy())

		return
	}

	s.stampRegistryRVLocked(obj)
	s.teardownRegistryObjectLocked(store, key, obj)
	store.watch.publish(EventDeleted, obj.GetNamespace(), *obj.DeepCopy())
}

func ownedBy(refs []metav1.OwnerReference, owner types.UID) bool {
	for _, ref := range refs {
		if ref.UID == owner {
			return true
		}
	}

	return false
}

func ownedByAny(refs []metav1.OwnerReference, owners map[types.UID]bool) bool {
	for _, ref := range refs {
		if owners[ref.UID] {
			return true
		}
	}

	return false
}

// registrySubresource serves the /status and /scale subresources for a
// registry-backed kind that declares them.
func (s *ClusterState) registrySubresource(w http.ResponseWriter, r *http.Request, route *Route, st *registryStore) {
	switch route.Subresource {
	case subresourceStatus:
		if !st.def.hasStatus {
			writeNotFound(w, "k8s api: "+st.def.kind+" has no status subresource")

			return
		}

		s.registryStatus(w, r, st, route.Namespace, route.Name)
	case subresourceScale:
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
		s.stampRegistryRVLocked(cur)
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
		s.stampRegistryRVLocked(cur)
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

		prev, _, _ := unstructured.NestedInt64(cur.Object, "spec", "replicas")
		_ = unstructured.SetNestedField(cur.Object, replicas, "spec", "replicas")
		// Only bump generation on an actual spec change, matching registryUpdate/
		// registryPatch — an idempotent scale to the current count must not make
		// a controller see a spurious generation != observedGeneration.
		if replicas != prev {
			cur.SetGeneration(cur.GetGeneration() + 1)
		}

		s.stampRegistryRVLocked(cur)

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
