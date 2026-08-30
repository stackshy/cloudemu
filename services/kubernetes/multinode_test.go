// Tests for the opt-in multi-node first-fit scheduler (#875): node seeding +
// taints, nodeSelector/taint/request placement, Pending/Unschedulable on no-fit,
// workload readyReplicas reflecting Pending pods, Jobs that can't schedule not
// reporting Succeeded, and per-node DaemonSet fan-out. The default single-node
// behavior is covered by the rest of the suite (every other fixture is N=1).

package kubernetes_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

// newMultiNodeFixture spins up an APIServer whose clusters seed `nodes` synthetic
// nodes, registers one cluster, and returns its test URL prefix.
func newMultiNodeFixture(t *testing.T, nodes int) (string, func()) {
	t.Helper()

	api := kubernetes.NewAPIServer()
	api.SetNodeCount(nodes)
	uid, _ := api.RegisterCluster()
	ts := httptest.NewServer(api)
	api.SetBaseURL(ts.URL)

	return ts.URL + "/k8s/" + uid, ts.Close
}

// podPlacements returns podName -> {nodeName, phase} for every pod in namespace.
func podPlacements(t *testing.T, base, namespace string) map[string]struct{ node, phase string } {
	t.Helper()

	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/"+namespace+"/pods", nil)
	defer resp.Body.Close()

	list := decodeMap(t, resp.Body)
	items, _ := list["items"].([]any)

	out := make(map[string]struct{ node, phase string }, len(items))

	for _, raw := range items {
		item, _ := raw.(map[string]any)
		meta, _ := item["metadata"].(map[string]any)
		spec, _ := item["spec"].(map[string]any)
		status, _ := item["status"].(map[string]any)
		name, _ := meta["name"].(string)
		node, _ := spec["nodeName"].(string)
		phase, _ := status["phase"].(string)
		out[name] = struct{ node, phase string }{node: node, phase: phase}
	}

	return out
}

func TestMultiNode_SeedsControlPlaneAndWorkers(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3)
	defer done()

	resp := do(t, http.MethodGet, base+"/api/v1/nodes", nil)
	defer resp.Body.Close()

	list := decodeMap(t, resp.Body)

	items, _ := list["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("node count: got %d, want 3", len(items))
	}

	taintByNode := map[string]bool{}

	for _, raw := range items {
		item, _ := raw.(map[string]any)
		meta, _ := item["metadata"].(map[string]any)
		spec, _ := item["spec"].(map[string]any)
		name, _ := meta["name"].(string)
		taints, _ := spec["taints"].([]any)
		taintByNode[name] = len(taints) > 0
	}

	if !taintByNode["cloudemu-node-0"] {
		t.Fatalf("control-plane node cloudemu-node-0 must carry a NoSchedule taint")
	}

	for _, worker := range []string{"cloudemu-node-1", "cloudemu-node-2"} {
		if taintByNode[worker] {
			t.Fatalf("worker %s must not be tainted", worker)
		}
	}
}

func TestMultiNode_SingleNodeSeedsUntainted(t *testing.T) {
	base, done := newMultiNodeFixture(t, 1)
	defer done()

	resp := do(t, http.MethodGet, base+"/api/v1/nodes", nil)
	defer resp.Body.Close()

	list := decodeMap(t, resp.Body)

	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("node count: got %d, want 1", len(items))
	}

	item, _ := items[0].(map[string]any)
	spec, _ := item["spec"].(map[string]any)

	if taints, _ := spec["taints"].([]any); len(taints) != 0 {
		t.Fatalf("single node must be untainted so everything schedules; got %d taints", len(taints))
	}
}

func TestMultiNode_FirstFitSpreadAcrossWorkers(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3)
	defer done()

	// Two workers with 4 CPU allocatable each; three pods requesting 3 CPU each
	// (no control-plane toleration). First-fit: pod0->node-1, pod1->node-2,
	// pod2 fits neither worker (3+3>4) and can't use the tainted control-plane
	// node -> Pending. So readyReplicas=2, replicas=3.
	dep := mustJSON(t, map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web"},
		"spec": map[string]any{
			"replicas": int64(3),
			"selector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "web"}},
				"spec": map[string]any{
					"containers": []any{map[string]any{
						"name":  "c",
						"image": "nginx",
						"resources": map[string]any{
							"requests": map[string]any{"cpu": "3"},
						},
					}},
				},
			},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments", dep)
	resp.Body.Close()

	pods := podPlacements(t, base, "default")
	if len(pods) != 3 {
		t.Fatalf("pod count: got %d, want 3", len(pods))
	}

	nodesUsed := map[string]int{}
	pending := 0

	for _, p := range pods {
		if p.phase == "Pending" {
			pending++

			continue
		}

		if p.node == "cloudemu-node-0" {
			t.Fatalf("pod scheduled onto tainted control-plane node without toleration")
		}

		nodesUsed[p.node]++
	}

	if pending != 1 {
		t.Fatalf("expected exactly 1 Pending pod (no worker fits the 3rd), got %d", pending)
	}

	if len(nodesUsed) != 2 {
		t.Fatalf("expected the two Running pods spread across both workers, got %v", nodesUsed)
	}

	// The Deployment status must reflect that only 2 of 3 replicas are ready.
	dresp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/deployments/web", nil)
	defer dresp.Body.Close()

	dm := decodeMap(t, dresp.Body)
	if r, _ := nestedInt(t, dm, "status", "replicas"); r != 3 {
		t.Fatalf("status.replicas: got %d, want 3", r)
	}

	if r, _ := nestedInt(t, dm, "status", "readyReplicas"); r != 2 {
		t.Fatalf("status.readyReplicas: got %d, want 2 (one pod Pending)", r)
	}
}

func TestMultiNode_NodeSelectorPlacesOnLabeledNode(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3)
	defer done()

	// nodeSelector pins the pod to a specific worker by hostname.
	pod := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "pinned"},
		"spec": map[string]any{
			"nodeSelector": map[string]any{"kubernetes.io/hostname": "cloudemu-node-2"},
			"containers":   []any{map[string]any{"name": "c", "image": "nginx"}},
		},
	})

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", pod)
	resp.Body.Close()

	pods := podPlacements(t, base, "default")

	got := pods["pinned"]
	if got.node != "cloudemu-node-2" {
		t.Fatalf("nodeSelector placement: pod on %q, want cloudemu-node-2", got.node)
	}

	if got.phase != "Running" {
		t.Fatalf("nodeSelector pod phase: got %q, want Running", got.phase)
	}
}

func TestMultiNode_TaintRepelsPodWithoutToleration(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3)
	defer done()

	// Force the pod onto the tainted control-plane node via its hostname but give
	// it no toleration -> it cannot schedule -> Pending/Unschedulable.
	pod := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "no-tol"},
		"spec": map[string]any{
			"nodeSelector": map[string]any{"kubernetes.io/hostname": "cloudemu-node-0"},
			"containers":   []any{map[string]any{"name": "c", "image": "nginx"}},
		},
	})

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", pod)
	resp.Body.Close()

	pods := podPlacements(t, base, "default")
	if got := pods["no-tol"]; got.phase != "Pending" || got.node != "" {
		t.Fatalf("untolerated pod: phase=%q node=%q, want Pending/unscheduled", got.phase, got.node)
	}

	// A pod that DOES tolerate the control-plane taint schedules onto it.
	tolPod := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "tol"},
		"spec": map[string]any{
			"nodeSelector": map[string]any{"kubernetes.io/hostname": "cloudemu-node-0"},
			"tolerations": []any{map[string]any{
				"key": "node-role.kubernetes.io/control-plane", "operator": "Exists", "effect": "NoSchedule",
			}},
			"containers": []any{map[string]any{"name": "c", "image": "nginx"}},
		},
	})

	tresp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", tolPod)
	tresp.Body.Close()

	pods = podPlacements(t, base, "default")
	if got := pods["tol"]; got.node != "cloudemu-node-0" || got.phase != "Running" {
		t.Fatalf("tolerating pod: node=%q phase=%q, want cloudemu-node-0/Running", got.node, got.phase)
	}
}

func TestMultiNode_UnschedulableRequestsGoPendingWithEvent(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3)
	defer done()

	// 100 CPU exceeds every node's 4-CPU allocatable -> Unschedulable.
	pod := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "huge"},
		"spec": map[string]any{
			"containers": []any{map[string]any{
				"name": "c", "image": "nginx",
				"resources": map[string]any{"requests": map[string]any{"cpu": "100"}},
			}},
		},
	})

	resp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", pod)
	resp.Body.Close()

	// Fetch the pod and assert Pending + PodScheduled=False/Unschedulable.
	gresp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods/huge", nil)
	defer gresp.Body.Close()

	pm := decodeMap(t, gresp.Body)
	status, _ := pm["status"].(map[string]any)

	if phase, _ := status["phase"].(string); phase != "Pending" {
		t.Fatalf("phase: got %q, want Pending", phase)
	}

	conds, _ := status["conditions"].([]any)
	scheduledFalse := false

	for _, raw := range conds {
		c, _ := raw.(map[string]any)
		if c["type"] == "PodScheduled" && c["status"] == "False" && c["reason"] == "Unschedulable" {
			scheduledFalse = true
		}
	}

	if !scheduledFalse {
		t.Fatalf("want a PodScheduled=False/Unschedulable condition, got %v", conds)
	}

	if !eventReasons(t, base+"/api/v1/namespaces/default/events")["FailedScheduling"] {
		t.Fatalf("expected a FailedScheduling event for the unschedulable pod")
	}
}

func TestMultiNode_JobUnschedulableNotSucceeded(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3)
	defer done()

	job := mustJSON(t, map[string]any{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]any{"name": "big-job"},
		"spec": map[string]any{
			"completions": int64(1),
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "big-job"}},
				"spec": map[string]any{
					"restartPolicy": "Never",
					"containers": []any{map[string]any{
						"name": "c", "image": "busybox",
						"resources": map[string]any{"requests": map[string]any{"cpu": "100"}},
					}},
				},
			},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/batch/v1/namespaces/default/jobs", job)
	resp.Body.Close()

	jresp := do(t, http.MethodGet, base+"/apis/batch/v1/namespaces/default/jobs/big-job", nil)
	defer jresp.Body.Close()

	jm := decodeMap(t, jresp.Body)
	if s, _ := nestedInt(t, jm, "status", "succeeded"); s != 0 {
		t.Fatalf("status.succeeded: got %d, want 0 (pod can't schedule)", s)
	}

	status, _ := jm["status"].(map[string]any)
	if conds, _ := status["conditions"].([]any); len(conds) != 0 {
		t.Fatalf("unschedulable Job must not be Complete; got conditions %v", conds)
	}

	// The materialized pod itself must be Pending, not Succeeded.
	pods := podPlacements(t, base, "default")
	for name, p := range pods {
		if p.phase == "Succeeded" {
			t.Fatalf("pod %s falsely Succeeded despite being unschedulable", name)
		}
	}
}

func TestMultiNode_DaemonSetFanOut(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3)
	defer done()

	// A DaemonSet with no control-plane toleration lands only on the two workers.
	ds := mustJSON(t, map[string]any{
		"apiVersion": "apps/v1", "kind": "DaemonSet",
		"metadata": map[string]any{"name": "agent"},
		"spec": map[string]any{
			"selector": map[string]any{"matchLabels": map[string]any{"app": "agent"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "agent"}},
				"spec":     map[string]any{"containers": []any{map[string]any{"name": "c", "image": "agent"}}},
			},
		},
	})

	resp := do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/daemonsets", ds)
	resp.Body.Close()

	dresp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/daemonsets/agent", nil)
	defer dresp.Body.Close()

	dm := decodeMap(t, dresp.Body)
	if n, _ := nestedInt(t, dm, "status", "desiredNumberScheduled"); n != 2 {
		t.Fatalf("DaemonSet desiredNumberScheduled: got %d, want 2 (workers only)", n)
	}

	pods := podPlacements(t, base, "default")

	agentNodes := map[string]bool{}

	for name, p := range pods {
		if name == "agent-cloudemu-node-0" {
			t.Fatalf("DaemonSet without control-plane toleration must not run on the control-plane node")
		}

		if p.node != "" {
			agentNodes[p.node] = true
		}
	}

	if !agentNodes["cloudemu-node-1"] || !agentNodes["cloudemu-node-2"] {
		t.Fatalf("DaemonSet must run one pod per worker; nodes seen = %v", agentNodes)
	}
}

func TestMultiNode_KubeProxyScalesToN(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3)
	defer done()

	// The seeded kube-proxy DaemonSet tolerates every taint, so it runs on all
	// three nodes (control-plane included).
	dresp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/kube-system/daemonsets/kube-proxy", nil)
	defer dresp.Body.Close()

	dm := decodeMap(t, dresp.Body)
	if n, _ := nestedInt(t, dm, "status", "desiredNumberScheduled"); n != 3 {
		t.Fatalf("kube-proxy desiredNumberScheduled: got %d, want 3 (all nodes)", n)
	}

	pods := podPlacements(t, base, "kube-system")

	proxyNodes := map[string]bool{}

	for name, p := range pods {
		if len(name) >= len("kube-proxy-") && name[:len("kube-proxy-")] == "kube-proxy-" {
			proxyNodes[p.node] = true
		}
	}

	for _, n := range []string{"cloudemu-node-0", "cloudemu-node-1", "cloudemu-node-2"} {
		if !proxyNodes[n] {
			t.Fatalf("kube-proxy missing on %s; nodes seen = %v", n, proxyNodes)
		}
	}
}

func TestMultiNode_EndpointsCarryRealNode(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3)
	defer done()

	// Spread two pods (3 CPU each) across the two workers, then front them with a
	// Service and assert the EndpointSlice carries each pod's actual node.
	dep := mustJSON(t, map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "svc-app"},
		"spec": map[string]any{
			"replicas": int64(2),
			"selector": map[string]any{"matchLabels": map[string]any{"app": "svc-app"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "svc-app"}},
				"spec": map[string]any{
					"containers": []any{map[string]any{
						"name": "c", "image": "nginx",
						"resources": map[string]any{"requests": map[string]any{"cpu": "3"}},
					}},
				},
			},
		},
	})

	dresp := do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments", dep)
	dresp.Body.Close()

	svc := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Service",
		"metadata": map[string]any{"name": "svc-app"},
		"spec": map[string]any{
			"selector": map[string]any{"app": "svc-app"},
			"ports":    []any{map[string]any{"port": int64(80)}},
		},
	})

	sresp := do(t, http.MethodPost, base+"/api/v1/namespaces/default/services", svc)
	sresp.Body.Close()

	eresp := do(t, http.MethodGet, base+"/apis/discovery.k8s.io/v1/namespaces/default/endpointslices/svc-app", nil)
	defer eresp.Body.Close()

	em := decodeMap(t, eresp.Body)
	endpoints, _ := em["endpoints"].([]any)

	if len(endpoints) != 2 {
		t.Fatalf("endpointslice endpoints: got %d, want 2", len(endpoints))
	}

	seen := map[string]bool{}

	for _, raw := range endpoints {
		ep, _ := raw.(map[string]any)
		node, _ := ep["nodeName"].(string)
		if node == "" || node == "cloudemu-node-0" {
			t.Fatalf("endpoint nodeName = %q, want a worker node", node)
		}

		seen[node] = true
	}

	if len(seen) != 2 {
		t.Fatalf("endpoints must carry both workers' node names, saw %v", seen)
	}
}
