// Tests for the advanced scheduler (#893): required nodeAffinity placement and
// rejection, required inter-pod anti-affinity spreading across topology domains,
// topologySpreadConstraints balancing with maxSkew, preferred-affinity scoring
// biasing placement over first-fit, and an unsatisfiable required constraint
// leaving a Pod Pending. A plain Pod (no affinity/spread) must still schedule
// first-fit exactly as before (regression guard). tolerationSeconds / live
// NoExecute eviction are a deferred follow-up and not covered here.

package kubernetes_test

import (
	"net/http"
	"testing"
)

// labeledNodeJSON registers a schedulable, untainted worker Node with an
// explicit label set and 4 CPU allocatable — a client seeding a labeled node
// pool (zones, disk types) for affinity/spread scheduling.
func labeledNodeJSON(t *testing.T, name string, labels map[string]any) []byte {
	t.Helper()

	l := map[string]any{"kubernetes.io/hostname": name}
	for k, v := range labels {
		l[k] = v
	}

	return mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Node",
		"metadata": map[string]any{"name": name, "labels": l},
		"status": map[string]any{
			"allocatable": map[string]any{"cpu": "4"},
			"addresses":   []any{map[string]any{"type": "InternalIP", "address": "10.0.0.50"}},
		},
	})
}

// registerNode POSTs a Node and asserts it was created.
func registerNode(t *testing.T, base string, body []byte) {
	t.Helper()

	resp := do(t, http.MethodPost, base+"/api/v1/nodes", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("node create status: got %d, want 201", resp.StatusCode)
	}
}

// zonedPoolFixture returns a fixture whose only labeled worker pool is the nodes
// the caller registers. It starts from a single (untainted) seed node with no
// "pool" label, so a pod pinned via nodeSelector pool=app only ever considers
// the registered zone nodes — isolating affinity/spread behavior from the seed.
func zonedPoolFixture(t *testing.T, nodes ...[]byte) (string, func()) {
	t.Helper()

	base, done := newMultiNodeFixture(t, 1)
	for _, n := range nodes {
		registerNode(t, base, n)
	}

	return base, done
}

const zoneKey = "topology.kubernetes.io/zone"

func TestScheduling_RequiredNodeAffinityPlacesAndRejects(t *testing.T) {
	base, done := zonedPoolFixture(t,
		labeledNodeJSON(t, "worker-ssd", map[string]any{"pool": "app", "disktype": "ssd"}),
		labeledNodeJSON(t, "worker-hdd", map[string]any{"pool": "app", "disktype": "hdd"}),
	)
	defer done()

	// required nodeAffinity disktype In [ssd] must place onto the ssd node only.
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods",
		affinityPodJSON(t, "wants-ssd", "ssd")).Body.Close()

	if got := podPlacements(t, base, "default")["wants-ssd"]; got.node != "worker-ssd" || got.phase != "Running" {
		t.Fatalf("wants-ssd: node=%q phase=%q, want worker-ssd/Running", got.node, got.phase)
	}

	// required nodeAffinity disktype In [nvme] matches no node -> Pending.
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods",
		affinityPodJSON(t, "wants-nvme", "nvme")).Body.Close()

	if got := podPlacements(t, base, "default")["wants-nvme"]; got.phase != "Pending" || got.node != "" {
		t.Fatalf("wants-nvme: phase=%q node=%q, want Pending/unscheduled", got.phase, got.node)
	}
}

// affinityPodJSON builds a Pod pinned to the app pool that requires
// disktype==disk via required nodeAffinity.
func affinityPodJSON(t *testing.T, name, disk string) []byte {
	t.Helper()

	return mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"nodeSelector": map[string]any{"pool": "app"},
			"affinity": map[string]any{"nodeAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{
					"nodeSelectorTerms": []any{map[string]any{
						"matchExpressions": []any{map[string]any{
							"key": "disktype", "operator": "In", "values": []any{disk},
						}},
					}},
				},
			}},
			"containers": []any{map[string]any{"name": "c", "image": "nginx"}},
		},
	})
}

func TestScheduling_PodAntiAffinitySeparatesZones(t *testing.T) {
	base, done := zonedPoolFixture(t,
		labeledNodeJSON(t, "worker-a", map[string]any{"pool": "app", zoneKey: "zone-a"}),
		labeledNodeJSON(t, "worker-b", map[string]any{"pool": "app", zoneKey: "zone-b"}),
	)
	defer done()

	// Two web Pods that repel each other by zone must land in different zones.
	for _, name := range []string{"web-1", "web-2"} {
		do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods",
			antiAffinityPodJSON(t, name)).Body.Close()
	}

	pods := podPlacements(t, base, "default")
	if pods["web-1"].phase != "Running" || pods["web-2"].phase != "Running" {
		t.Fatalf("both anti-affinity pods must be Running, got %+v", pods)
	}

	if pods["web-1"].node == pods["web-2"].node {
		t.Fatalf("anti-affinity failed: both pods on %q, want different zones", pods["web-1"].node)
	}
}

// antiAffinityPodJSON builds an app=web Pod that repels other app=web Pods
// across the zone topology key.
func antiAffinityPodJSON(t *testing.T, name string) []byte {
	t.Helper()

	return mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name, "labels": map[string]any{"app": "web"}},
		"spec": map[string]any{
			"nodeSelector": map[string]any{"pool": "app"},
			"affinity": map[string]any{"podAntiAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
					"topologyKey":   zoneKey,
					"labelSelector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
				}},
			}},
			"containers": []any{map[string]any{"name": "c", "image": "nginx"}},
		},
	})
}

func TestScheduling_TopologySpreadBalancesZones(t *testing.T) {
	base, done := zonedPoolFixture(t,
		labeledNodeJSON(t, "worker-a", map[string]any{"pool": "app", zoneKey: "zone-a"}),
		labeledNodeJSON(t, "worker-b", map[string]any{"pool": "app", zoneKey: "zone-b"}),
	)
	defer done()

	// 4 replicas with maxSkew=1 across two zones must split 2/2.
	dep := mustJSON(t, map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "spread"},
		"spec": map[string]any{
			"replicas": int64(4),
			"selector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "web"}},
				"spec": map[string]any{
					"nodeSelector": map[string]any{"pool": "app"},
					"topologySpreadConstraints": []any{map[string]any{
						"maxSkew":           int64(1),
						"topologyKey":       zoneKey,
						"whenUnsatisfiable": "DoNotSchedule",
						"labelSelector":     map[string]any{"matchLabels": map[string]any{"app": "web"}},
					}},
					"containers": []any{map[string]any{"name": "c", "image": "nginx"}},
				},
			},
		},
	})
	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments", dep).Body.Close()

	perNode := map[string]int{}

	for name, p := range podPlacements(t, base, "default") {
		if p.phase != "Running" {
			t.Fatalf("pod %s not Running: phase=%q", name, p.phase)
		}

		perNode[p.node]++
	}

	if perNode["worker-a"] != 2 || perNode["worker-b"] != 2 {
		t.Fatalf("topology spread imbalance: worker-a=%d worker-b=%d, want 2/2", perNode["worker-a"], perNode["worker-b"])
	}
}

func TestScheduling_PreferredNodeAffinityBiasesPlacement(t *testing.T) {
	base, done := zonedPoolFixture(t,
		labeledNodeJSON(t, "worker-a", map[string]any{"pool": "app", zoneKey: "zone-a"}),
		labeledNodeJSON(t, "worker-b", map[string]any{"pool": "app", zoneKey: "zone-b"}),
	)
	defer done()

	// worker-a sorts first (first-fit would pick it), but a preferred nodeAffinity
	// weight for zone-b must bias placement onto worker-b.
	pod := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "prefers-b"},
		"spec": map[string]any{
			"nodeSelector": map[string]any{"pool": "app"},
			"affinity": map[string]any{"nodeAffinity": map[string]any{
				"preferredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{
					"weight": int64(100),
					"preference": map[string]any{"matchExpressions": []any{map[string]any{
						"key": zoneKey, "operator": "In", "values": []any{"zone-b"},
					}}},
				}},
			}},
			"containers": []any{map[string]any{"name": "c", "image": "nginx"}},
		},
	})
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", pod).Body.Close()

	if got := podPlacements(t, base, "default")["prefers-b"]; got.node != "worker-b" || got.phase != "Running" {
		t.Fatalf("prefers-b: node=%q phase=%q, want worker-b/Running (preferred affinity over first-fit)", got.node, got.phase)
	}
}

func TestScheduling_UnsatisfiableRequiredConstraintStaysPending(t *testing.T) {
	base, done := zonedPoolFixture(t,
		labeledNodeJSON(t, "worker-a", map[string]any{"pool": "app"}),
	)
	defer done()

	// required nodeAffinity gpu Exists matches no node -> Pending + FailedScheduling.
	pod := mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "needs-gpu"},
		"spec": map[string]any{
			"nodeSelector": map[string]any{"pool": "app"},
			"affinity": map[string]any{"nodeAffinity": map[string]any{
				"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{
					"nodeSelectorTerms": []any{map[string]any{
						"matchExpressions": []any{map[string]any{"key": "gpu", "operator": "Exists"}},
					}},
				},
			}},
			"containers": []any{map[string]any{"name": "c", "image": "nginx"}},
		},
	})
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", pod).Body.Close()

	if got := podPlacements(t, base, "default")["needs-gpu"]; got.phase != "Pending" || got.node != "" {
		t.Fatalf("needs-gpu: phase=%q node=%q, want Pending/unscheduled", got.phase, got.node)
	}

	if !eventReasons(t, base+"/api/v1/namespaces/default/events")["FailedScheduling"] {
		t.Fatalf("expected a FailedScheduling event for the unschedulable pod")
	}
}

func TestScheduling_PlainPodStillSchedulesFirstFit(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3)
	defer done()

	// No affinity/spread: first-fit onto the lowest-named worker, as before #893.
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods",
		mustJSON(t, map[string]any{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]any{"name": "plain"},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "c", "image": "nginx"}}},
		})).Body.Close()

	if got := podPlacements(t, base, "default")["plain"]; got.node != "cloudemu-node-1" || got.phase != "Running" {
		t.Fatalf("plain pod: node=%q phase=%q, want cloudemu-node-1/Running (unchanged first-fit)", got.node, got.phase)
	}
}
