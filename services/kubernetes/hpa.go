package kubernetes

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// defaultHPAMinReplicas mirrors the apiserver default when spec.minReplicas
// is omitted from a HorizontalPodAutoscaler.
const defaultHPAMinReplicas = 1

// reconcileHPA actuates a HorizontalPodAutoscaler against its scale target.
// cloudemu has no real metrics pipeline, so there is nothing to sample; the
// deterministic behavior implemented here is scale-to-bounds: the target
// Deployment's current replica count is clamped into [minReplicas,
// maxReplicas] and written back, so an HPA still visibly does something (an
// under-provisioned Deployment gets scaled up to the floor, an
// over-provisioned one gets capped) rather than being a inert stored object.
// Runs under s.mu (called from the registry create/update/patch path).
func reconcileHPA(s *ClusterState, obj *unstructured.Unstructured) {
	kind, name := hpaScaleTarget(obj)
	if kind != "Deployment" || name == "" {
		return
	}

	dep, ok := s.deployments[deploymentKey(obj.GetNamespace(), name)]
	if !ok {
		// Target not found: leave status untouched rather than fabricating
		// numbers for a Deployment that doesn't exist.
		return
	}

	minReplicas, maxReplicas := hpaReplicaBounds(obj)

	current := 1
	if dep.Spec.Replicas != nil {
		current = int(*dep.Spec.Replicas)
	}

	desired := clampReplicas(current, minReplicas, maxReplicas)

	if desired != current {
		r := int32(desired) //nolint:gosec // bounded by maxReplicas, a user-supplied HPA spec field.
		dep.Spec.Replicas = &r
		dep.ResourceVersion = bumpResourceVersion(dep.ResourceVersion)
		s.reconcileDeploymentLocked(dep)
		s.wDeployments.publish(EventModified, dep.Namespace, *dep.DeepCopy())
	}

	setHPAStatus(obj, int64(dep.Status.Replicas), int64(desired), s.now())
}

// hpaScaleTarget reads spec.scaleTargetRef.{kind,name}.
func hpaScaleTarget(obj *unstructured.Unstructured) (kind, name string) {
	kind, _, _ = unstructured.NestedString(obj.Object, "spec", "scaleTargetRef", "kind")
	name, _, _ = unstructured.NestedString(obj.Object, "spec", "scaleTargetRef", "name")

	return kind, name
}

// hpaReplicaBounds reads spec.minReplicas (default defaultHPAMinReplicas) and
// spec.maxReplicas, clamping an inverted range (max < min) up to min so
// callers always get a valid [min, max] window.
func hpaReplicaBounds(obj *unstructured.Unstructured) (minReplicas, maxReplicas int) {
	minReplicas = defaultHPAMinReplicas
	if v, found, _ := unstructured.NestedInt64(obj.Object, "spec", "minReplicas"); found {
		minReplicas = int(v)
	}

	maxReplicas = minReplicas
	if v, found, _ := unstructured.NestedInt64(obj.Object, "spec", "maxReplicas"); found {
		maxReplicas = int(v)
	}

	if maxReplicas < minReplicas {
		maxReplicas = minReplicas
	}

	return minReplicas, maxReplicas
}

func clampReplicas(n, minReplicas, maxReplicas int) int {
	switch {
	case n < minReplicas:
		return minReplicas
	case n > maxReplicas:
		return maxReplicas
	default:
		return n
	}
}

// setHPAStatus mirrors the current/desired replica counts and a
// lastScaleTime onto the HPA's status, matching what a real
// horizontal-pod-autoscaler controller reports.
func setHPAStatus(obj *unstructured.Unstructured, currentReplicas, desiredReplicas int64, now metav1.Time) {
	_ = unstructured.SetNestedField(obj.Object, currentReplicas, "status", "currentReplicas")
	_ = unstructured.SetNestedField(obj.Object, desiredReplicas, "status", "desiredReplicas")
	_ = unstructured.SetNestedField(obj.Object, now.UTC().Format(time.RFC3339), "status", "lastScaleTime")
}
