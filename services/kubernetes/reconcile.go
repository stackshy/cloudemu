package kubernetes

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"reflect"
	"sort"
	"strconv"
	"strings"

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
	// octetMask extracts the low 8 bits of a synthetic Pod-IP counter octet.
	octetMask = 0xff
)

// allocatePodIPLocked hands out the next synthetic Pod IP. Callers hold s.mu.
func (s *ClusterState) allocatePodIPLocked() string {
	n := s.nextPodIP
	s.nextPodIP++

	return fmt.Sprintf("10.244.%d.%d", (n>>8)&octetMask, n&octetMask)
}

// markPodRunningLocked synthesizes the status of a scheduled, running Pod: a Pod
// IP, ready containers, and the standard conditions. Terminal Pods are left
// alone. Callers hold s.mu.
func (s *ClusterState) markPodRunningLocked(pod *corev1.Pod) {
	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return
	}

	now := s.now()

	if pod.Status.PodIP == "" {
		ip := s.allocatePodIPLocked()
		pod.Status.PodIP = ip
		pod.Status.PodIPs = []corev1.PodIP{{IP: ip}}
	}

	s.scheduleNodeLocked(pod)

	pod.Status.Phase = corev1.PodRunning
	pod.Status.HostIP = nodeHostIP

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

	for i := range pod.Spec.Containers {
		c := &pod.Spec.Containers[i]
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

	// kubelet "Started" event (deduped per Pod). The scheduler's "Scheduled"
	// event is emitted by scheduleNodeLocked on node assignment.
	s.recordEventLocked(objectReferenceForPod(pod), "Started",
		"Started container "+firstContainerName(pod))
}

// buildControllerPod builds a Running Pod from a controller's PodTemplateSpec,
// owned by that controller. Callers hold s.mu.
//
//nolint:gocritic // hugeParam: k8s template/owner structs, copy is intentional.
func (s *ClusterState) buildControllerPod(
	namespace, name string, tmpl corev1.PodTemplateSpec, owner metav1.OwnerReference,
) *corev1.Pod {
	// Copy the template labels so stamping pod-template-hash can't mutate the
	// caller's (the controller's) shared template map, and record the hash so a
	// later template change is detected as a rolling update.
	// Capacity is a hint; the map grows to fit the extra pod-template-hash key.
	// (Avoid len()+1 arithmetic — it trips CodeQL's allocation-overflow query.)
	labels := make(map[string]string, len(tmpl.Labels))
	for k, v := range tmpl.Labels {
		labels[k] = v
	}

	labels[podTemplateHashLabel] = podTemplateHash(tmpl)

	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: kindPod, APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			UID:               types.UID(newUID()),
			CreationTimestamp: s.now(),
			ResourceVersion:   s.nextClusterRVLocked(),
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

//nolint:gocritic // hugeParam: k8s template struct, copy is intentional.
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
//
//nolint:gocritic // hugeParam: k8s template/owner structs, copy is intentional.
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
		s.recordEventLocked(objectReferenceForOwner(owner, namespace),
			"SuccessfulCreate", "Created pod: "+pod.Name)

		owned = append(owned, pod)
	}

	return len(owned)
}

// syncStablePods reconciles the owner's Pods to exactly the named set (stable
// identity — StatefulSet / DaemonSet). Returns the live count.
//
//nolint:gocritic // hugeParam: k8s template/owner structs, copy is intentional.
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
			s.recordEventLocked(objectReferenceForOwner(owner, namespace),
				"SuccessfulCreate", "Created pod: "+pod.Name)
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

	addrs := s.matchingEndpointAddressesLocked(svc)

	var subsets []corev1.EndpointSubset
	if len(addrs) > 0 {
		subsets = []corev1.EndpointSubset{{Addresses: addrs, Ports: endpointPorts(svc)}}
	}

	// Only touch the stores when the address set actually changed — resync runs
	// for every Service on any Pod change, so most calls are no-ops and must not
	// emit spurious watch events / bump ResourceVersions. The EndpointSlice
	// mirrors the same addresses, so it only needs updating when Endpoints did.
	if !s.writeEndpointsLocked(svc, subsets) {
		return
	}

	s.syncEndpointSliceLocked(svc, addrs)
}

// matchingEndpointAddressesLocked returns the ready Pod addresses a Service
// selector matches, sorted by IP for a stable (change-detectable) result.
func (s *ClusterState) matchingEndpointAddressesLocked(svc *corev1.Service) []corev1.EndpointAddress {
	addrs := make([]corev1.EndpointAddress, 0, len(s.pods))

	for _, pod := range s.pods {
		if pod.Namespace != svc.Namespace || pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" {
			continue
		}

		// A Pod being torn down (staged Terminating) is removed from Endpoints
		// before it disappears, matching real endpoint-controller behavior.
		if pod.DeletionTimestamp != nil {
			continue
		}

		if !labelsMatch(svc.Spec.Selector, pod.Labels) {
			continue
		}

		addrs = append(addrs, corev1.EndpointAddress{
			IP:        pod.Status.PodIP,
			NodeName:  strPtr(nodeName),
			TargetRef: &corev1.ObjectReference{Kind: kindPod, Namespace: pod.Namespace, Name: pod.Name, UID: pod.UID},
		})
	}

	sort.Slice(addrs, func(i, j int) bool { return addrs[i].IP < addrs[j].IP })

	return addrs
}

// writeEndpointsLocked stores the Service's Endpoints and reports whether the
// subset set changed (so callers skip spurious watch events on no-op resyncs).
func (s *ClusterState) writeEndpointsLocked(svc *corev1.Service, subsets []corev1.EndpointSubset) bool {
	key := endpointsKey(svc.Namespace, svc.Name)

	ep := s.endpoints[key]
	existed := ep != nil

	if !existed {
		ep = s.newEndpointsObject(svc.Namespace, svc.Name)
	}

	if existed && reflect.DeepEqual(ep.Subsets, subsets) {
		return false
	}

	ep.Subsets = subsets
	ep.ResourceVersion = s.nextClusterRVLocked()
	s.endpoints[key] = ep
	s.wEndpoints.publish(EventModified, svc.Namespace, *ep.DeepCopy())

	return true
}

// serviceNameLabel is the label EndpointSlices carry so kube-proxy / Gateway
// API / controller-runtime can find the slices backing a Service.
const serviceNameLabel = "kubernetes.io/service-name"

// syncEndpointSliceLocked mirrors a Service's endpoints into a discovery.k8s.io
// EndpointSlice, so EndpointSlice-mode consumers (kube-proxy, Gateway API) see
// the same backends the typed Endpoints object carries.
func (s *ClusterState) syncEndpointSliceLocked(svc *corev1.Service, addrs []corev1.EndpointAddress) {
	store := s.reg.getStore(apiGroupDiscovery, "v1", "endpointslices")
	if store == nil {
		return
	}

	endpoints := make([]any, 0, len(addrs))
	for _, a := range addrs {
		endpoints = append(endpoints, map[string]any{
			"addresses":  []any{a.IP},
			"conditions": map[string]any{"ready": true},
			"nodeName":   nodeName,
			"targetRef": map[string]any{
				"kind": "Pod", "namespace": a.TargetRef.Namespace, "name": a.TargetRef.Name, "uid": string(a.TargetRef.UID),
			},
		})
	}

	ports := make([]any, 0, len(svc.Spec.Ports))
	for _, p := range endpointPorts(svc) {
		ports = append(ports, map[string]any{"name": p.Name, "port": int64(p.Port), "protocol": string(p.Protocol)})
	}

	key := objKey(svc.Namespace, svc.Name)

	slice := store.items[key]
	if slice == nil {
		slice = &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": apiGroupDiscovery + "/v1",
			"kind":       "EndpointSlice",
			"metadata": map[string]any{
				"name":              svc.Name,
				"namespace":         svc.Namespace,
				"labels":            map[string]any{serviceNameLabel: svc.Name},
				"creationTimestamp": nil,
			},
		}}
		slice.SetUID(types.UID(newUID()))
	}

	slice.Object["addressType"] = "IPv4"
	slice.Object["endpoints"] = endpoints
	slice.Object["ports"] = ports
	s.stampRegistryRVLocked(slice)
	store.items[key] = slice
	store.watch.publish(EventModified, svc.Namespace, *slice.DeepCopy())
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
	defaultDeploymentStrategy(dep)

	requested := 1
	if dep.Spec.Replicas != nil {
		requested = int(*dep.Spec.Replicas)
	}

	desired := clampPodCount(requested)
	noteClampMeta(&dep.ObjectMeta, requested, desired)

	// Emit ScalingReplicaSet only on an actual replica-count change — this
	// reconcile runs on every create/update/patch, so an ungated emit would fire
	// on a no-op annotation patch (dedup absorbs volume, not the wrong semantics).
	prevReplicas := dep.Status.Replicas

	// Interpose a ReplicaSet (Deployment→RS→Pod) rather than owning Pods
	// directly, matching real Deployment topology.
	ready := s.syncDeploymentReplicaSetLocked(dep, desired)

	if int32(desired) != prevReplicas { //nolint:gosec // desired is clampPodCount-bounded.
		verb := "up"
		if int32(desired) < prevReplicas { //nolint:gosec // bounded.
			verb = "down"
		}

		rsName := dep.Name + "-" + podTemplateHash(dep.Spec.Template)
		s.recordEventLocked(ownerReferenceForMeta(apiVersionAppsV1, "Deployment", &dep.ObjectMeta),
			"ScalingReplicaSet",
			fmt.Sprintf("Scaled %s replica set %s to %d", verb, rsName, desired))
	}

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
	requested := rawReplicasOf(obj)
	desired := clampPodCount(requested)
	noteClampUnstructured(obj, requested, desired)

	ready := s.syncScaledPods(obj.GetNamespace(), obj.GetName(), ownerRefOf(obj),
		podTemplateFromUnstructured(obj), desired)
	setWorkloadStatus(obj, ready)
	s.resyncEndpointsForNamespaceLocked(obj.GetNamespace())
}

func reconcileStatefulSet(s *ClusterState, obj *unstructured.Unstructured) {
	defaultStatefulSetStrategy(obj)

	requested := rawReplicasOf(obj)
	desired := clampPodCount(requested)
	noteClampUnstructured(obj, requested, desired)

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
	defaultDaemonSetStrategy(obj)

	// A DaemonSet runs one Pod per node whose labels satisfy the template's
	// nodeSelector. With a single synthetic node, a non-matching selector yields
	// zero Pods (rather than the previous unconditional one).
	var names []string
	if s.daemonSetSchedulesToNode(obj) {
		names = []string{obj.GetName() + "-" + nodeName}
	}

	ready := int64(s.syncStablePods(obj.GetNamespace(), ownerRefOf(obj), podTemplateFromUnstructured(obj), names))

	set := func(field string) { _ = unstructured.SetNestedField(obj.Object, ready, "status", field) }
	set("desiredNumberScheduled")
	set("currentNumberScheduled")
	set("numberReady")
	set("numberAvailable")

	_ = unstructured.SetNestedField(obj.Object, obj.GetGeneration(), "status", "observedGeneration")

	s.resyncEndpointsForNamespaceLocked(obj.GetNamespace())
}

// daemonSetSchedulesToNode reports whether the DaemonSet's template nodeSelector
// matches the single synthetic node's labels (empty selector always matches).
func (s *ClusterState) daemonSetSchedulesToNode(obj *unstructured.Unstructured) bool {
	sel, _, _ := unstructured.NestedStringMap(obj.Object, "spec", "template", "spec", "nodeSelector")
	if len(sel) == 0 {
		return true
	}

	labels := s.nodeLabels()
	for k, v := range sel {
		if labels[k] != v {
			return false
		}
	}

	return true
}

// nodeLabels returns the synthetic node's labels (nil if the node is absent).
func (s *ClusterState) nodeLabels() map[string]string {
	st := s.reg.getStore("", "v1", "nodes")
	if st == nil {
		return nil
	}

	node := st.items[objKey("", nodeName)]
	if node == nil {
		return nil
	}

	labels, _, _ := unstructured.NestedStringMap(node.Object, "metadata", "labels")

	return labels
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

// Job-controller Pod labels stamped by reconcileJob. These mirror the real
// upstream Job controller: the batch.kubernetes.io/* keys are GA (job-name
// since 1.27), and the bare job-name/controller-uid keys are the legacy
// aliases kept for compatibility. They are stamped only on Job Pods (not in
// the shared buildControllerPod, which also serves RS/STS/DS).
const (
	jobNameLabel                = "batch.kubernetes.io/job-name"
	jobNameLegacyLabel          = "job-name"
	jobControllerUIDLabel       = "batch.kubernetes.io/controller-uid"
	jobControllerUIDLegacyLabel = "controller-uid"
)

// reconcileJob runs a Job to completion: it reconciles the Job's owned Pods to
// exactly `completions` (default 1) Succeeded Pods and marks the Job Complete.
// Reconciling to the exact count — rather than only topping up — means a lowered
// completions drops the surplus Pods, so status.succeeded reflects the current
// spec instead of overstating it with Pods from a previous, larger run.
func reconcileJob(s *ClusterState, obj *unstructured.Unstructured) {
	requested := 1
	if c, found, _ := unstructured.NestedInt64(obj.Object, "spec", "completions"); found && c > 0 {
		requested = int(c)
	}

	completions := clampPodCount(requested)
	noteClampUnstructured(obj, requested, completions)

	ns := obj.GetNamespace()
	owner := ownerRefOf(obj)
	tmpl := podTemplateFromUnstructured(obj)

	owned := s.podsOwnedByLocked(ns, owner.UID)

	// Shrink first: drop Pods above the desired completions (highest-sorted
	// names) so a re-reconcile after a lowered completions cleans up.
	for len(owned) > completions {
		last := owned[len(owned)-1]
		delete(s.pods, podKey(ns, last.Name))
		s.wPods.publish(EventDeleted, ns, *last.DeepCopy())

		owned = owned[:len(owned)-1]
	}

	// Top up the rest with Pods driven straight to Succeeded.
	for len(owned) < completions {
		pod := s.buildControllerPod(ns, obj.GetName()+"-"+shortID(), tmpl, owner)
		// Stamp the real Job-controller Pod labels (GA + legacy aliases). Done
		// here, not in buildControllerPod, so RS/STS/DS Pods are left untouched.
		jobName := obj.GetName()
		jobUID := string(owner.UID)
		pod.Labels[jobNameLabel] = jobName
		pod.Labels[jobNameLegacyLabel] = jobName
		pod.Labels[jobControllerUIDLabel] = jobUID
		pod.Labels[jobControllerUIDLegacyLabel] = jobUID
		s.markPodSucceededLocked(pod)
		s.pods[podKey(ns, pod.Name)] = pod
		s.wPods.publish(EventAdded, ns, *pod.DeepCopy())
		owned = append(owned, pod)
	}

	succeeded := int64(len(owned))
	_ = unstructured.SetNestedField(obj.Object, succeeded, "status", "succeeded")
	_ = unstructured.SetNestedField(obj.Object, int64(0), "status", "active")

	if completions > 0 {
		_ = unstructured.SetNestedSlice(obj.Object,
			[]any{map[string]any{"type": "Complete", "status": "True"}}, "status", "conditions")

		s.recordEventLocked(objectReferenceForUnstructured(obj), "Completed",
			"Job completed")
	}
}

// markPodSucceededLocked drives a Pod to the completed (Succeeded) terminal
// state used by Job pods. Callers hold s.mu.
func (s *ClusterState) markPodSucceededLocked(pod *corev1.Pod) {
	s.markPodRunningLocked(pod)

	now := s.now()
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

// PVC retention-policy values (spec.persistentVolumeClaimRetentionPolicy).
const (
	pvcRetentionRetain = "Retain"
	pvcRetentionDelete = "Delete"
)

// syncStatefulSetPVCsLocked creates a Bound PVC per (volumeClaimTemplate,
// ordinal) named "<template>-<sts>-<ordinal>". Ownership and scale-down deletion
// honor spec.persistentVolumeClaimRetentionPolicy (default Retain/Retain):
//   - whenDeleted=Delete stamps the StatefulSet as the PVC owner so
//     garbageCollectLocked reaps the PVCs when the StatefulSet is deleted. The
//     default (Retain) leaves NO owner ref, so the volumes survive a delete /
//     helm uninstall — matching real k8s, where data is not lost on uninstall.
//   - whenScaled=Delete removes the PVCs whose ordinal falls outside the new
//     replica count. This is an EXPLICIT reap: StatefulSet scale-down deletes
//     Pods with a bare delete that never invokes garbageCollectLocked, so a Pod
//     owner ref would be inert here.
func (s *ClusterState) syncStatefulSetPVCsLocked(sts *unstructured.Unstructured, replicas int) {
	templates, found, _ := unstructured.NestedSlice(sts.Object, "spec", "volumeClaimTemplates")
	if !found {
		return
	}

	store := s.reg.getStore("", "v1", "persistentvolumeclaims")
	if store == nil {
		return
	}

	whenDeleted, whenScaled := statefulSetPVCRetentionPolicy(sts)

	for _, raw := range templates {
		tmpl, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		tmplName, _, _ := unstructured.NestedString(tmpl, "metadata", "name")

		if whenScaled == pvcRetentionDelete {
			s.reapScaledStatefulSetPVCsLocked(store, sts.GetNamespace(), tmplName, sts.GetName(), replicas)
		}

		for i := range replicas {
			name := fmt.Sprintf("%s-%s-%d", tmplName, sts.GetName(), i)

			key := objKey(sts.GetNamespace(), name)
			if _, exists := store.items[key]; exists {
				continue
			}

			// Fresh deep copy per ordinal — NestedMap copies once, so hoisting it
			// out of the loop would alias one spec map across every PVC.
			spec, _, _ := unstructured.NestedMap(tmpl, "spec")

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
			pvc.SetCreationTimestamp(s.now())

			// Only whenDeleted=Delete stamps the owner ref — the one case where the
			// STS-delete cascade should reap the PVC. Under the Retain default the
			// PVC is deliberately ownerless so it outlives the StatefulSet.
			if whenDeleted == pvcRetentionDelete {
				pvc.SetOwnerReferences([]metav1.OwnerReference{ownerRefOf(sts)})
			}

			s.stampRegistryRVLocked(pvc)
			store.items[key] = pvc
			store.watch.publish(EventAdded, sts.GetNamespace(), *pvc.DeepCopy())
		}
	}
}

// statefulSetPVCRetentionPolicy reads spec.persistentVolumeClaimRetentionPolicy,
// defaulting either field to Retain when unset — the apiserver default.
func statefulSetPVCRetentionPolicy(sts *unstructured.Unstructured) (whenDeleted, whenScaled string) {
	whenDeleted, whenScaled = pvcRetentionRetain, pvcRetentionRetain

	if v, ok, _ := unstructured.NestedString(
		sts.Object, "spec", "persistentVolumeClaimRetentionPolicy", "whenDeleted"); ok && v != "" {
		whenDeleted = v
	}

	if v, ok, _ := unstructured.NestedString(
		sts.Object, "spec", "persistentVolumeClaimRetentionPolicy", "whenScaled"); ok && v != "" {
		whenScaled = v
	}

	return whenDeleted, whenScaled
}

// reapScaledStatefulSetPVCsLocked deletes the volumeClaimTemplate PVCs whose
// ordinal is >= replicas — the whenScaled=Delete behavior. It scans the PVC
// store for "<template>-<sts>-<ordinal>" names in the namespace and reaps the
// out-of-range ones explicitly, because StatefulSet scale-down never runs the
// ownerReference garbage collector.
func (s *ClusterState) reapScaledStatefulSetPVCsLocked(
	store *registryStore, namespace, tmplName, stsName string, replicas int,
) {
	prefix := fmt.Sprintf("%s-%s-", tmplName, stsName)

	for key, pvc := range store.items {
		if pvc.GetNamespace() != namespace {
			continue
		}

		name := pvc.GetName()
		if !strings.HasPrefix(name, prefix) {
			continue
		}

		ordinal, err := strconv.Atoi(name[len(prefix):])
		if err != nil || ordinal < replicas {
			continue
		}

		s.reapRegistryObjectLocked(store, key, pvc)
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

// maxReconciledPods caps how many Pods a single controller materializes. The
// reconciler runs synchronously under the cluster lock, so an unbounded
// replicas/completions (a copied prod manifest, a typo, a fuzz input) would
// otherwise allocate and hang the entire cluster API — a real apiserver just
// stores the integer and lets asynchronous controllers catch up. Clamping keeps
// the emulator responsive; the object's own spec is preserved unchanged.
const maxReconciledPods = 500

func clampPodCount(n int) int {
	switch {
	case n < 0:
		return 0
	case n > maxReconciledPods:
		return maxReconciledPods
	default:
		return n
	}
}

// clampAnnotation records, on an object whose spec asked for more Pods than the
// reconciler will materialize, exactly how many were requested vs materialized.
// The spec is preserved unchanged; this annotation is the only surfacing of the
// cap, so a caller can see why status.replicas is below spec instead of the
// clamp being silent.
const clampAnnotation = "cloudemu.io/pod-count-clamped"

func clampNote(requested, materialized int) string {
	return fmt.Sprintf("requested=%d materialized=%d cap=%d", requested, materialized, maxReconciledPods)
}

// noteClampUnstructured stamps clampAnnotation on a registry-backed object.
func noteClampUnstructured(obj *unstructured.Unstructured, requested, materialized int) {
	if requested <= materialized {
		return
	}

	anns := obj.GetAnnotations()
	if anns == nil {
		anns = make(map[string]string, 1)
	}

	anns[clampAnnotation] = clampNote(requested, materialized)
	obj.SetAnnotations(anns)
}

// noteClampMeta stamps clampAnnotation on a typed object's ObjectMeta.
func noteClampMeta(meta *metav1.ObjectMeta, requested, materialized int) {
	if requested <= materialized {
		return
	}

	if meta.Annotations == nil {
		meta.Annotations = make(map[string]string, 1)
	}

	meta.Annotations[clampAnnotation] = clampNote(requested, materialized)
}

// rawReplicasOf returns the object's requested spec.replicas WITHOUT clamping
// (default 1, negatives floored to 0), so callers can compare it against the
// clamped count and surface the difference via noteClamp.
func rawReplicasOf(obj *unstructured.Unstructured) int {
	n, found, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas")
	if !found {
		return 1
	}

	if n < 0 {
		return 0
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
