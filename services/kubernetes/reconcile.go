package kubernetes

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"reflect"
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
	var addrs []corev1.EndpointAddress

	for _, pod := range s.pods {
		if pod.Namespace != svc.Namespace || pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" {
			continue
		}

		if !labelsMatch(svc.Spec.Selector, pod.Labels) {
			continue
		}

		addrs = append(addrs, corev1.EndpointAddress{
			IP:        pod.Status.PodIP,
			NodeName:  strPtr(nodeName),
			TargetRef: &corev1.ObjectReference{Kind: "Pod", Namespace: pod.Namespace, Name: pod.Name, UID: pod.UID},
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
		ep = newEndpointsObject(svc.Namespace, svc.Name)
	}

	if existed && reflect.DeepEqual(ep.Subsets, subsets) {
		return false
	}

	ep.Subsets = subsets
	ep.ResourceVersion = bumpResourceVersion(ep.ResourceVersion)
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
	store := s.reg.stores[regKey(apiGroupDiscovery, "v1", "endpointslices")]
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
	store.stampRVLocked(slice)
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
	desired := 1
	if dep.Spec.Replicas != nil {
		desired = int(*dep.Spec.Replicas)
	}

	desired = clampPodCount(desired)

	owner := metav1.OwnerReference{
		APIVersion: "apps/v1", Kind: "Deployment", Name: dep.Name, UID: dep.UID,
		Controller: boolPtr(true), BlockOwnerDeletion: boolPtr(true),
	}

	// syncScaledPods returns a count already clamped to maxReconciledPods, so the
	// int32 conversion cannot overflow.
	ready := int32(s.syncScaledPods(dep.Namespace, dep.Name, owner, dep.Spec.Template, desired)) //nolint:gosec // bounded by maxReconciledPods

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
	completions := 1
	if c, found, _ := unstructured.NestedInt64(obj.Object, "spec", "completions"); found && c > 0 {
		completions = clampPodCount(int(c))
	}

	ns := obj.GetNamespace()
	owner := ownerRefOf(obj)
	tmpl := podTemplateFromUnstructured(obj)

	// Count owned Pods once, then top up — re-scanning s.pods each iteration
	// would make this O(n²).
	have := len(s.podsOwnedByLocked(ns, owner.UID))
	for ; have < completions; have++ {
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

// newNodeObject builds the single synthetic Node the emulator schedules every
// Pod onto. It is marked Ready with an InternalIP so tooling that inspects node
// health or addresses (kubectl get nodes, scheduler-style field selectors) sees
// a consistent, healthy node.
func newNodeObject() *unstructured.Unstructured {
	node := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata": map[string]any{
			"name":              nodeName,
			"labels":            map[string]any{"kubernetes.io/hostname": nodeName},
			"creationTimestamp": nil,
		},
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"type": "Ready", "status": "True", "reason": "KubeletReady"},
			},
			"addresses": []any{
				map[string]any{"type": "InternalIP", "address": nodeHostIP},
				map[string]any{"type": "Hostname", "address": nodeName},
			},
		},
	}}
	node.SetUID(types.UID(newUID()))
	node.SetResourceVersion("1")

	return node
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

func replicasOf(obj *unstructured.Unstructured) int {
	n, found, _ := unstructured.NestedInt64(obj.Object, "spec", "replicas")
	if !found {
		return 1
	}

	return clampPodCount(int(n))
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
