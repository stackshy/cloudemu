// Tests for managed node pools (SyncNodePool): a cloud control plane (EKS
// managed nodegroups) drives a group of real Node objects that scale up, scale
// down with cordon and drain, re-apply labels and taints in place, and vanish on
// delete, with Pods rescheduled or left Pending as the node set changes.

package kubernetes_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/stackshy/cloudemu/v2/services/kubernetes"
)

func newPoolFixture(t *testing.T) (*kubernetes.APIServer, *kubernetes.ClusterState, string, func()) {
	t.Helper()

	api := kubernetes.NewAPIServer()
	uid, state := api.RegisterCluster()
	ts := httptest.NewServer(api)
	api.SetBaseURL(ts.URL)

	return api, state, ts.URL + "/k8s/" + uid, ts.Close
}

func testPool(count int) kubernetes.NodePool {
	return kubernetes.NodePool{
		Name:   "workers",
		Count:  count,
		Labels: map[string]string{"pool": "workers", "team": "a"},
		Zones:  []string{"eu-west-1a", "eu-west-1b"},
		Capacity: kubernetes.NodeResources{
			CPU: "2", Memory: "4015584Ki", Pods: "17", EphemeralStorage: "20959212Ki",
		},
		Allocatable: kubernetes.NodeResources{
			CPU: "1930m", Memory: "3474400Ki", Pods: "17", EphemeralStorage: "18242267924",
		},
		Info: kubernetes.NodeInfo{
			KubeletVersion: "v1.30.4-eks-a737599", OSImage: "Amazon Linux 2023",
			OperatingSystem: "linux", Architecture: "amd64",
		},
		NodeName: func(ip string) string {
			return "ip-" + strings.ReplaceAll(ip, ".", "-") + ".eu-west-1.compute.internal"
		},
		ProviderID: func(zone, name string) string { return "aws:///" + zone + "/i-" + name[:6] },
	}
}

// nodeItems lists every Node as decoded JSON, keyed by name.
func nodeItems(t *testing.T, base string) map[string]map[string]any {
	t.Helper()

	resp := do(t, http.MethodGet, base+"/api/v1/nodes", nil)
	list := decodeMap(t, resp.Body)
	items, _ := list["items"].([]any)

	out := make(map[string]map[string]any, len(items))

	for _, raw := range items {
		item, _ := raw.(map[string]any)
		meta, _ := item["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		out[name] = item
	}

	return out
}

func nodeLabels(n map[string]any) map[string]any {
	meta, _ := n["metadata"].(map[string]any)
	labels, _ := meta["labels"].(map[string]any)

	return labels
}

func nodeTaintList(n map[string]any) []any {
	spec, _ := n["spec"].(map[string]any)
	taints, _ := spec["taints"].([]any)

	return taints
}

func plainPodJSON(t *testing.T, name string, tolerate bool) []byte {
	t.Helper()

	spec := map[string]any{"containers": []any{map[string]any{"name": "c", "image": "nginx"}}}
	if tolerate {
		spec["tolerations"] = []any{map[string]any{
			"key": "dedicated", "operator": "Equal", "value": "gpu", "effect": "NoSchedule",
		}}
	}

	return mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": name}, "spec": spec,
	})
}

func TestNodePool_SyncCreatesNodesAndRetiresBootstrapNode(t *testing.T) {
	_, state, base, done := newPoolFixture(t)
	defer done()

	// A Pod created before any managed node lands on the bootstrap node.
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", plainPodJSON(t, "early", false)).Body.Close()

	state.SyncNodePool(testPool(2))

	nodes := nodeItems(t, base)
	if _, ok := nodes["cloudemu-node-0"]; ok {
		t.Fatal("bootstrap node cloudemu-node-0 must be retired once managed nodes exist")
	}

	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2: %v", len(nodes), keys(nodes))
	}

	zones := map[string]bool{}

	for name, n := range nodes {
		if !strings.HasPrefix(name, "ip-10-0-") || !strings.HasSuffix(name, ".eu-west-1.compute.internal") {
			t.Fatalf("node name %q does not follow the pool naming", name)
		}

		labels := nodeLabels(n)
		if labels["pool"] != "workers" || labels["team"] != "a" || labels["kubernetes.io/hostname"] != name {
			t.Fatalf("node %s labels = %v", name, labels)
		}

		zone, _ := labels["topology.kubernetes.io/zone"].(string)
		zones[zone] = true

		status, _ := n["status"].(map[string]any)
		alloc, _ := status["allocatable"].(map[string]any)
		capacity, _ := status["capacity"].(map[string]any)

		if alloc["cpu"] != "1930m" || capacity["cpu"] != "2" || alloc["pods"] != "17" {
			t.Fatalf("node %s capacity=%v allocatable=%v", name, capacity, alloc)
		}

		if !nodeIsReady(n) {
			t.Fatalf("node %s is not Ready", name)
		}

		spec, _ := n["spec"].(map[string]any)
		if pid, _ := spec["providerID"].(string); !strings.HasPrefix(pid, "aws:///"+zone+"/") {
			t.Fatalf("node %s providerID = %q", name, pid)
		}
	}

	if !zones["eu-west-1a"] || !zones["eu-west-1b"] {
		t.Fatalf("nodes not spread over both zones: %v", zones)
	}

	early := podPlacements(t, base, "default")["early"]
	if early.phase != "Running" || nodes[early.node] == nil {
		t.Fatalf("early pod after sync: %+v, want Running on a managed node", early)
	}

	resp := do(t, http.MethodGet, base+"/apis/coordination.k8s.io/v1/namespaces/kube-node-lease/leases", nil)
	leases := decodeMap(t, resp.Body)

	if items, _ := leases["items"].([]any); len(items) != 2 {
		t.Fatalf("got %d node leases, want 2", len(items))
	}
}

func nodeIsReady(n map[string]any) bool {
	status, _ := n["status"].(map[string]any)
	conds, _ := status["conditions"].([]any)

	for _, raw := range conds {
		c, _ := raw.(map[string]any)
		if c["type"] == "Ready" && c["status"] == "True" {
			return true
		}
	}

	return false
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

func TestNodePool_ScaleDownDrainsAndReschedules(t *testing.T) {
	_, state, base, done := newPoolFixture(t)
	defer done()

	state.SyncNodePool(testPool(3))

	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", plainPodJSON(t, name, false)).Body.Close()
	}

	before := nodeItems(t, base)

	state.SyncNodePool(testPool(1))

	after := nodeItems(t, base)
	if len(after) != 1 {
		t.Fatalf("after scale-down got %d nodes, want 1: %v", len(after), keys(after))
	}

	var kept string
	for name := range after {
		kept = name
	}

	// The lowest-ordinal node survives: it is the one with the smallest IP.
	for name := range before {
		if name < kept {
			t.Fatalf("scale-down kept %s but removed older node %s", kept, name)
		}
	}

	for name, p := range podPlacements(t, base, "default") {
		if p.phase != "Running" || p.node != kept {
			t.Fatalf("pod %s after scale-down: %+v, want Running on %s", name, p, kept)
		}
	}

	resp := do(t, http.MethodGet, base+"/apis/coordination.k8s.io/v1/namespaces/kube-node-lease/leases", nil)
	leases := decodeMap(t, resp.Body)

	if items, _ := leases["items"].([]any); len(items) != 1 {
		t.Fatalf("got %d node leases after scale-down, want 1", len(items))
	}
}

// TestNodePool_ScaleDownCordonsWholeRemovalSetFirst checks that a Pod drained
// off one removed node is never rescheduled onto another node that the same
// scale down is about to remove.
func TestNodePool_ScaleDownCordonsWholeRemovalSetFirst(t *testing.T) {
	_, state, base, done := newPoolFixture(t)
	defer done()

	const (
		nodeA = "ip-10-0-1-10.eu-west-1.compute.internal"
		nodeB = "ip-10-0-1-11.eu-west-1.compute.internal"
		nodeC = "ip-10-0-1-12.eu-west-1.compute.internal"
	)

	create := func(name string) {
		t.Helper()
		do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", cpuPodJSON(t, name, "600m")).Body.Close()
	}

	// Nodes A and B: three 600m Pods fill A, the fourth lands on B.
	state.SyncNodePool(testPool(2))

	for _, name := range []string{"p1", "p2", "p3", "p4"} {
		create(name)
	}

	// Add C and steer p5 onto it by cordoning B for one create.
	state.SyncNodePool(testPool(3))

	setUnschedulable := func(node string, v bool) {
		t.Helper()

		patch := mustJSON(t, map[string]any{"spec": map[string]any{"unschedulable": v}})
		do(t, http.MethodPatch, base+"/api/v1/nodes/"+node, patch).Body.Close()
	}

	setUnschedulable(nodeB, true)
	create("p5")
	setUnschedulable(nodeB, false)

	placed := podPlacements(t, base, "default")
	if placed["p4"].node != nodeB || placed["p5"].node != nodeC {
		t.Fatalf("setup placements: p4=%+v p5=%+v, want p4 on B and p5 on C", placed["p4"], placed["p5"])
	}

	// Scale to 1 removes B and C. B has room for p5, but it is being removed too.
	state.SyncNodePool(testPool(1))

	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/events", nil)
	events := decodeMap(t, resp.Body)
	items, _ := events["items"].([]any)

	for _, raw := range items {
		ev, _ := raw.(map[string]any)
		if msg, _ := ev["message"].(string); msg == "Successfully assigned default/p5 to "+nodeB {
			t.Fatalf("p5 was rescheduled onto %s, a node in the same removal set", nodeB)
		}
	}

	for _, name := range []string{"p4", "p5"} {
		if got := podPlacements(t, base, "default")[name]; got.phase != "Pending" || got.node != "" {
			t.Fatalf("%s after scale-down: %+v, want Pending (node A is full)", name, got)
		}
	}
}

func TestNodePool_TaintedPoolSchedulesOnlyTolerations(t *testing.T) {
	_, state, base, done := newPoolFixture(t)
	defer done()

	pool := testPool(1)
	pool.Taints = []corev1.Taint{{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule}}
	state.SyncNodePool(pool)

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", plainPodJSON(t, "tolerant", true)).Body.Close()
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", plainPodJSON(t, "plain", false)).Body.Close()

	got := podPlacements(t, base, "default")
	if got["tolerant"].phase != "Running" {
		t.Fatalf("tolerant pod: %+v, want Running", got["tolerant"])
	}

	if got["plain"].phase != "Pending" || got["plain"].node != "" {
		t.Fatalf("plain pod on a single tainted node: %+v, want Pending/unscheduled", got["plain"])
	}

	for _, n := range nodeItems(t, base) {
		taints := nodeTaintList(n)
		if len(taints) != 1 {
			t.Fatalf("taints = %v, want the one pool taint", taints)
		}
	}
}

func TestNodePool_DeleteLeavesPodsPending(t *testing.T) {
	_, state, base, done := newPoolFixture(t)
	defer done()

	state.SyncNodePool(testPool(2))
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", plainPodJSON(t, "web", false)).Body.Close()

	state.SyncNodePool(testPool(0))

	if nodes := nodeItems(t, base); len(nodes) != 0 {
		t.Fatalf("after delete got nodes %v, want none", keys(nodes))
	}

	if got := podPlacements(t, base, "default")["web"]; got.phase != "Pending" || got.node != "" {
		t.Fatalf("web after pool delete: %+v, want Pending/unscheduled", got)
	}

	// A Pod created with no nodes at all must also stay Pending rather than
	// binding to the retired bootstrap node.
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", plainPodJSON(t, "late", false)).Body.Close()

	if got := podPlacements(t, base, "default")["late"]; got.phase != "Pending" || got.node != "" {
		t.Fatalf("late pod with no nodes: %+v, want Pending/unscheduled", got)
	}

	// Scaling back up schedules the Pending Pods.
	state.SyncNodePool(testPool(1))

	for name, p := range podPlacements(t, base, "default") {
		if p.phase != "Running" {
			t.Fatalf("pod %s after scale-up: %+v, want Running", name, p)
		}
	}
}

func TestNodePool_LabelAndTaintUpdateInPlace(t *testing.T) {
	_, state, base, done := newPoolFixture(t)
	defer done()

	pool := testPool(2)
	pool.Taints = []corev1.Taint{{Key: "old", Value: "x", Effect: corev1.TaintEffectNoSchedule}}
	state.SyncNodePool(pool)

	before := nodeItems(t, base)

	// A label a user adds by hand must survive a pool update.
	for name := range before {
		patch := mustJSON(t, map[string]any{"metadata": map[string]any{"labels": map[string]any{"manual": "yes"}}})
		req := do(t, http.MethodPatch, base+"/api/v1/nodes/"+name, patch)
		req.Body.Close()
	}

	pool.Labels = map[string]string{"pool": "workers", "tier": "gold"}
	pool.Taints = []corev1.Taint{{Key: "new", Value: "y", Effect: corev1.TaintEffectPreferNoSchedule}}
	state.SyncNodePool(pool)

	after := nodeItems(t, base)
	if len(after) != 2 {
		t.Fatalf("got %d nodes after update, want 2", len(after))
	}

	for name, n := range after {
		if before[name] == nil {
			t.Fatalf("label update replaced node %s; nodes must be updated in place", name)
		}

		labels := nodeLabels(n)
		if labels["tier"] != "gold" || labels["manual"] != "yes" {
			t.Fatalf("node %s labels = %v", name, labels)
		}

		if _, ok := labels["team"]; ok {
			t.Fatalf("node %s still has removed pool label team: %v", name, labels)
		}

		taints := nodeTaintList(n)
		if len(taints) != 1 {
			t.Fatalf("node %s taints = %v, want only the new taint", name, taints)
		}

		taint, _ := taints[0].(map[string]any)
		if taint["key"] != "new" || taint["effect"] != "PreferNoSchedule" {
			t.Fatalf("node %s taint = %v", name, taint)
		}
	}
}

func TestNodePool_CordonedNodeIsNotScheduled(t *testing.T) {
	_, state, base, done := newPoolFixture(t)
	defer done()

	state.SyncNodePool(testPool(2))

	names := keys(nodeItems(t, base))
	cordon := mustJSON(t, map[string]any{"spec": map[string]any{"unschedulable": true}})

	for _, name := range names {
		do(t, http.MethodPatch, base+"/api/v1/nodes/"+name, cordon).Body.Close()
	}

	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", plainPodJSON(t, "p", false)).Body.Close()

	if got := podPlacements(t, base, "default")["p"]; got.phase != "Pending" {
		t.Fatalf("pod with every node cordoned: %+v, want Pending", got)
	}
}

func TestNodePool_SnapshotKeepsManagedState(t *testing.T) {
	api, state, base, done := newPoolFixture(t)
	defer done()

	state.SyncNodePool(testPool(2))
	state.SyncNodePool(testPool(0))

	raw, err := api.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored := kubernetes.NewAPIServer()
	if err := restored.Restore(context.Background(), raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	ts := httptest.NewServer(restored)
	defer ts.Close()

	uid := strings.TrimPrefix(base[strings.Index(base, "/k8s/"):], "/k8s/")
	rbase := ts.URL + "/k8s/" + uid

	// The restored cluster still knows its bootstrap node was retired, so a new
	// Pod stays Pending instead of binding to a node that does not exist.
	do(t, http.MethodPost, rbase+"/api/v1/namespaces/default/pods", plainPodJSON(t, "p", false)).Body.Close()

	if got := podPlacements(t, rbase, "default")["p"]; got.phase != "Pending" || got.node != "" {
		t.Fatalf("pod after restore: %+v, want Pending/unscheduled", got)
	}

	// The node ordinal allocator survives too: the next node gets a fresh IP,
	// not one the two earlier nodes held.
	restored.Lookup(uid).SyncNodePool(testPool(1))

	for name := range nodeItems(t, rbase) {
		if name == "ip-10-0-1-10.eu-west-1.compute.internal" || name == "ip-10-0-1-11.eu-west-1.compute.internal" {
			t.Fatalf("restored cluster reused an earlier node address: %s", name)
		}
	}
}
