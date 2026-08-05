// Test for Finding 10: docs/services.md §18 claims a Deployment pod-template
// change "creates a new ReplicaSet (a real rolling update) and retires the
// old one" — but no test asserted a second ReplicaSet actually gets created
// on a template change (only single-revision creation, in
// phase3_controllers_test.go's TestDeployment_InterposesReplicaSet, was
// covered). This exercises the roll-to-new-RS path end to end and records
// what "retires" actually means in the implementation (services/kubernetes/
// deployment_rs.go: pruneStaleDeploymentRSLocked deletes the old ReplicaSet
// outright — it does not scale it to zero and keep it around for rollback).

package kubernetes_test

import (
	"net/http"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// listDeploymentReplicaSets fetches every ReplicaSet in the default namespace.
func listDeploymentReplicaSets(t *testing.T, base string) appsv1.ReplicaSetList {
	t.Helper()

	resp := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/replicasets", nil)
	defer resp.Body.Close()

	var list appsv1.ReplicaSetList
	mustDecode(t, resp.Body, &list)

	return list
}

// listDeploymentPods fetches every Pod in the default namespace.
func listDeploymentPods(t *testing.T, base string) corev1.PodList {
	t.Helper()

	resp := do(t, http.MethodGet, base+"/api/v1/namespaces/default/pods", nil)
	defer resp.Body.Close()

	var list corev1.PodList
	mustDecode(t, resp.Body, &list)

	return list
}

// TestDeployment_TemplateChangeRollsToNewReplicaSet drives the exact scenario
// §18 describes: create a Deployment, confirm it interposes exactly one
// ReplicaSet, then change the pod template (container image) and confirm a
// second, new-revision ReplicaSet is created, the old one is gone (deleted,
// not scaled to zero), and the Pods now belong to the new ReplicaSet.
func TestDeployment_TemplateChangeRollsToNewReplicaSet(t *testing.T) {
	base, cleanup := newFixture(t)
	t.Cleanup(cleanup)

	dep := makeDeployment("web", 2)

	resp := do(t, http.MethodPost, base+"/apis/apps/v1/namespaces/default/deployments", mustJSON(t, dep))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// Step 1: exactly one ReplicaSet exists for the initial revision.
	initial := listDeploymentReplicaSets(t, base)
	if len(initial.Items) != 1 {
		t.Fatalf("initial RS count: got %d, want 1", len(initial.Items))
	}

	firstRS := initial.Items[0]
	firstRSUID := firstRS.UID

	initialPods := listDeploymentPods(t, base)
	if len(initialPods.Items) != 2 {
		t.Fatalf("initial pod count: got %d, want 2", len(initialPods.Items))
	}

	for i := range initialPods.Items {
		if !ownedByUID(initialPods.Items[i].OwnerReferences, firstRSUID) {
			t.Fatalf("pod %s not owned by initial RS %s", initialPods.Items[i].Name, firstRSUID)
		}
	}

	// Step 2: change the pod template (a new image is a template change, just
	// like a real `kubectl set image`), which must trigger a rolling update.
	dep.Spec.Template.Spec.Containers[0].Image = "nginx:1.28"

	resp = do(t, http.MethodPut, base+"/apis/apps/v1/namespaces/default/deployments/web", mustJSON(t, dep))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: got %d", resp.StatusCode)
	}

	resp.Body.Close()

	// A second, new-revision ReplicaSet must now exist — and only it: the
	// old RS is retired by deletion (pruneStaleDeploymentRSLocked), not by
	// scaling to zero, so there is exactly one RS again, but it is NOT the
	// same object as before.
	afterRollout := listDeploymentReplicaSets(t, base)
	if len(afterRollout.Items) != 1 {
		t.Fatalf("post-rollout RS count: got %d, want 1 (old RS should be deleted, not kept at 0 replicas)",
			len(afterRollout.Items))
	}

	newRS := afterRollout.Items[0]
	if newRS.UID == firstRSUID {
		t.Fatalf("post-rollout RS is the same object (UID %s) as the pre-rollout RS — no new ReplicaSet was created",
			newRS.UID)
	}

	if newRS.Name == firstRS.Name {
		t.Fatalf("post-rollout RS name %q unchanged — template-hash naming did not roll", newRS.Name)
	}

	// The old ReplicaSet is gone entirely (deleted, confirming "retires" in
	// the docs means delete, not scale-to-zero-and-keep-for-rollback).
	oldRS := do(t, http.MethodGet, base+"/apis/apps/v1/namespaces/default/replicasets/"+firstRS.Name, nil)
	defer oldRS.Body.Close()

	if oldRS.StatusCode != http.StatusNotFound {
		t.Fatalf("old RS %s: got %d, want 404 (deleted) — if this is ever 200, the docs' \"retires\" wording"+
			" (implying rollback support) becomes accurate and should stop being softened", firstRS.Name, oldRS.StatusCode)
	}

	// Pods now belong to the new ReplicaSet, and only the new one.
	afterPods := listDeploymentPods(t, base)
	if len(afterPods.Items) != 2 {
		t.Fatalf("post-rollout pod count: got %d, want 2", len(afterPods.Items))
	}

	for i := range afterPods.Items {
		pod := afterPods.Items[i]
		if !ownedByUID(pod.OwnerReferences, newRS.UID) {
			t.Fatalf("pod %s not owned by new RS %s", pod.Name, newRS.UID)
		}

		if ownedByUID(pod.OwnerReferences, firstRSUID) {
			t.Fatalf("pod %s still owned by the retired RS %s", pod.Name, firstRSUID)
		}

		if pod.Spec.Containers[0].Image != "nginx:1.28" {
			t.Fatalf("pod %s image: got %q, want nginx:1.28", pod.Name, pod.Spec.Containers[0].Image)
		}
	}
}

// ownedByUID reports whether refs contains an owner reference to uid.
func ownedByUID(refs []metav1.OwnerReference, uid types.UID) bool {
	for _, ref := range refs {
		if ref.UID == uid {
			return true
		}
	}

	return false
}
