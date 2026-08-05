// Tests for Phase 3 controller materialization: Deployment→ReplicaSet→Pod
// interposition and DaemonSet nodeSelector honoring.

package kubernetes_test

import (
	"net/http"
	"testing"
)

func TestDeployment_InterposesReplicaSet(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	dep := mustJSON(t, map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web"},
		"spec": map[string]any{
			"replicas": int64(2),
			"selector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "web"}},
				"spec":     map[string]any{"containers": []any{map[string]any{"name": "c", "image": "nginx"}}},
			},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments", dep)
	resp.Body.Close()

	// A ReplicaSet owned by the Deployment must now exist.
	rs := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/replicasets", nil)
	defer rs.Body.Close()

	list := decodeMap(t, rs.Body)

	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 ReplicaSet interposed for the Deployment, got %d", len(items))
	}

	item, _ := items[0].(map[string]any)
	meta, _ := item["metadata"].(map[string]any)
	owners, _ := meta["ownerReferences"].([]any)
	if len(owners) == 0 {
		t.Fatalf("interposed ReplicaSet has no ownerReferences")
	}

	owner, _ := owners[0].(map[string]any)
	if owner["kind"] != "Deployment" {
		t.Fatalf("ReplicaSet owner kind = %v, want Deployment", owner["kind"])
	}
}

func TestDaemonSet_NodeSelectorSkipsNonMatchingNode(t *testing.T) {
	base, done := newFixture(t)
	defer done()

	// A nodeSelector the single synthetic node does not satisfy → 0 pods.
	ds := mustJSON(t, map[string]any{
		"apiVersion": "apps/v1", "kind": "DaemonSet",
		"metadata": map[string]any{"name": "agent"},
		"spec": map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"app": "agent"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "agent"}},
				"spec": map[string]any{
					"nodeSelector": map[string]any{"disktype": "ssd"},
					"containers":   []any{map[string]any{"name": "c", "image": "img"}},
				},
			},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/daemonsets", ds)
	obj := decodeMap(t, resp.Body)
	resp.Body.Close()

	status, _ := obj["status"].(map[string]any)
	if got, _ := nestedInt(t, status, "desiredNumberScheduled"); got != 0 {
		t.Fatalf("non-matching nodeSelector: desiredNumberScheduled = %d, want 0", got)
	}

	// A matching selector → 1 pod.
	ds2 := mustJSON(t, map[string]any{
		"apiVersion": "apps/v1", "kind": "DaemonSet",
		"metadata": map[string]any{"name": "agent2"},
		"spec": map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"app": "a2"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "a2"}},
				"spec": map[string]any{
					"nodeSelector": map[string]any{"kubernetes.io/hostname": "cloudemu-node-0"},
					"containers":   []any{map[string]any{"name": "c", "image": "img"}},
				},
			},
		},
	})

	resp2 := do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/daemonsets", ds2)
	obj2 := decodeMap(t, resp2.Body)
	resp2.Body.Close()

	status2, _ := obj2["status"].(map[string]any)
	if got, _ := nestedInt(t, status2, "desiredNumberScheduled"); got != 1 {
		t.Fatalf("matching nodeSelector: desiredNumberScheduled = %d, want 1", got)
	}
}
