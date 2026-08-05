package kubernetes_test

import (
	"net/http"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
)

func makeHPA(name, targetName string, minReplicas, maxReplicas int32) map[string]any {
	return map[string]any{
		"apiVersion": "autoscaling/v2",
		"kind":       "HorizontalPodAutoscaler",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"scaleTargetRef": map[string]any{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"name":       targetName,
			},
			"minReplicas": minReplicas,
			"maxReplicas": maxReplicas,
		},
	}
}

func TestHPA_ScalesDeployment(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments",
		mustJSON(t, makeDeployment("web", 1))).Body.Close()

	resp := do(t, http.MethodPost, base+"/apis/autoscaling/v2/namespaces/default/horizontalpodautoscalers",
		mustJSON(t, makeHPA("web-hpa", "web", 3, 5)))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create hpa: got %d, want 201", resp.StatusCode)
	}

	var hpa map[string]any
	mustDecode(t, resp.Body, &hpa)

	status, _ := hpa["status"].(map[string]any)
	if status["desiredReplicas"] != float64(3) {
		t.Fatalf("hpa status.desiredReplicas: got %v, want 3", status["desiredReplicas"])
	}

	if status["currentReplicas"] != float64(3) {
		t.Fatalf("hpa status.currentReplicas: got %v, want 3", status["currentReplicas"])
	}

	if status["lastScaleTime"] == nil || status["lastScaleTime"] == "" {
		t.Fatalf("hpa status.lastScaleTime missing")
	}

	resp = do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/deployments/web", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get deployment: got %d, want 200", resp.StatusCode)
	}

	var dep appsv1.Deployment
	mustDecode(t, resp.Body, &dep)

	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 3 {
		t.Fatalf("deployment spec.replicas: got %v, want 3", dep.Spec.Replicas)
	}

	if dep.Status.Replicas != 3 {
		t.Fatalf("deployment status.replicas: got %d, want 3", dep.Status.Replicas)
	}
}

func TestHPA_CapsAboveMaxReplicas(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments",
		mustJSON(t, makeDeployment("api", 10))).Body.Close()

	do(t, http.MethodPost, base+"/apis/autoscaling/v2/namespaces/default/horizontalpodautoscalers",
		mustJSON(t, makeHPA("api-hpa", "api", 1, 4))).Body.Close()

	resp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/deployments/api", nil)
	var dep appsv1.Deployment
	mustDecode(t, resp.Body, &dep)

	if *dep.Spec.Replicas != 4 {
		t.Fatalf("deployment spec.replicas: got %d, want 4 (capped)", *dep.Spec.Replicas)
	}
}

func TestHPA_LeavesReplicasUnchangedWithinBounds(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments",
		mustJSON(t, makeDeployment("steady", 3))).Body.Close()

	do(t, http.MethodPost, base+"/apis/autoscaling/v2/namespaces/default/horizontalpodautoscalers",
		mustJSON(t, makeHPA("steady-hpa", "steady", 2, 5))).Body.Close()

	resp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/deployments/steady", nil)
	var dep appsv1.Deployment
	mustDecode(t, resp.Body, &dep)

	if *dep.Spec.Replicas != 3 {
		t.Fatalf("deployment spec.replicas: got %d, want 3 (already within bounds)", *dep.Spec.Replicas)
	}
}

func TestHPA_DefaultMinReplicas(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments",
		mustJSON(t, makeDeployment("nomin", 0))).Body.Close()

	// No minReplicas in the spec — the emulator should default it to 1.
	hpa := map[string]any{
		"apiVersion": "autoscaling/v2",
		"kind":       "HorizontalPodAutoscaler",
		"metadata":   map[string]any{"name": "nomin-hpa"},
		"spec": map[string]any{
			"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "nomin"},
			"maxReplicas":    int32(4),
		},
	}

	do(t, http.MethodPost, base+"/apis/autoscaling/v2/namespaces/default/horizontalpodautoscalers",
		mustJSON(t, hpa)).Body.Close()

	resp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/deployments/nomin", nil)
	var dep appsv1.Deployment
	mustDecode(t, resp.Body, &dep)

	if *dep.Spec.Replicas != 1 {
		t.Fatalf("deployment spec.replicas: got %d, want 1 (default minReplicas)", *dep.Spec.Replicas)
	}
}

func TestHPA_TargetNotFoundDoesNotPanic(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodPost, base+"/apis/autoscaling/v2/namespaces/default/horizontalpodautoscalers",
		mustJSON(t, makeHPA("ghost-hpa", "ghost", 2, 4)))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create hpa with missing target: got %d, want 201", resp.StatusCode)
	}

	var hpa map[string]any
	mustDecode(t, resp.Body, &hpa)

	if hpa["status"] != nil {
		if status, ok := hpa["status"].(map[string]any); ok && status["desiredReplicas"] != nil {
			t.Fatalf("status should be untouched for a missing target: got %v", status)
		}
	}
}

func TestHPA_NonDeploymentTargetIsIgnored(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	hpa := map[string]any{
		"apiVersion": "autoscaling/v2",
		"kind":       "HorizontalPodAutoscaler",
		"metadata":   map[string]any{"name": "sts-hpa"},
		"spec": map[string]any{
			"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "StatefulSet", "name": "db"},
			"minReplicas":    int32(2),
			"maxReplicas":    int32(4),
		},
	}

	resp := do(t, http.MethodPost, base+"/apis/autoscaling/v2/namespaces/default/horizontalpodautoscalers",
		mustJSON(t, hpa))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create hpa: got %d, want 201", resp.StatusCode)
	}
}

// makeDeploymentWithCPURequest builds a Deployment whose single container
// declares a CPU request, so metric-driven HPA has a denominator to compute a
// utilization percentage against (usage is the fixed metrics.k8s.io sample).
func makeDeploymentWithCPURequest(name string, replicas int32, cpuRequest string) map[string]any {
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"replicas": replicas,
			"selector": map[string]any{"matchLabels": map[string]any{"app": name}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": name}},
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":      "main",
							"image":     "nginx:1.27",
							"resources": map[string]any{"requests": map[string]any{"cpu": cpuRequest}},
						},
					},
				},
			},
		},
	}
}

// makeCPUHPA builds an autoscaling/v2 HPA targeting a Deployment on a Resource
// CPU averageUtilization metric.
func makeCPUHPA(name, targetName string, minReplicas, maxReplicas, targetUtil int32) map[string]any {
	return map[string]any{
		"apiVersion": "autoscaling/v2",
		"kind":       "HorizontalPodAutoscaler",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": targetName},
			"minReplicas":    minReplicas,
			"maxReplicas":    maxReplicas,
			"metrics": []any{
				map[string]any{
					"type": "Resource",
					"resource": map[string]any{
						"name":   "cpu",
						"target": map[string]any{"type": "Utilization", "averageUtilization": targetUtil},
					},
				},
			},
		},
	}
}

func getDeploymentReplicas(t *testing.T, base, name string) int32 {
	t.Helper()

	resp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/deployments/"+name, nil)

	var dep appsv1.Deployment
	mustDecode(t, resp.Body, &dep)

	if dep.Spec.Replicas == nil {
		t.Fatalf("deployment %s: spec.replicas is nil", name)
	}

	return *dep.Spec.Replicas
}

// Fixed metrics.k8s.io sample is 250m CPU per container. With a 100m request
// the utilization is 250%, well above a 50% target, so an HPA scales up toward
// ceil(N * 250/50) and is capped at maxReplicas.
func TestHPA_MetricScalesUpUnderLoad(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments",
		mustJSON(t, makeDeploymentWithCPURequest("hot", 2, "100m"))).Body.Close()

	resp := do(t, http.MethodPost, base+"/apis/autoscaling/v2/namespaces/default/horizontalpodautoscalers",
		mustJSON(t, makeCPUHPA("hot-hpa", "hot", 1, 6, 50)))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create hpa: got %d, want 201", resp.StatusCode)
	}

	var hpa map[string]any
	mustDecode(t, resp.Body, &hpa)

	// ceil(2 * 250/50) = 10, capped at maxReplicas=6.
	if got := getDeploymentReplicas(t, base, "hot"); got != 6 {
		t.Fatalf("deployment spec.replicas: got %d, want 6 (scaled up, capped at max)", got)
	}

	status, _ := hpa["status"].(map[string]any)
	if status["desiredReplicas"] != float64(6) {
		t.Fatalf("hpa status.desiredReplicas: got %v, want 6", status["desiredReplicas"])
	}

	metrics, ok := status["currentMetrics"].([]any)
	if !ok || len(metrics) == 0 {
		t.Fatalf("hpa status.currentMetrics missing: %v", status["currentMetrics"])
	}
}

// A 1000m request against the fixed 250m sample is 25% utilization, below the
// 50% target, so the HPA scales down toward ceil(N * 25/50) but never past
// minReplicas.
func TestHPA_MetricScalesDownAndFloorsAtMin(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments",
		mustJSON(t, makeDeploymentWithCPURequest("cold", 4, "1000m"))).Body.Close()

	do(t, http.MethodPost, base+"/apis/autoscaling/v2/namespaces/default/horizontalpodautoscalers",
		mustJSON(t, makeCPUHPA("cold-hpa", "cold", 3, 6, 50))).Body.Close()

	// ceil(4 * 25/50) = 2, floored at minReplicas=3.
	if got := getDeploymentReplicas(t, base, "cold"); got != 3 {
		t.Fatalf("deployment spec.replicas: got %d, want 3 (scaled down, floored at min)", got)
	}
}

// A 500m request against the fixed 250m sample is exactly the 50% target, so
// ceil(N * 50/50) = N leaves replicas unchanged.
func TestHPA_MetricAtTargetLeavesReplicasUnchanged(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments",
		mustJSON(t, makeDeploymentWithCPURequest("even", 3, "500m"))).Body.Close()

	do(t, http.MethodPost, base+"/apis/autoscaling/v2/namespaces/default/horizontalpodautoscalers",
		mustJSON(t, makeCPUHPA("even-hpa", "even", 1, 6, 50))).Body.Close()

	if got := getDeploymentReplicas(t, base, "even"); got != 3 {
		t.Fatalf("deployment spec.replicas: got %d, want 3 (at target, unchanged)", got)
	}
}

// With a CPU metric configured but no CPU request on the Pods, utilization is
// unknown — the HPA must not scale on missing data and must fall back to the
// min/max clamp (here capping 8 replicas at max=5), without panicking.
func TestHPA_MetricMissingFallsBackToClamp(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// makeDeployment's container declares no resources.requests.cpu.
	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments",
		mustJSON(t, makeDeployment("norq", 8))).Body.Close()

	resp := do(t, http.MethodPost, base+"/apis/autoscaling/v2/namespaces/default/horizontalpodautoscalers",
		mustJSON(t, makeCPUHPA("norq-hpa", "norq", 1, 5, 50)))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create hpa: got %d, want 201", resp.StatusCode)
	}

	resp.Body.Close()

	if got := getDeploymentReplicas(t, base, "norq"); got != 5 {
		t.Fatalf("deployment spec.replicas: got %d, want 5 (fallback clamp to max)", got)
	}
}
