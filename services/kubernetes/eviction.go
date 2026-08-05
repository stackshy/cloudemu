package kubernetes

import (
	"fmt"
	"io"
	"math"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// subresourceEviction is the pods subresource a real apiserver serves at
// POST .../pods/{name}/eviction (policy/v1 Eviction). It has no group of its
// own — it hangs off the core Pod resource, same as /log and /exec would.
const subresourceEviction = "eviction"

// evictPod handles POST .../namespaces/{ns}/pods/{name}/eviction: it deletes
// the named Pod unless doing so would violate a PodDisruptionBudget whose
// selector matches it, in which case it responds 429 Too Many Requests and
// leaves the Pod in place — mirroring the real apiserver's eviction handler.
func (s *ClusterState) evictPod(w http.ResponseWriter, r *http.Request, namespace, name string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, "k8s api: pods/eviction: method not allowed: "+r.Method)

		return
	}

	// The Eviction body (policy/v1 Eviction, carrying only ObjectMeta and
	// optional DeleteOptions) adds nothing this handler needs — namespace and
	// name already come from the URL — but real clients send one, so it must
	// be drained rather than left to leak the connection.
	_, _ = io.Copy(io.Discard, r.Body)

	s.mu.Lock()
	defer s.mu.Unlock()

	key := podKey(namespace, name)

	pod, ok := s.pods[key]
	if !ok {
		writeNotFound(w, "k8s api: pod not found: "+key)

		return
	}

	if status := s.checkPDBAllowsEvictionLocked(namespace, pod); status != nil {
		writeJSON(w, http.StatusTooManyRequests, status)

		return
	}

	delete(s.pods, key)
	s.resyncEndpointsForNamespaceLocked(namespace)
	s.wPods.publish(EventDeleted, namespace, *pod.DeepCopy())

	writeJSON(w, http.StatusOK, &metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusSuccess,
		Code:     http.StatusOK,
		Message:  "eviction complete",
	})
}

// checkPDBAllowsEvictionLocked returns a non-nil 429 Status if evicting pod
// would violate a PodDisruptionBudget in namespace whose selector matches it.
// Every matching PDB's status is refreshed with the current observed counts
// regardless of the outcome, mirroring what the (absent) disruption
// controller would compute. Callers hold s.mu.
func (s *ClusterState) checkPDBAllowsEvictionLocked(namespace string, pod *corev1.Pod) *metav1.Status {
	var blocked *metav1.Status

	for _, pdb := range s.pdbs {
		if pdb.Namespace != namespace || pdb.Spec.Selector == nil {
			continue
		}

		sel, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil || !sel.Matches(labels.Set(pod.Labels)) {
			continue
		}

		expected, healthy := s.matchingPodCountsLocked(namespace, sel)
		desiredHealthy, allowed := disruptionBudget(pdb, expected, healthy)

		updatePDBStatusLocked(pdb, healthy, desiredHealthy, allowed, expected)

		if allowed <= 0 && blocked == nil {
			blocked = pdbBlockedStatus(pdb.Name, desiredHealthy, healthy)
		}
	}

	return blocked
}

// matchingPodCountsLocked returns, among namespace's non-terminal Pods
// matching sel: expected (the total count) and healthy (those Running and
// Ready) — the inputs a PodDisruptionBudget's status is computed from.
// Callers hold s.mu.
func (s *ClusterState) matchingPodCountsLocked(namespace string, sel labels.Selector) (expected, healthy int) {
	for _, p := range s.pods {
		if p.Namespace != namespace || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}

		if !sel.Matches(labels.Set(p.Labels)) {
			continue
		}

		expected++

		if p.Status.Phase == corev1.PodRunning && podReady(p) {
			healthy++
		}
	}

	return expected, healthy
}

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}

	return false
}

// disruptionBudget resolves a PDB's minAvailable/maxUnavailable (int or
// percent of expected) into the desired healthy count and how many further
// disruptions are currently allowed against it.
func disruptionBudget(pdb *policyv1.PodDisruptionBudget, expected, healthy int) (desiredHealthy, allowed int) {
	switch {
	case pdb.Spec.MinAvailable != nil:
		v, err := intstr.GetScaledValueFromIntOrPercent(pdb.Spec.MinAvailable, expected, true)
		if err != nil {
			v = expected
		}

		desiredHealthy = v
	case pdb.Spec.MaxUnavailable != nil:
		v, err := intstr.GetScaledValueFromIntOrPercent(pdb.Spec.MaxUnavailable, expected, false)
		if err != nil {
			v = 0
		}

		desiredHealthy = expected - v
	default:
		desiredHealthy = 0
	}

	return desiredHealthy, healthy - desiredHealthy
}

// updatePDBStatusLocked mirrors the observed counts onto the PDB's status, the
// same fields the (absent) disruption controller would keep in sync. Callers
// hold s.mu.
func updatePDBStatusLocked(pdb *policyv1.PodDisruptionBudget, healthy, desiredHealthy, allowed, expected int) {
	pdb.Status.CurrentHealthy = int32(healthy)        //nolint:gosec // pod counts are far below int32 range
	pdb.Status.DesiredHealthy = int32(desiredHealthy) //nolint:gosec // pod counts are far below int32 range
	pdb.Status.ExpectedPods = int32(expected)         //nolint:gosec // pod counts are far below int32 range
	pdb.Status.DisruptionsAllowed = clampToInt32(allowed)
	pdb.Status.ObservedGeneration = pdb.Generation
}

// clampToInt32 narrows a disruption count to int32, clamping to [0,
// math.MaxInt32]. allowed can go negative when a PDB is already violated
// (more disruptions have happened than the budget permits), which the 429
// decision in checkPDBAllowsEvictionLocked relies on seeing as "no budget
// left" rather than a negative DisruptionsAllowed on the wire — matching
// what a real PodDisruptionBudgetStatus reports. The upper clamp makes the
// int->int32 narrowing a deliberate, bound-checked conversion instead of
// gosec G115's unchecked one (mirrors safeInt32 in server/aws/eks/operations.go).
func clampToInt32(v int) int32 {
	switch {
	case v < 0:
		return 0
	case v > math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(v)
	}
}

// pdbBlockedStatus builds the 429 Status a real apiserver returns when an
// eviction would violate a PodDisruptionBudget.
func pdbBlockedStatus(pdbName string, desiredHealthy, healthy int) *metav1.Status {
	msg := fmt.Sprintf(
		"Cannot evict pod as it would violate the pod's disruption budget %q: needs %d healthy pods, has %d",
		pdbName, desiredHealthy, healthy,
	)

	return &metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Code:     http.StatusTooManyRequests,
		Reason:   metav1.StatusReasonTooManyRequests,
		Message:  msg,
	}
}
