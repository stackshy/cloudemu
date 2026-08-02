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
