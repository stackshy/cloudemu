package kubernetes_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

func TestNetworkPolicy_DefaultAllowAndSelectiveDeny(t *testing.T) {
	api := kubernetes.NewAPIServer()
	uid, state := api.RegisterCluster()
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)
	api.SetBaseURL(ts.URL)

	base := ts.URL + "/k8s/" + uid

	srcLabels := map[string]string{"role": "other"}
	dstLabels := map[string]string{"app": "web"}

	// No NetworkPolicy exists yet: default allow.
	if !state.EvaluateNetworkPolicy("default", srcLabels, dstLabels, 80, "TCP") {
		t.Error("expected default allow with no NetworkPolicy")
	}

	policyBody := mustJSON(t, map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata":   map[string]any{"name": "web-policy"},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
			"ingress": []map[string]any{
				{"from": []map[string]any{
					{"podSelector": map[string]any{"matchLabels": map[string]any{"role": "allowed"}}},
				}},
			},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/networking.k8s.io/v1/namespaces/default/networkpolicies", policyBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create NetworkPolicy: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// A policy now selects the destination; a source that doesn't match the
	// ingress rule's "from" selector must be denied.
	if state.EvaluateNetworkPolicy("default", srcLabels, dstLabels, 80, "TCP") {
		t.Error("expected deny: src labels don't match the policy's from selector")
	}

	// A source that does match the "from" selector is allowed.
	allowedSrc := map[string]string{"role": "allowed"}
	if !state.EvaluateNetworkPolicy("default", allowedSrc, dstLabels, 80, "TCP") {
		t.Error("expected allow: src labels match the policy's from selector")
	}

	// A pod not selected by any policy's podSelector is unaffected (default
	// allow still applies to it).
	unselectedDst := map[string]string{"app": "other"}
	if !state.EvaluateNetworkPolicy("default", srcLabels, unselectedDst, 80, "TCP") {
		t.Error("expected default allow for a pod not selected by any NetworkPolicy")
	}
}

func TestNetworkPolicy_NamespaceSelectorPeer(t *testing.T) {
	api := kubernetes.NewAPIServer()
	uid, state := api.RegisterCluster()
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)
	api.SetBaseURL(ts.URL)

	base := ts.URL + "/k8s/" + uid

	policyBody := mustJSON(t, map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata":   map[string]any{"name": "ns-and-pod-policy"},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
			"ingress": []map[string]any{
				// Peer 1: namespaceSelector only (empty selector = all namespaces),
				// so it matches regardless of pod labels.
				{"from": []map[string]any{{"namespaceSelector": map[string]any{}}}},
			},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/networking.k8s.io/v1/namespaces/default/networkpolicies", policyBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create NetworkPolicy: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	dstLabels := map[string]string{"app": "web"}
	anySrc := map[string]string{"anything": "goes"}

	if !state.EvaluateNetworkPolicy("default", anySrc, dstLabels, 80, "TCP") {
		t.Error("expected allow: empty namespaceSelector peer matches every namespace")
	}
}

func TestNetworkPolicy_PodAndNamespaceSelectorPeer(t *testing.T) {
	api := kubernetes.NewAPIServer()
	uid, state := api.RegisterCluster()
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)
	api.SetBaseURL(ts.URL)

	base := ts.URL + "/k8s/" + uid

	policyBody := mustJSON(t, map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata":   map[string]any{"name": "ns-pod-combo-policy"},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
			"ingress": []map[string]any{
				{"from": []map[string]any{
					{
						"podSelector":       map[string]any{"matchLabels": map[string]any{"role": "allowed"}},
						"namespaceSelector": map[string]any{},
					},
				}},
			},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/networking.k8s.io/v1/namespaces/default/networkpolicies", policyBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create NetworkPolicy: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	dstLabels := map[string]string{"app": "web"}

	if state.EvaluateNetworkPolicy("default", map[string]string{"role": "other"}, dstLabels, 80, "TCP") {
		t.Error("expected deny: src pod labels don't satisfy the combined peer's podSelector")
	}

	if !state.EvaluateNetworkPolicy("default", map[string]string{"role": "allowed"}, dstLabels, 80, "TCP") {
		t.Error("expected allow: src pod labels satisfy the combined peer's podSelector")
	}
}

func TestNetworkPolicy_PortRestriction(t *testing.T) {
	api := kubernetes.NewAPIServer()
	uid, state := api.RegisterCluster()
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)
	api.SetBaseURL(ts.URL)

	base := ts.URL + "/k8s/" + uid

	policyBody := mustJSON(t, map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata":   map[string]any{"name": "port-policy"},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
			"ingress": []map[string]any{
				{"ports": []map[string]any{{"protocol": "TCP", "port": 80}}},
			},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/networking.k8s.io/v1/namespaces/default/networkpolicies", policyBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create NetworkPolicy: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	dstLabels := map[string]string{"app": "web"}

	if !state.EvaluateNetworkPolicy("default", nil, dstLabels, 80, "TCP") {
		t.Error("expected allow on the permitted port")
	}

	if state.EvaluateNetworkPolicy("default", nil, dstLabels, 443, "TCP") {
		t.Error("expected deny on a port the rule doesn't list")
	}
}
