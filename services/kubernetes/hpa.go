package kubernetes

import (
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// defaultHPAMinReplicas mirrors the apiserver default when spec.minReplicas
	// is omitted from a HorizontalPodAutoscaler.
	defaultHPAMinReplicas = 1
	// hpaTargetKindDeployment is the only scaleTargetRef kind actuated today.
	hpaTargetKindDeployment = "Deployment"
	// hpaMetricTypeResource / hpaResourceCPU select the Resource CPU metric,
	// the common HPA case cloudemu samples from the metrics.k8s.io source.
	hpaMetricTypeResource = "Resource"
	hpaResourceCPU        = "cpu"
	// hpaUtilizationPercent turns a usage/request ratio into a percentage, the
	// unit spec.metrics[].resource.target.averageUtilization is expressed in.
	hpaUtilizationPercent = 100
)

// reconcileHPA actuates a HorizontalPodAutoscaler against its scale target.
// When the spec carries a Resource CPU averageUtilization metric, it samples
// the target's Pods from the same metrics.k8s.io source `kubectl top` reads
// and applies the real HPA ratio,
//
//	desiredReplicas = ceil(currentReplicas * currentUtilization / targetUtilization),
//
// clamped into [minReplicas, maxReplicas]. Without a usable metric (no metric
// configured, no matching Pods, or Pods with no CPU request) it falls back to
// clamping the current replica count into bounds — real HPA reports "unknown"
// and holds rather than scaling on missing data. Only Deployment targets are
// actuated; other kinds are left unchanged. Runs under s.mu (called from the
// registry create/update/patch path).
func reconcileHPA(s *ClusterState, obj *unstructured.Unstructured) {
	kind, name := hpaScaleTarget(obj)
	if kind != hpaTargetKindDeployment || name == "" {
		return
	}

	dep, ok := s.deployments[deploymentKey(obj.GetNamespace(), name)]
	if !ok {
		// Target not found: leave status untouched rather than fabricating
		// numbers for a Deployment that doesn't exist.
		return
	}

	minReplicas, maxReplicas := hpaReplicaBounds(obj)
	current := deploymentReplicas(dep)

	desired, utilization, metricDriven := s.hpaComputeDesired(obj, dep, current, minReplicas, maxReplicas)

	if desired != current {
		s.applyHPAScale(dep, desired)
	}

	setHPAStatus(obj, int64(dep.Status.Replicas), int64(desired), s.now())

	if metricDriven {
		setHPACurrentCPUMetric(obj, utilization)
	}
}

// hpaComputeDesired returns the replica count the HPA wants for dep. When a
// Resource CPU metric is configured and the target's Pods yield a utilization
// sample it applies the HPA ratio; otherwise it falls back to a min/max clamp
// of current. metricDriven reports whether a real sample drove the result.
func (s *ClusterState) hpaComputeDesired(
	obj *unstructured.Unstructured, dep *appsv1.Deployment, current, minReplicas, maxReplicas int,
) (desired int, utilization int64, metricDriven bool) {
	target, ok := hpaCPUTargetUtilization(obj)
	if !ok {
		return clampReplicas(current, minReplicas, maxReplicas), 0, false
	}

	pods := s.hpaTargetPodsLocked(dep)

	util, ok := averageCPUUtilization(pods)
	if !ok || len(pods) == 0 {
		return clampReplicas(current, minReplicas, maxReplicas), 0, false
	}

	scaled := scaleFromMetric(len(pods), util, target)

	return clampReplicas(scaled, minReplicas, maxReplicas), util, true
}

// applyHPAScale writes desired onto the Deployment, re-reconciles it (so Pods
// and status converge to the new count) and publishes the update. Callers hold
// s.mu.
func (s *ClusterState) applyHPAScale(dep *appsv1.Deployment, desired int) {
	r := int32(desired) //nolint:gosec // bounded by maxReplicas, a user-supplied HPA spec field.
	dep.Spec.Replicas = &r
	dep.ResourceVersion = s.nextClusterRVLocked()
	s.reconcileDeploymentLocked(dep)
	s.wDeployments.publish(EventModified, dep.Namespace, *dep.DeepCopy())
}

// scaleFromMetric implements the core HPA ratio,
// ceil(currentReplicas * currentUtil / targetUtil), using integer math so the
// result is deterministic. A non-positive target (already filtered upstream)
// leaves the count unchanged rather than dividing by zero.
func scaleFromMetric(currentReplicas int, currentUtil, targetUtil int64) int {
	if targetUtil <= 0 {
		return currentReplicas
	}

	num := int64(currentReplicas)*currentUtil + targetUtil - 1

	return int(num / targetUtil)
}

// averageCPUUtilization returns the aggregate CPU utilization percentage across
// pods — sum(usage)/sum(request)*100, matching how the real HPA computes a
// Resource utilization metric. Usage comes from the metrics.k8s.io source
// (podMetricCPUUsage per container); requests come from each container's
// resources.requests.cpu. Reports ok=false when there are no Pods or none
// declare a CPU request (utilization is then "unknown", as in real HPA).
func averageCPUUtilization(pods []*corev1.Pod) (int64, bool) {
	usagePerContainer, err := resource.ParseQuantity(podMetricCPUUsage)
	if err != nil {
		return 0, false
	}

	var totalUsage, totalRequest int64

	for _, pod := range pods {
		for i := range pod.Spec.Containers {
			c := &pod.Spec.Containers[i]
			totalUsage += usagePerContainer.MilliValue()

			if req, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
				totalRequest += req.MilliValue()
			}
		}
	}

	if totalRequest == 0 {
		return 0, false
	}

	return totalUsage * hpaUtilizationPercent / totalRequest, true
}

// hpaTargetPodsLocked returns the Running Pods that back dep, matched by the
// Deployment's label selector (the set the metrics sample is averaged over).
// Callers hold s.mu.
func (s *ClusterState) hpaTargetPodsLocked(dep *appsv1.Deployment) []*corev1.Pod {
	if dep.Spec.Selector == nil || len(dep.Spec.Selector.MatchLabels) == 0 {
		return nil
	}

	var pods []*corev1.Pod

	for _, pod := range s.pods {
		if pod.Namespace != dep.Namespace || pod.Status.Phase != corev1.PodRunning {
			continue
		}

		if labelsMatch(dep.Spec.Selector.MatchLabels, pod.Labels) {
			pods = append(pods, pod)
		}
	}

	return pods
}

// hpaCPUTargetUtilization reads the target averageUtilization of the first
// Resource/cpu entry in spec.metrics. ok=false means no such metric is
// configured (the min/max-clamp fallback applies).
func hpaCPUTargetUtilization(obj *unstructured.Unstructured) (int64, bool) {
	metrics, found, _ := unstructured.NestedSlice(obj.Object, "spec", "metrics")
	if !found {
		return 0, false
	}

	for _, raw := range metrics {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		if t, _, _ := unstructured.NestedString(m, "type"); t != hpaMetricTypeResource {
			continue
		}

		if n, _, _ := unstructured.NestedString(m, "resource", "name"); n != hpaResourceCPU {
			continue
		}

		if target, ok, _ := unstructured.NestedInt64(m, "resource", "target", "averageUtilization"); ok && target > 0 {
			return target, true
		}
	}

	return 0, false
}

// deploymentReplicas reads the Deployment's spec.replicas, defaulting to 1 when
// unset (matching apiserver defaulting).
func deploymentReplicas(dep *appsv1.Deployment) int {
	if dep.Spec.Replicas != nil {
		return int(*dep.Spec.Replicas)
	}

	return 1
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

// setHPACurrentCPUMetric records the sampled CPU utilization on
// status.currentMetrics, the field the real HPA controller populates so
// `kubectl get hpa` can show the observed vs target percentage.
func setHPACurrentCPUMetric(obj *unstructured.Unstructured, utilization int64) {
	_ = unstructured.SetNestedSlice(obj.Object, []any{
		map[string]any{
			"type": hpaMetricTypeResource,
			"resource": map[string]any{
				"name":    hpaResourceCPU,
				"current": map[string]any{"averageUtilization": utilization},
			},
		},
	}, "status", "currentMetrics")
}
