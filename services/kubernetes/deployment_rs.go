package kubernetes

import (
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// Deployment → ReplicaSet → Pod interposition. Real Deployments don't own Pods
// directly — they own a ReplicaSet per pod-template revision, and the ReplicaSet
// owns the Pods. Materializing the intermediate ReplicaSet makes `kubectl get
// rs` and owner-reference-walking operators behave like a real cluster, and a
// pod-template change becomes a new ReplicaSet (a real rolling update) rather
// than an in-place pod swap. Cascade GC already walks Deployment→RS→Pod, so
// deletion needs no change.

// syncDeploymentReplicaSetLocked reconciles the Deployment's current-revision
// ReplicaSet (creating/updating it and pruning stale revisions), materializes
// its Pods, and returns the live replica count. Callers hold s.mu.
func (s *ClusterState) syncDeploymentReplicaSetLocked(dep *appsv1.Deployment, desired int) int32 {
	st := s.reg.getStore(apiGroupApps, "v1", "replicasets")
	if st == nil {
		// Registry unavailable (should not happen) — fall back to direct ownership.
		return clampInt32(s.syncScaledPods(dep.Namespace, dep.Name, deploymentOwnerRef(dep), dep.Spec.Template, desired))
	}

	rsName := dep.Name + "-" + podTemplateHash(dep.Spec.Template)

	s.pruneStaleDeploymentRSLocked(st, dep, rsName)

	rs := s.upsertDeploymentRSLocked(st, dep, rsName, desired)
	if rs == nil {
		return 0
	}

	reconcileReplicaSet(s, rs)
	s.stampRegistryRVLocked(rs)
	st.watch.publish(EventModified, rs.GetNamespace(), *rs.DeepCopy())

	ready, _, _ := unstructured.NestedInt64(rs.Object, "status", "replicas")

	return clampInt32(int(ready))
}

// upsertDeploymentRSLocked creates or updates the current-revision ReplicaSet,
// returning the stored object (or nil if it couldn't be built).
func (s *ClusterState) upsertDeploymentRSLocked(
	st *registryStore, dep *appsv1.Deployment, rsName string, desired int,
) *unstructured.Unstructured {
	key := objKey(dep.Namespace, rsName)

	if existing, ok := st.items[key]; ok {
		_ = unstructured.SetNestedField(existing.Object, int64(desired), "spec", "replicas")

		return existing
	}

	rs, err := buildDeploymentRSObject(dep, rsName, desired)
	if err != nil {
		return nil
	}

	rs.SetUID(types.UID(newUID()))
	rs.SetCreationTimestamp(s.now())
	st.items[key] = rs
	st.watch.publish(EventAdded, rs.GetNamespace(), *rs.DeepCopy())

	return rs
}

// pruneStaleDeploymentRSLocked deletes ReplicaSets owned by dep whose name isn't
// the current revision — a rolling update retires the old revision's Pods via
// the normal owner cascade.
func (s *ClusterState) pruneStaleDeploymentRSLocked(st *registryStore, dep *appsv1.Deployment, keepName string) {
	for key, rs := range st.items {
		if rs.GetNamespace() != dep.Namespace || rs.GetName() == keepName {
			continue
		}

		if !ownedBy(rs.GetOwnerReferences(), dep.UID) {
			continue
		}

		s.stampRegistryRVLocked(rs)
		delete(st.items, key)
		s.garbageCollectLocked(rs.GetUID())
		st.watch.publish(EventDeleted, rs.GetNamespace(), *rs.DeepCopy())
	}
}

// buildDeploymentRSObject renders a ReplicaSet unstructured from a Deployment's
// template + selector, owned (controller) by the Deployment.
func buildDeploymentRSObject(dep *appsv1.Deployment, rsName string, desired int) (*unstructured.Unstructured, error) {
	tmpl, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&dep.Spec.Template)
	if err != nil {
		return nil, err
	}

	var selector map[string]any
	if dep.Spec.Selector != nil {
		selector, _ = runtime.DefaultUnstructuredConverter.ToUnstructured(dep.Spec.Selector)
	}

	rs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "ReplicaSet",
		"metadata": map[string]any{
			"name":      rsName,
			"namespace": dep.Namespace,
			"labels":    map[string]any{podTemplateHashLabel: podTemplateHash(dep.Spec.Template)},
		},
		"spec": map[string]any{
			"replicas": int64(desired),
			"selector": selector,
			"template": tmpl,
		},
	}}
	rs.SetOwnerReferences([]metav1.OwnerReference{deploymentOwnerRef(dep)})

	return rs, nil
}

func deploymentOwnerRef(dep *appsv1.Deployment) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: "apps/v1", Kind: "Deployment", Name: dep.Name, UID: dep.UID,
		Controller: boolPtr(true), BlockOwnerDeletion: boolPtr(true),
	}
}

// clampInt32 narrows a reconciled count (already bounded by maxReconciledPods) to
// int32 for the status fields.
func clampInt32(n int) int32 {
	if n < 0 {
		return 0
	}

	if n > maxReconciledPods {
		return maxReconciledPods
	}

	return int32(n)
}
