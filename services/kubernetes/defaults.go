package kubernetes

import (
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// strategyRollingUpdate is the default (and only defaulted) updateStrategy type.
const strategyRollingUpdate = "RollingUpdate"

// Workload updateStrategy defaulting (#874 seam 3). `kubectl rollout status` on
// a StatefulSet/DaemonSet errors ("only available for RollingUpdate strategy
// type") if the strategy type is unset, so — like a real apiserver — the
// reconciler defaults it at write time. Deployments default to RollingUpdate
// 25%/25%, StatefulSets to RollingUpdate partition 0, DaemonSets to
// RollingUpdate maxUnavailable 1 / maxSurge 0. All defaulting is
// only-if-missing, so it is idempotent across re-reconciles.

func defaultDeploymentStrategy(dep *appsv1.Deployment) {
	if dep.Spec.Strategy.Type == "" {
		dep.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
	}

	if dep.Spec.Strategy.Type == appsv1.RollingUpdateDeploymentStrategyType && dep.Spec.Strategy.RollingUpdate == nil {
		maxUnavailable := intstr.FromString("25%")
		maxSurge := intstr.FromString("25%")
		dep.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{
			MaxUnavailable: &maxUnavailable,
			MaxSurge:       &maxSurge,
		}
	}
}

func defaultStatefulSetStrategy(obj *unstructured.Unstructured) {
	t, found, _ := unstructured.NestedString(obj.Object, "spec", "updateStrategy", "type")
	if !found || t == "" {
		_ = unstructured.SetNestedField(obj.Object, strategyRollingUpdate, "spec", "updateStrategy", "type")
		t = strategyRollingUpdate
	}

	if t != strategyRollingUpdate {
		return
	}

	if _, f, _ := unstructured.NestedInt64(obj.Object, "spec", "updateStrategy", "rollingUpdate", "partition"); !f {
		_ = unstructured.SetNestedField(obj.Object, int64(0), "spec", "updateStrategy", "rollingUpdate", "partition")
	}
}

func defaultDaemonSetStrategy(obj *unstructured.Unstructured) {
	t, found, _ := unstructured.NestedString(obj.Object, "spec", "updateStrategy", "type")
	if !found || t == "" {
		_ = unstructured.SetNestedField(obj.Object, strategyRollingUpdate, "spec", "updateStrategy", "type")
		t = strategyRollingUpdate
	}

	if t != strategyRollingUpdate {
		return
	}

	if _, f, _ := unstructured.NestedInt64(obj.Object, "spec", "updateStrategy", "rollingUpdate", "maxUnavailable"); !f {
		_ = unstructured.SetNestedField(obj.Object, int64(1), "spec", "updateStrategy", "rollingUpdate", "maxUnavailable")
	}

	if _, f, _ := unstructured.NestedInt64(obj.Object, "spec", "updateStrategy", "rollingUpdate", "maxSurge"); !f {
		_ = unstructured.SetNestedField(obj.Object, int64(0), "spec", "updateStrategy", "rollingUpdate", "maxSurge")
	}
}
