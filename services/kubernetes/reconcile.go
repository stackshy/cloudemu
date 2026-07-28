package kubernetes

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// cloudemu has no scheduler or kubelet, so the reconciler runs synchronously on
// every write and drives objects straight to their healthy terminal state:
// controllers materialize Running Pods, Services get Endpoints. This makes the
// data plane behave like a tiny always-converged cluster (minikube-like) rather
// than a bare object store, while staying deterministic for tests.

const (
	nodeHostIP = "10.0.0.1"
	nodeName   = "cloudemu-node-0"
)

// allocatePodIPLocked hands out the next synthetic Pod IP. Callers hold s.mu.
func (s *ClusterState) allocatePodIPLocked() string {
	n := s.nextPodIP
	s.nextPodIP++

	return fmt.Sprintf("10.244.%d.%d", (n>>8)&0xff, n&0xff)
}

// markPodRunningLocked synthesizes the status of a scheduled, running Pod: a Pod
// IP, ready containers, and the standard conditions. Terminal Pods are left
// alone. Callers hold s.mu.
func (s *ClusterState) markPodRunningLocked(pod *corev1.Pod) {
	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return
	}

	now := metav1.NewTime(time.Now())

	if pod.Status.PodIP == "" {
		ip := s.allocatePodIPLocked()
		pod.Status.PodIP = ip
		pod.Status.PodIPs = []corev1.PodIP{{IP: ip}}
	}

	pod.Status.Phase = corev1.PodRunning
	pod.Status.HostIP = nodeHostIP
	if pod.Spec.NodeName == "" {
		pod.Spec.NodeName = nodeName
	}
	if pod.Status.StartTime == nil {
		pod.Status.StartTime = &now
	}

	pod.Status.Conditions = []corev1.PodCondition{
		{Type: corev1.PodInitialized, Status: corev1.ConditionTrue, LastTransitionTime: now},
		{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: now},
		{Type: corev1.ContainersReady, Status: corev1.ConditionTrue, LastTransitionTime: now},
		{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: now},
	}

	statuses := make([]corev1.ContainerStatus, 0, len(pod.Spec.Containers))
	for _, c := range pod.Spec.Containers {
		statuses = append(statuses, corev1.ContainerStatus{
			Name:        c.Name,
			Image:       c.Image,
			ImageID:     "cloudemu://" + c.Image,
			ContainerID: "containerd://" + newUID(),
			Ready:       true,
			Started:     boolPtr(true),
			State:       corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: now}},
		})
	}
	pod.Status.ContainerStatuses = statuses
}

// buildControllerPod builds a Running Pod from a controller's PodTemplateSpec,
// owned by that controller. Callers hold s.mu.
func (s *ClusterState) buildControllerPod(
	namespace, name string, tmpl corev1.PodTemplateSpec, owner metav1.OwnerReference,
) *corev1.Pod {
	// Copy the template labels so stamping pod-template-hash can't mutate the
	// caller's (the controller's) shared template map, and record the hash so a
	// later template change is detected as a rolling update.
	labels := make(map[string]string, len(tmpl.Labels)+1)
	for k, v := range tmpl.Labels {
		labels[k] = v
	}
	labels[podTemplateHashLabel] = podTemplateHash(tmpl)

	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			UID:               types.UID(newUID()),
			CreationTimestamp: metav1.NewTime(time.Now()),
			ResourceVersion:   "1",
			Labels:            labels,
			Annotations:       tmpl.Annotations,
			OwnerReferences:   []metav1.OwnerReference{owner},
		},
		Spec: *tmpl.Spec.DeepCopy(),
	}
	s.markPodRunningLocked(pod)

	return pod
}

// podTemplateHashLabel mirrors the upstream label a ReplicaSet stamps on its
// Pods; a change to the pod template changes the hash, which the reconciler
// treats as a rolling update — stale-hash Pods are replaced.
const podTemplateHashLabel = "pod-template-hash"

func podTemplateHash(tmpl corev1.PodTemplateSpec) string {
	// Hash only the spec: label/annotation churn that doesn't change what runs
	// should not trigger a rollout, matching upstream's collision-avoidance
	// intent closely enough for an emulator.
	b, err := json.Marshal(tmpl.Spec)
	if err != nil {
		return "0"
	}

	h := fnv.New32a()
	_, _ = h.Write(b)

	return strconv.FormatUint(uint64(h.Sum32()), 16)
}

// podsOwnedByLocked returns namespace Pods controlled by owner, sorted by name.
// Callers hold s.mu.
func (s *ClusterState) podsOwnedByLocked(namespace string, owner types.UID) []*corev1.Pod {
	var owned []*corev1.Pod

	for _, pod := range s.pods {
		if pod.Namespace == namespace && ownedBy(pod.OwnerReferences, owner) {
			owned = append(owned, pod)
		}
	}

	sort.Slice(owned, func(i, j int) bool { return owned[i].Name < owned[j].Name })

	return owned
}

// syncScaledPods brings the owner's Pods to desired count using random-suffixed
// names (ReplicaSet / Deployment semantics). Returns the live count.
func (s *ClusterState) syncScaledPods(
	namespace, baseName string, owner metav1.OwnerReference, tmpl corev1.PodTemplateSpec, desired int,
) int {
	// Rolling update: drop Pods whose template hash no longer matches the
	// controller's current template, so the top-up below recreates them on the
	// new template. cloudemu converges instantly (no surge/unavailable pacing).
	hash := podTemplateHash(tmpl)
	for _, pod := range s.podsOwnedByLocked(namespace, owner.UID) {
		if pod.Labels[podTemplateHashLabel] != hash {
			delete(s.pods, podKey(namespace, pod.Name))
			s.wPods.publish(EventDeleted, namespace, *pod.DeepCopy())
		}
	}

	owned := s.podsOwnedByLocked(namespace, owner.UID)

	for len(owned) > desired {
		last := owned[len(owned)-1]
		delete(s.pods, podKey(namespace, last.Name))
		s.wPods.publish(EventDeleted, namespace, *last.DeepCopy())
		owned = owned[:len(owned)-1]
	}

	for len(owned) < desired {
		pod := s.buildControllerPod(namespace, baseName+"-"+shortID(), tmpl, owner)
		s.pods[podKey(namespace, pod.Name)] = pod
		s.wPods.publish(EventAdded, namespace, *pod.DeepCopy())
		owned = append(owned, pod)
	}

	return len(owned)
}

// syncStablePods reconciles the owner's Pods to exactly the named set (stable
// identity — StatefulSet / DaemonSet). Returns the live count.
func (s *ClusterState) syncStablePods(
	namespace string, owner metav1.OwnerReference, tmpl corev1.PodTemplateSpec, names []string,
) int {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}

	hash := podTemplateHash(tmpl)
	for _, pod := range s.podsOwnedByLocked(namespace, owner.UID) {
		// Delete Pods that are no longer wanted (scale-down) OR whose template
		// hash is stale (rolling update) so the loop below recreates them.
		if !want[pod.Name] || pod.Labels[podTemplateHashLabel] != hash {
			delete(s.pods, podKey(namespace, pod.Name))
			s.wPods.publish(EventDeleted, namespace, *pod.DeepCopy())
		}
	}

	for _, n := range names {
		if _, ok := s.pods[podKey(namespace, n)]; !ok {
			pod := s.buildControllerPod(namespace, n, tmpl, owner)
			s.pods[podKey(namespace, n)] = pod
			s.wPods.publish(EventAdded, namespace, *pod.DeepCopy())
		}
	}

	return len(names)
}

// --- Endpoints controller ---------------------------------------------------

// resyncEndpointsForNamespaceLocked recomputes Endpoints for every selector
// Service in the namespace. Callers hold s.mu.
func (s *ClusterState) resyncEndpointsForNamespaceLocked(namespace string) {
	for _, svc := range s.services {
		if svc.Namespace == namespace {
			s.reconcileServiceEndpointsLocked(svc)
		}
	}
}

// reconcileServiceEndpointsLocked fills a Service's Endpoints from the Running
// Pods matching its selector. Selectorless Services are left to the user.
func (s *ClusterState) reconcileServiceEndpointsLocked(svc *corev1.Service) {
	if len(svc.Spec.Selector) == 0 {
		return
	}

	var addrs []corev1.EndpointAddress

	for _, pod := range s.pods {
		if pod.Namespace != svc.Namespace || pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" {
			continue
		}
		if !labelsMatch(svc.Spec.Selector, pod.Labels) {
			continue
		}

		p := pod
		addrs = append(addrs, corev1.EndpointAddress{
			IP:        p.Status.PodIP,
			NodeName:  strPtr(nodeName),
			TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: p.Namespace, Name: p.Name, UID: p.UID},
		})
	}

	sort.Slice(addrs, func(i, j int) bool { return addrs[i].IP < addrs[j].IP })

	key := endpointsKey(svc.Namespace, svc.Name)

	ep := s.endpoints[key]
	if ep == nil {
		ep = newEndpointsObject(svc.Namespace, svc.Name)
	}

	if len(addrs) == 0 {
		ep.Subsets = nil
	} else {
		ep.Subsets = []corev1.EndpointSubset{{Addresses: addrs, Ports: endpointPorts(svc)}}
	}

	ep.ResourceVersion = bumpResourceVersion(ep.ResourceVersion)
	s.endpoints[key] = ep
	s.wEndpoints.publish(EventModified, svc.Namespace, *ep.DeepCopy())
}

func endpointPorts(svc *corev1.Service) []corev1.EndpointPort {
	ports := make([]corev1.EndpointPort, 0, len(svc.Spec.Ports))

	for _, sp := range svc.Spec.Ports {
		port := sp.Port
		if sp.TargetPort.IntValue() != 0 {
			port = int32(sp.TargetPort.IntValue()) //nolint:gosec // port fits int32.
		}

		proto := sp.Protocol
		if proto == "" {
			proto = corev1.ProtocolTCP
		}

		ports = append(ports, corev1.EndpointPort{Name: sp.Name, Port: port, Protocol: proto})
	}

	return ports
}

func labelsMatch(selector, labels map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}

	return true
}

// --- Typed Deployment reconcile ---------------------------------------------

// reconcileDeploymentLocked brings the Pods owned by dep to its desired count
// and refreshes dep.Status, then resyncs Service endpoints. (The intermediate
// ReplicaSet object is not yet materialized; Pods are owned by the Deployment
// directly — a documented simplification.) Callers hold s.mu.
func (s *ClusterState) reconcileDeploymentLocked(dep *appsv1.Deployment) {
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}

	owner := metav1.OwnerReference{
		APIVersion: "apps/v1", Kind: "Deployment", Name: dep.Name, UID: dep.UID,
		Controller: boolPtr(true), BlockOwnerDeletion: boolPtr(true),
	}

	ready := int32(s.syncScaledPods(dep.Namespace, dep.Name, owner, dep.Spec.Template, int(desired))) //nolint:gosec

	dep.Status.Replicas = ready
	dep.Status.ReadyReplicas = ready
	dep.Status.AvailableReplicas = ready
	dep.Status.UpdatedReplicas = ready
	dep.Status.ObservedGeneration = dep.Generation
	dep.Status.Conditions = []appsv1.DeploymentCondition{
		{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue, Reason: "MinimumReplicasAvailable"},
		{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionTrue, Reason: "NewReplicaSetAvailable"},
	}

	s.resyncEndpointsForNamespaceLocked(dep.Namespace)
}

// --- Registry reconcile hooks (apps/v1 workloads + PVC) ----------------------

func reconcileReplicaSet(s *ClusterState, obj *unstructured.Unstructured) {
	ready := s.syncScaledPods(obj.GetNamespace(), obj.GetName(), ownerRefOf(obj),
		podTemplateFromUnstructured(obj), replicasOf(obj))
	setWorkloadStatus(obj, ready)
	s.resyncEndpointsForNamespaceLocked(obj.GetNamespace())
}

func reconcileStatefulSet(s *ClusterState, obj *unstructured.Unstructured) {
	desired := replicasOf(obj)

	names := make([]string, desired)
	for i := range names {
		names[i] = fmt.Sprintf("%s-%d", obj.GetName(), i)
	}

	s.syncStatefulSetPVCsLocked(obj, desired)
	ready := s.syncStablePods(obj.GetNamespace(), ownerRefOf(obj), podTemplateFromUnstructured(obj), names)
	setWorkloadStatus(obj, ready)
	s.resyncEndpointsForNamespaceLocked(obj.GetNamespace())
}

func reconcileDaemonSet(s *ClusterState, obj *unstructured.Unstructured) {
	names := []string{obj.GetName() + "-" + nodeName}
	ready := int64(s.syncStablePods(obj.GetNamespace(), ownerRefOf(obj), podTemplateFromUnstructured(obj), names))

	set := func(field string) { _ = unstructured.SetNestedField(obj.Object, ready, "status", field) }
	set("desiredNumberScheduled")
	set("currentNumberScheduled")
	set("numberReady")
	set("numberAvailable")
	_ = unstructured.SetNestedField(obj.Object, obj.GetGeneration(), "status", "observedGeneration")

	s.resyncEndpointsForNamespaceLocked(obj.GetNamespace())
}

// reconcilePVC marks a PersistentVolumeClaim Bound — cloudemu dynamically
// "provisions" storage immediately (there is no real volume plugin).
func reconcilePVC(_ *ClusterState, obj *unstructured.Unstructured) {
	if phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase"); phase == "" {
		_ = unstructured.SetNestedField(obj.Object, "Bound", "status", "phase")
	}
}

// reconcilePV marks a PersistentVolume Available (there is no real storage
// backend to bind against unless a claim already references it).
func reconcilePV(_ *ClusterState, obj *unstructured.Unstructured) {
	if phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase"); phase == "" {
		_ = unstructured.SetNestedField(obj.Object, "Available", "status", "phase")
	}
}

// ingressLBIP is the synthetic external IP every emulated Ingress and
// LoadBalancer Service is assigned (TEST-NET-3, RFC 5737).
const ingressLBIP = "203.0.113.10"

// reconcileIngress assigns the Ingress a load-balancer address so
// status.loadBalancer.ingress is populated, as a real ingress controller would.
func reconcileIngress(_ *ClusterState, obj *unstructured.Unstructured) {
	_ = unstructured.SetNestedSlice(obj.Object,
		[]any{map[string]any{"ip": ingressLBIP}}, "status", "loadBalancer", "ingress")
}

// reconcileJob runs a Job to completion: it creates `completions` Pods (default
// 1) that go straight to Succeeded, and marks the Job Complete.
func reconcileJob(s *ClusterState, obj *unstructured.Unstructured) {
	completions := int64(1)
	if c, found, _ := unstructured.NestedInt64(obj.Object, "spec", "completions"); found {
		completions = c
	}

	ns := obj.GetNamespace()
	owner := ownerRefOf(obj)
	tmpl := podTemplateFromUnstructured(obj)

	for len(s.podsOwnedByLocked(ns, owner.UID)) < int(completions) {
		pod := s.buildControllerPod(ns, obj.GetName()+"-"+shortID(), tmpl, owner)
		s.markPodSucceededLocked(pod)
		s.pods[podKey(ns, pod.Name)] = pod
		s.wPods.publish(EventAdded, ns, *pod.DeepCopy())
	}

	succeeded := int64(len(s.podsOwnedByLocked(ns, owner.UID)))
	_ = unstructured.SetNestedField(obj.Object, succeeded, "status", "succeeded")
	_ = unstructured.SetNestedField(obj.Object, int64(0), "status", "active")
	_ = unstructured.SetNestedSlice(obj.Object,
		[]any{map[string]any{"type": "Complete", "status": "True"}}, "status", "conditions")
}

// markPodSucceededLocked drives a Pod to the completed (Succeeded) terminal
// state used by Job pods. Callers hold s.mu.
func (s *ClusterState) markPodSucceededLocked(pod *corev1.Pod) {
	s.markPodRunningLocked(pod)

	now := metav1.NewTime(time.Now())
	pod.Status.Phase = corev1.PodSucceeded

	for i := range pod.Status.ContainerStatuses {
		pod.Status.ContainerStatuses[i].Ready = false
		pod.Status.ContainerStatuses[i].State = corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{
				ExitCode: 0, Reason: "Completed", StartedAt: now, FinishedAt: now,
			},
		}
	}
}

// syncStatefulSetPVCsLocked creates a Bound PVC per (volumeClaimTemplate,
// ordinal) named "<template>-<sts>-<ordinal>", owned by the StatefulSet.
func (s *ClusterState) syncStatefulSetPVCsLocked(sts *unstructured.Unstructured, replicas int) {
	templates, found, _ := unstructured.NestedSlice(sts.Object, "spec", "volumeClaimTemplates")
	if !found {
		return
	}

	store := s.reg.stores[regKey("", "v1", "persistentvolumeclaims")]
	if store == nil {
		return
	}

	for _, raw := range templates {
		tmpl, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		tmplName, _, _ := unstructured.NestedString(tmpl, "metadata", "name")
		spec, _, _ := unstructured.NestedMap(tmpl, "spec")

		for i := range replicas {
			name := fmt.Sprintf("%s-%s-%d", tmplName, sts.GetName(), i)
			key := objKey(sts.GetNamespace(), name)
			if _, exists := store.items[key]; exists {
				continue
			}

			pvc := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "PersistentVolumeClaim",
				"metadata": map[string]any{
					"name":      name,
					"namespace": sts.GetNamespace(),
				},
				"spec":   spec,
				"status": map[string]any{"phase": "Bound"},
			}}
			pvc.SetUID(types.UID(newUID()))
			pvc.SetCreationTimestamp(metav1.NewTime(time.Now()))
			pvc.SetOwnerReferences([]metav1.OwnerReference{ownerRefOf(sts)})
			store.stampRVLocked(pvc)
			store.items[key] = pvc
			store.watch.publish(EventAdded, sts.GetNamespace(), *pvc.DeepCopy())
		}
	}
}

// --- unstructured workload helpers ------------------------------------------

func ownerRefOf(obj *unstructured.Unstructured) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Name:       obj.GetName(),
		UID:        obj.GetUID(),
		Controller: boolPtr(true), BlockOwnerDeletion: boolPtr(true),
	}
}

func replicasOf(obj *unstructured.Unstructured) int {
	n, found, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas")
	if !found {
		return 1
	}

	return int(n)
}

func podTemplateFromUnstructured(obj *unstructured.Unstructured) corev1.PodTemplateSpec {
	var tmpl corev1.PodTemplateSpec

	m, found, _ := unstructured.NestedMap(obj.Object, "spec", "template")
	if found {
		_ = runtime.DefaultUnstructuredConverter.FromUnstructured(m, &tmpl)
	}

	return tmpl
}

// setWorkloadStatus mirrors the live replica count onto the standard workload
// status fields (ReplicaSet/StatefulSet share these names).
func setWorkloadStatus(obj *unstructured.Unstructured, ready int) {
	r := int64(ready)
	for _, f := range []string{"replicas", "readyReplicas", "availableReplicas", "currentReplicas", "updatedReplicas"} {
		_ = unstructured.SetNestedField(obj.Object, r, "status", f)
	}
	_ = unstructured.SetNestedField(obj.Object, obj.GetGeneration(), "status", "observedGeneration")
}

// shortID returns a short random suffix for controller-generated Pod names.
func shortID() string { return newUID()[:10] }

func boolPtr(b bool) *bool { return &b }

func strPtr(s string) *string { return &s }
