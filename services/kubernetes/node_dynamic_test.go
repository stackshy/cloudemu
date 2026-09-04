// Tests for dynamic Node add/remove (#894): registering a Node at runtime
// schedules Pods left Pending because nothing fit them, and deleting a Node
// reschedules its bound Pods onto a remaining fitting node (and deletes the
// node-pinned DaemonSet Pods). Node membership was fixed-at-seed before this.

package kubernetes_test

import (
	"net/http"
	"testing"
)

// workerNodeJSON builds a schedulable worker Node (untainted, cpu allocatable)
// as a client would POST it to register a node at runtime.
func workerNodeJSON(t *testing.T, name, cpu string) []byte {
	t.Helper()

	return mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Node",
		"metadata": map[string]any{
			"name":   name,
			"labels": map[string]any{"kubernetes.io/hostname": name},
		},
		"status": map[string]any{
			"allocatable": map[string]any{"cpu": cpu},
			"addresses":   []any{map[string]any{"type": "InternalIP", "address": "10.0.0.99"}},
		},
	})
}

// cpuPodJSON builds a Pod requesting `cpu` CPU with no control-plane toleration.
func cpuPodJSON(t *testing.T, name, cpu string) []byte {
	t.Helper()

	return mustJSON(t, map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"containers": []any{map[string]any{
				"name": "c", "image": "nginx",
				"resources": map[string]any{"requests": map[string]any{"cpu": cpu}},
			}},
		},
	})
}

func TestDynamicNode_AddSchedulesPendingPod(t *testing.T) {
	base, done := newMultiNodeFixture(t, 2) // node-0 (control-plane, tainted), node-1 (worker, 4 CPU)
	defer done()

	// Fill the only worker, then add a Pod that no longer fits: node-1 is full and
	// the control-plane node is tainted -> the second Pod is stuck Pending.
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", cpuPodJSON(t, "filler", "4")).Body.Close()
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", cpuPodJSON(t, "waiter", "4")).Body.Close()

	if got := podPlacements(t, base, "default")["waiter"]; got.phase != "Pending" || got.node != "" {
		t.Fatalf("waiter before add: phase=%q node=%q, want Pending/unscheduled", got.phase, got.node)
	}

	// Register a new worker at runtime -> the Pending Pod must schedule onto it.
	resp := do(t, http.MethodPost, base+"/api/v1/nodes", workerNodeJSON(t, "cloudemu-node-9", "4"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("node create status: got %d, want 201", resp.StatusCode)
	}

	resp.Body.Close()

	if got := podPlacements(t, base, "default")["waiter"]; got.phase != "Running" || got.node != "cloudemu-node-9" {
		t.Fatalf("waiter after add: phase=%q node=%q, want Running/cloudemu-node-9", got.phase, got.node)
	}
}

func TestDynamicNode_AddRefansDaemonSet(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3) // node-0 (tainted), node-1 + node-2 (workers)
	defer done()

	// A DaemonSet fans one Pod onto each worker (agent-cloudemu-node-1/-2).
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
	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/daemonsets", ds).Body.Close()

	if _, ok := podPlacements(t, base, "default")["agent-cloudemu-node-9"]; ok {
		t.Fatalf("agent-cloudemu-node-9 must not exist before the node is added")
	}

	// Registering a new schedulable node must immediately re-fan the DaemonSet
	// onto it, as the real DaemonSet controller does on Node creation.
	do(t, http.MethodPost, base+"/api/v1/nodes", workerNodeJSON(t, "cloudemu-node-9", "4")).Body.Close()

	if got := podPlacements(t, base, "default")["agent-cloudemu-node-9"]; got.node != "cloudemu-node-9" || got.phase != "Running" {
		t.Fatalf("agent-cloudemu-node-9 after add: node=%q phase=%q, want cloudemu-node-9/Running", got.node, got.phase)
	}
}

func TestDynamicNode_DeleteReschedulesBoundPod(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3) // node-0 (tainted), node-1 + node-2 (workers, 4 CPU)
	defer done()

	// First-fit places the Pod on node-1 (node-0 tainted, node-1 fits 2<=4 first).
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", cpuPodJSON(t, "mover", "2")).Body.Close()

	if got := podPlacements(t, base, "default")["mover"]; got.node != "cloudemu-node-1" || got.phase != "Running" {
		t.Fatalf("mover before delete: node=%q phase=%q, want cloudemu-node-1/Running", got.node, got.phase)
	}

	// Delete the node it runs on -> it must reschedule onto the remaining worker.
	resp := do(t, http.MethodDelete, base+"/api/v1/nodes/cloudemu-node-1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("node delete status: got %d, want 200", resp.StatusCode)
	}

	resp.Body.Close()

	if got := podPlacements(t, base, "default")["mover"]; got.node != "cloudemu-node-2" || got.phase != "Running" {
		t.Fatalf("mover after delete: node=%q phase=%q, want cloudemu-node-2/Running", got.node, got.phase)
	}
}

func TestDynamicNode_DeleteWithNoRemainingFitGoesPending(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3) // node-0 (tainted), node-1 + node-2 (workers, 4 CPU)
	defer done()

	// Fill node-1 (first-fit), then place "solo" on node-2. Both workers are now
	// full; the control-plane node stays tainted.
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", cpuPodJSON(t, "block", "4")).Body.Close()
	do(t, http.MethodPost, base+"/api/v1/namespaces/default/pods", cpuPodJSON(t, "solo", "4")).Body.Close()

	if got := podPlacements(t, base, "default")["solo"]; got.node != "cloudemu-node-2" {
		t.Fatalf("solo before delete: node=%q, want cloudemu-node-2", got.node)
	}

	// Delete solo's node. Two nodes remain (still multi-node): the control-plane
	// node is tainted and node-1 is full, so solo falls back to Pending.
	do(t, http.MethodDelete, base+"/api/v1/nodes/cloudemu-node-2", nil).Body.Close()

	if got := podPlacements(t, base, "default")["solo"]; got.phase != "Pending" || got.node != "" {
		t.Fatalf("solo after delete: phase=%q node=%q, want Pending/unscheduled", got.phase, got.node)
	}
}

func TestDynamicNode_DeleteRemovesNodePinnedDaemonSetPod(t *testing.T) {
	base, done := newMultiNodeFixture(t, 3) // node-0 (tainted), node-1 + node-2 (workers)
	defer done()

	// A DaemonSet fans one Pod onto each worker (agent-cloudemu-node-1/-2).
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
	do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/daemonsets", ds).Body.Close()

	if _, ok := podPlacements(t, base, "default")["agent-cloudemu-node-1"]; !ok {
		t.Fatalf("DaemonSet Pod agent-cloudemu-node-1 should exist before the node is deleted")
	}

	// Deleting the node removes its node-pinned DaemonSet Pod (not rescheduled:
	// DaemonSet Pods are one-per-node, and the node is gone).
	do(t, http.MethodDelete, base+"/api/v1/nodes/cloudemu-node-1", nil).Body.Close()

	pods := podPlacements(t, base, "default")
	if _, ok := pods["agent-cloudemu-node-1"]; ok {
		t.Fatalf("DaemonSet Pod agent-cloudemu-node-1 must be deleted with its node")
	}

	if _, ok := pods["agent-cloudemu-node-2"]; !ok {
		t.Fatalf("DaemonSet Pod on the surviving worker must remain")
	}
}
