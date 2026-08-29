package kubernetes

import (
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// Opt-in staged Pod lifecycle (#874). With progression OFF (the default), a
// directly-created Pod is driven straight to Running — the synchronous behavior
// every existing test relies on. With progression ON, a bare Pod starts Pending
// and advances Pending -> ContainerCreating -> Running (and, on delete,
// Terminating -> gone) one stage per Tick(), each stage a distinct RV-bumping
// write + watch event + kubelet Event, all on the cluster clock so it stays
// deterministic under a FakeClock. Controller-materialized Pods (Deployment/RS/
// STS/DS) still come up Running immediately — only client-created Pods stage —
// so a `kubectl scale` still shows Running replicas at once.

const (
	// lifecycleStageAnn / lifecycleNextAnn track a staged Pod's current stage and
	// the cluster-clock time its next transition is due.
	lifecycleStageAnn = "cloudemu.io/lifecycle-stage"
	lifecycleNextAnn  = "cloudemu.io/lifecycle-next"

	stagePending           = "Pending"
	stageContainerCreating = "ContainerCreating"
	stageRunning           = "Running"
	stageTerminating       = "Terminating"

	// stageInterval is the cluster-clock time a Pod dwells in each pre-Running
	// stage. A test advances its FakeClock by at least this between Ticks; live
	// `serve` ticks on the real clock.
	stageInterval = time.Second
)

// initPendingPodLocked starts a client-created Pod in the staged lifecycle:
// Pending, scheduled onto the node (Scheduled event), with its first transition
// due one stageInterval out. Callers hold s.mu.
func (s *ClusterState) initPendingPodLocked(pod *corev1.Pod) {
	now := s.now()
	pod.Status.Phase = corev1.PodPending
	s.scheduleNodeLocked(pod)
	pod.Status.Conditions = []corev1.PodCondition{
		{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: now},
	}
	setStageLocked(pod, stagePending, now.Time.Add(stageInterval))
}

// Tick advances every staged Pod whose next transition is due. Tests call it
// explicitly after advancing a FakeClock; the serve ticker calls it on the real
// clock. A no-op when progression is off. Safe for external callers.
func (s *ClusterState) Tick() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.lifecycleProgression {
		return
	}

	now := s.now().Time

	keys := make([]string, 0, len(s.pods))
	for k := range s.pods {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	touched := map[string]bool{}

	for _, k := range keys {
		pod, ok := s.pods[k]
		if !ok {
			continue
		}

		if s.advancePodLocked(pod, now) {
			touched[pod.Namespace] = true
		}
	}

	// A Pod reaching Running (or being reaped) changes which addresses back a
	// Service — refresh endpoints in every namespace we touched.
	for ns := range touched {
		s.resyncEndpointsForNamespaceLocked(ns)
	}
}

// advancePodLocked moves one Pod forward by at most one stage if its transition
// is due, returning true when the change may affect Service endpoints (a Pod
// reached Running or was reaped). Callers hold s.mu.
func (s *ClusterState) advancePodLocked(pod *corev1.Pod, now time.Time) bool {
	if pod.DeletionTimestamp != nil {
		return s.reapTerminatingLocked(pod, now)
	}

	stage := pod.Annotations[lifecycleStageAnn]
	if stage == "" || stage == stageRunning {
		return false
	}

	if !stageDue(pod, now) {
		return false
	}

	switch stage {
	case stagePending:
		s.toContainerCreatingLocked(pod, now)

		return false
	case stageContainerCreating:
		s.toRunningLocked(pod)

		return true
	default:
		return false
	}
}

// toContainerCreatingLocked advances a Pending Pod to ContainerCreating: its
// containers report a Waiting/ContainerCreating state and the kubelet emits a
// Pulling event. Callers hold s.mu.
func (s *ClusterState) toContainerCreatingLocked(pod *corev1.Pod, now time.Time) {
	statuses := make([]corev1.ContainerStatus, 0, len(pod.Spec.Containers))

	for i := range pod.Spec.Containers {
		c := &pod.Spec.Containers[i]
		statuses = append(statuses, corev1.ContainerStatus{
			Name:  c.Name,
			Image: c.Image,
			Ready: false,
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: stageContainerCreating}},
		})
	}

	pod.Status.ContainerStatuses = statuses
	setStageLocked(pod, stageContainerCreating, now.Add(stageInterval))
	s.recordEventLocked(objectReferenceForPod(pod), "Pulling",
		"Pulling image for pod "+pod.Name)

	pod.ResourceVersion = s.nextClusterRVLocked()

	s.wPods.publish(EventModified, pod.Namespace, *pod.DeepCopy())
}

// toRunningLocked drives a ContainerCreating Pod to Running (Started event via
// markPodRunningLocked) and marks the lifecycle complete. Callers hold s.mu.
func (s *ClusterState) toRunningLocked(pod *corev1.Pod) {
	s.markPodRunningLocked(pod)
	setStageLocked(pod, stageRunning, time.Time{})

	pod.ResourceVersion = s.nextClusterRVLocked()

	s.wPods.publish(EventModified, pod.Namespace, *pod.DeepCopy())
}

// reapTerminatingLocked removes a Pod whose deletion grace period (one
// stageInterval) has elapsed, publishing DELETED and releasing its quota.
// Returns true when the Pod was removed. Callers hold s.mu.
func (s *ClusterState) reapTerminatingLocked(pod *corev1.Pod, now time.Time) bool {
	if pod.Annotations[lifecycleStageAnn] != stageTerminating || !stageDue(pod, now) {
		return false
	}

	delete(s.pods, podKey(pod.Namespace, pod.Name))
	s.releaseQuotaLocked(pod.Namespace, "Pod", resourcePods)
	s.wPods.publish(EventDeleted, pod.Namespace, *pod.DeepCopy())

	return true
}

// beginTerminatingLocked puts a Pod into the staged Terminating state (used by
// the delete path under progression instead of an immediate hard delete), due to
// be reaped one stageInterval later. Callers hold s.mu.
func (s *ClusterState) beginTerminatingLocked(pod *corev1.Pod) {
	now := s.now()
	pod.DeletionTimestamp = &now
	setStageLocked(pod, stageTerminating, now.Time.Add(stageInterval))

	pod.ResourceVersion = s.nextClusterRVLocked()

	s.wPods.publish(EventModified, pod.Namespace, *pod.DeepCopy())
}

// setStageLocked records a Pod's lifecycle stage and next-transition time on its
// annotations (nil next clears the timer). Callers hold s.mu.
func setStageLocked(pod *corev1.Pod, stage string, next time.Time) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	pod.Annotations[lifecycleStageAnn] = stage

	if next.IsZero() {
		delete(pod.Annotations, lifecycleNextAnn)

		return
	}

	pod.Annotations[lifecycleNextAnn] = next.UTC().Format(time.RFC3339Nano)
}

// stageDue reports whether a Pod's next-transition time has arrived.
func stageDue(pod *corev1.Pod, now time.Time) bool {
	raw := pod.Annotations[lifecycleNextAnn]
	if raw == "" {
		return true
	}

	next, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return true
	}

	return !now.Before(next)
}
