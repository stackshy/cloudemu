package kubernetes_test

import (
	"net/http"
	"testing"
)

// sarAllowed posts a SubjectAccessReview for user against verb/resource in
// namespace and returns status.allowed.
func sarAllowed(t *testing.T, base, user, verb, resource, namespace string) bool {
	t.Helper()

	body := mustJSON(t, map[string]any{
		"apiVersion": "authorization.k8s.io/v1",
		"kind":       "SubjectAccessReview",
		"spec": map[string]any{
			"user": user,
			"resourceAttributes": map[string]any{
				"verb":      verb,
				"resource":  resource,
				"namespace": namespace,
			},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/authorization.k8s.io/v1/subjectaccessreviews", body)

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("SubjectAccessReview: status %d", resp.StatusCode)
	}

	m := decodeMap(t, resp.Body)

	status, _ := m["status"].(map[string]any)
	allowed, _ := status["allowed"].(bool)

	return allowed
}

func TestSubjectAccessReview_AllowAndDeny(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	roleBody := mustJSON(t, map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "Role",
		"metadata":   map[string]any{"name": "pod-reader"},
		"rules": []map[string]any{
			{"apiGroups": []string{""}, "resources": []string{"pods"}, "verbs": []string{"get"}},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/rbac.authorization.k8s.io/v1/namespaces/default/roles", roleBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create Role: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	bindingBody := mustJSON(t, map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "RoleBinding",
		"metadata":   map[string]any{"name": "alice-pod-reader"},
		"subjects": []map[string]any{
			{"kind": "User", "name": "alice", "apiGroup": "rbac.authorization.k8s.io"},
		},
		"roleRef": map[string]any{
			"kind": "Role", "name": "pod-reader", "apiGroup": "rbac.authorization.k8s.io",
		},
	})

	resp = do(t, http.MethodPost, base+"/apis/rbac.authorization.k8s.io/v1/namespaces/default/rolebindings", bindingBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create RoleBinding: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	if !sarAllowed(t, base, "alice", "get", "pods", "default") {
		t.Error("alice: expected get pods to be allowed")
	}

	if sarAllowed(t, base, "bob", "get", "pods", "default") {
		t.Error("bob: expected get pods to be denied (no binding)")
	}

	if sarAllowed(t, base, "alice", "delete", "pods", "default") {
		t.Error("alice: expected delete pods to be denied (role only grants get)")
	}
}

func TestSubjectAccessReview_ClusterRoleBindingGroupSubject(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	crBody := mustJSON(t, map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRole",
		"metadata":   map[string]any{"name": "node-viewer"},
		"rules": []map[string]any{
			{"apiGroups": []string{""}, "resources": []string{"nodes"}, "verbs": []string{"list"}},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/rbac.authorization.k8s.io/v1/clusterroles", crBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create ClusterRole: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	crbBody := mustJSON(t, map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRoleBinding",
		"metadata":   map[string]any{"name": "admins-node-viewer"},
		"subjects": []map[string]any{
			{"kind": "Group", "name": "admins", "apiGroup": "rbac.authorization.k8s.io"},
		},
		"roleRef": map[string]any{
			"kind": "ClusterRole", "name": "node-viewer", "apiGroup": "rbac.authorization.k8s.io",
		},
	})

	resp = do(t, http.MethodPost, base+"/apis/rbac.authorization.k8s.io/v1/clusterrolebindings", crbBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create ClusterRoleBinding: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	sarBody := mustJSON(t, map[string]any{
		"apiVersion": "authorization.k8s.io/v1",
		"kind":       "SubjectAccessReview",
		"spec": map[string]any{
			"user":   "carol",
			"groups": []string{"admins"},
			"resourceAttributes": map[string]any{
				"verb": "list", "resource": "nodes",
			},
		},
	})

	resp = do(t, http.MethodPost, base+"/apis/authorization.k8s.io/v1/subjectaccessreviews", sarBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("SubjectAccessReview: status %d", resp.StatusCode)
	}

	m := decodeMap(t, resp.Body)
	status, _ := m["status"].(map[string]any)

	if allowed, _ := status["allowed"].(bool); !allowed {
		t.Error("carol (group admins): expected list nodes to be allowed via ClusterRoleBinding")
	}
}

func TestSubjectAccessReview_ServiceAccountSubjectAndMissingRole(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	// RoleBinding referencing a Role that was never created: the binding's
	// subject matches, but there's nothing to resolve rules from.
	rbBody := mustJSON(t, map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "RoleBinding",
		"metadata":   map[string]any{"name": "sa-missing-role"},
		"subjects": []map[string]any{
			{"kind": "ServiceAccount", "name": "default"},
		},
		"roleRef": map[string]any{
			"kind": "Role", "name": "does-not-exist", "apiGroup": "rbac.authorization.k8s.io",
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/rbac.authorization.k8s.io/v1/namespaces/default/rolebindings", rbBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create RoleBinding: status %d", resp.StatusCode)
	}
	resp.Body.Close()

	if sarAllowed(t, base, "system:serviceaccount:default:default", "get", "pods", "default") {
		t.Error("expected denial: RoleBinding references a Role that doesn't exist")
	}
}

func TestSubjectAccessReview_NonResourceAttributesNoOpinion(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	body := mustJSON(t, map[string]any{
		"apiVersion": "authorization.k8s.io/v1",
		"kind":       "SubjectAccessReview",
		"spec": map[string]any{
			"user":                  "alice",
			"nonResourceAttributes": map[string]any{"verb": "get", "path": "/healthz"},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/authorization.k8s.io/v1/subjectaccessreviews", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("SubjectAccessReview: status %d", resp.StatusCode)
	}

	m := decodeMap(t, resp.Body)
	status, _ := m["status"].(map[string]any)

	if allowed, _ := status["allowed"].(bool); allowed {
		t.Error("expected nonResourceAttributes review to not be allowed")
	}
}

func TestSubjectAccessReview_DiscoveryAdvertisesGroup(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	resp := do(t, http.MethodGet, base+"/apis/authorization.k8s.io/v1", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discovery: status %d", resp.StatusCode)
	}

	m := decodeMap(t, resp.Body)

	resources, _ := m["resources"].([]any)
	if len(resources) == 0 {
		t.Fatal("expected subjectaccessreviews to be advertised")
	}
}
