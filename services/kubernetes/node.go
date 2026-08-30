package kubernetes

import (
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// The emulator runs one or more synthetic Nodes (KWOK-style: node objects with a
// Ready status and no kubelet). By default it runs a single node
// (cloudemu-node-0) and every Pod schedules onto it — the behavior every
// single-node test relies on. With --k8s-nodes N (fixed at cluster creation,
// immutable) it seeds one control-plane node plus N-1 workers and routes Pod
// placement through a deterministic first-fit scheduler
// (scheduleNodeLocked) that honors nodeSelector, taints/tolerations, and
// resource-request feasibility. Node/inter-pod affinity, topology spread,
// scoring/bin-packing, tolerationSeconds/live eviction, and dynamic node
// add/remove are deferred follow-ups.

const (
	// nodeKubeletVersion is the Kubernetes version the synthetic nodes report;
	// clients that gate on server/node version see a modern, stable release.
	nodeKubeletVersion = "v1.31.0"
	// nodeControlPlaneRoleLabel marks the first node as control-plane so
	// `kubectl get nodes` shows a role instead of <none>. It doubles as the
	// standard control-plane taint key: in a multi-node cluster the control-
	// plane node carries it with NoSchedule so workloads land on the workers
	// unless they tolerate it (a real managed-cluster split). At N=1 the node is
	// untainted so everything schedules, matching minikube/kind single-node
	// semantics.
	nodeControlPlaneRoleLabel = "node-role.kubernetes.io/control-plane"
	// nodeLeaseDurationSeconds is the kubelet lease duration reported on each
	// node's kube-node-lease Lease (the real-cluster default).
	nodeLeaseDurationSeconds = 40
	// nodeAllocatableCPU / nodeAllocatableMemory are the schedulable capacity
	// each synthetic node advertises; the scheduler sums scheduled Pods'
	// requests against these when N>1.
	nodeAllocatableCPU    = "4"
	nodeAllocatableMemory = "8041864Ki"
	// nodeAddressTypeInternalIP is the Node address type carrying the routable IP.
	nodeAddressTypeInternalIP = "InternalIP"
)

// nodeNameForIndex returns the deterministic name of the i-th synthetic node.
func nodeNameForIndex(i int) string {
	return fmt.Sprintf("cloudemu-node-%d", i)
}

// nodeInternalIPForIndex returns the deterministic InternalIP of the i-th node
// (10.0.0.1 for node 0, so cloudemu-node-0 keeps its historical host IP).
func nodeInternalIPForIndex(i int) string {
	return fmt.Sprintf("10.0.0.%d", i+1)
}

// newNodeObject builds one synthetic Node, populated so node-inspecting tooling
// (kubectl get nodes [-o wide], scheduler-style field selectors) sees a
// consistent, healthy, real-looking node. Node 0 is the control-plane node;
// when tainted is true it also carries the control-plane NoSchedule taint.
// Callers stamp its resourceVersion and hold s.mu.
func (s *ClusterState) newNodeObject(index int, tainted bool) *unstructured.Unstructured {
	now := s.now()
	ready := now.UTC().Format(time.RFC3339)

	name := nodeNameForIndex(index)
	internalIP := nodeInternalIPForIndex(index)
	controlPlane := index == 0

	labels := map[string]any{
		"kubernetes.io/hostname": name,
		"kubernetes.io/os":       "linux",
		"kubernetes.io/arch":     "amd64",
	}
	if controlPlane {
		labels[nodeControlPlaneRoleLabel] = ""
	}

	node := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata": map[string]any{
			"name":   name,
			"labels": labels,
		},
		"status": map[string]any{
			"conditions":  nodeReadyConditions(ready),
			"addresses":   nodeAddresses(name, internalIP),
			"capacity":    nodeResourceMap(),
			"allocatable": nodeResourceMap(),
			"nodeInfo":    nodeInfoMap(),
		},
	}}

	if controlPlane && tainted {
		_ = unstructured.SetNestedSlice(node.Object, []any{
			map[string]any{"key": nodeControlPlaneRoleLabel, "effect": string(corev1.TaintEffectNoSchedule)},
		}, "spec", "taints")
	}

	node.SetUID(types.UID(newUID()))
	node.SetCreationTimestamp(now)

	return node
}

// nodeReadyConditions returns the four standard healthy Node conditions.
func nodeReadyConditions(ready string) []any {
	return []any{
		map[string]any{
			"type": "MemoryPressure", "status": "False", "reason": "KubeletHasSufficientMemory",
			"lastTransitionTime": ready, "message": "kubelet has sufficient memory available",
		},
		map[string]any{
			"type": "DiskPressure", "status": "False", "reason": "KubeletHasNoDiskPressure",
			"lastTransitionTime": ready, "message": "kubelet has no disk pressure",
		},
		map[string]any{
			"type": "PIDPressure", "status": "False", "reason": "KubeletHasSufficientPID",
			"lastTransitionTime": ready, "message": "kubelet has sufficient PID available",
		},
		map[string]any{
			"type": "Ready", "status": "True", "reason": "KubeletReady",
			"lastTransitionTime": ready, "message": "kubelet is posting ready status",
		},
	}
}

// nodeAddresses returns the InternalIP + Hostname address list for a node.
func nodeAddresses(name, internalIP string) []any {
	return []any{
		map[string]any{"type": nodeAddressTypeInternalIP, "address": internalIP},
		map[string]any{"type": "Hostname", "address": name},
	}
}

// nodeResourceMap returns the capacity/allocatable resource map every synthetic
// node advertises.
func nodeResourceMap() map[string]any {
	return map[string]any{
		"cpu": nodeAllocatableCPU, "memory": nodeAllocatableMemory, "pods": "110", "ephemeral-storage": "46000000Ki",
	}
}

// nodeInfoMap returns the static nodeInfo block reported by every synthetic node.
func nodeInfoMap() map[string]any {
	return map[string]any{
		"kubeletVersion":          nodeKubeletVersion,
		"kubeProxyVersion":        nodeKubeletVersion,
		"osImage":                 "Cloudemu Linux",
		"operatingSystem":         "linux",
		"architecture":            "amd64",
		"kernelVersion":           "6.1.0-cloudemu",
		"containerRuntimeVersion": "containerd://1.7.0",
	}
}

// schedNode is the parsed, scheduling-relevant view of one Node object: its
// name, labels, taints, InternalIP, and allocatable CPU/memory. nodesLocked
// materializes these from the registry Node store so the scheduler and the
// DaemonSet fan-out both read the same source of truth.
type schedNode struct {
	name       string
	internalIP string
	labels     map[string]string
	taints     []corev1.Taint
	allocCPU   resource.Quantity
	allocMem   resource.Quantity
}

// nodesLocked returns every synthetic Node parsed for scheduling, sorted by
// name so first-fit placement and DaemonSet fan-out are deterministic. Callers
// hold s.mu.
func (s *ClusterState) nodesLocked() []schedNode {
	store := s.reg.getStore("", "v1", "nodes")
	if store == nil {
		return nil
	}

	out := make([]schedNode, 0, len(store.items))
	for _, obj := range store.items {
		out = append(out, parseSchedNode(obj))
	}

	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })

	return out
}

// parseSchedNode extracts the scheduling-relevant fields from a Node object.
func parseSchedNode(obj *unstructured.Unstructured) schedNode {
	labels, _, _ := unstructured.NestedStringMap(obj.Object, "metadata", "labels")

	n := schedNode{
		name:       obj.GetName(),
		internalIP: nodeInternalIPOf(obj),
		labels:     labels,
		taints:     nodeTaintsOf(obj),
		allocCPU:   nodeAllocatableQuantity(obj, "cpu"),
		allocMem:   nodeAllocatableQuantity(obj, "memory"),
	}

	return n
}

// nodeInternalIPOf returns a Node's InternalIP address, or "" if none is set.
func nodeInternalIPOf(obj *unstructured.Unstructured) string {
	addrs, _, _ := unstructured.NestedSlice(obj.Object, "status", "addresses")
	for _, raw := range addrs {
		a, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		if t, _ := a["type"].(string); t == nodeAddressTypeInternalIP {
			ip, _ := a["address"].(string)

			return ip
		}
	}

	return ""
}

// nodeTaintsOf parses a Node's spec.taints into typed corev1.Taint values.
func nodeTaintsOf(obj *unstructured.Unstructured) []corev1.Taint {
	raw, found, _ := unstructured.NestedSlice(obj.Object, "spec", "taints")
	if !found {
		return nil
	}

	taints := make([]corev1.Taint, 0, len(raw))

	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}

		key, _ := m["key"].(string)
		value, _ := m["value"].(string)
		effect, _ := m["effect"].(string)
		taints = append(taints, corev1.Taint{Key: key, Value: value, Effect: corev1.TaintEffect(effect)})
	}

	return taints
}

// nodeAllocatableQuantity parses one status.allocatable resource as a Quantity
// (zero when absent/unparseable).
func nodeAllocatableQuantity(obj *unstructured.Unstructured, name string) resource.Quantity {
	v, _, _ := unstructured.NestedString(obj.Object, "status", "allocatable", name)
	if v == "" {
		return resource.Quantity{}
	}

	q, err := resource.ParseQuantity(v)
	if err != nil {
		return resource.Quantity{}
	}

	return q
}

// nodeInternalIPLocked returns the InternalIP of the named node, falling back to
// the historical single-node host IP when the node is unknown. Callers hold
// s.mu.
func (s *ClusterState) nodeInternalIPLocked(name string) string {
	store := s.reg.getStore("", "v1", "nodes")
	if store == nil {
		return nodeHostIP
	}

	node := store.items[objKey("", name)]
	if node == nil {
		return nodeHostIP
	}

	if ip := nodeInternalIPOf(node); ip != "" {
		return ip
	}

	return nodeHostIP
}

// scheduleNodeLocked places pod on a node and reports whether placement
// succeeded. An explicit spec.nodeName bypasses the scheduler (kubelet-style
// direct assignment). With a single node, the pod is placed unconditionally
// (the historical back-compat path — no nodeSelector/taint/request gating). With
// multiple nodes it runs deterministic first-fit over the name-sorted candidates,
// filtering by nodeSelector, taints/tolerations, and request feasibility; the
// first feasible node wins. When nothing fits it emits FailedScheduling and
// returns false, leaving the caller to mark the pod Unschedulable. Callers hold
// s.mu.
func (s *ClusterState) scheduleNodeLocked(pod *corev1.Pod) bool {
	if pod.Spec.NodeName != "" {
		return true
	}

	nodes := s.nodesLocked()

	// Single-node (default) is unconditionally back-compat: no request-
	// feasibility or taint gating, regardless of the manifest's requests, so
	// every existing single-node test schedules exactly as before.
	if len(nodes) <= 1 {
		name := nodeName
		if len(nodes) == 1 {
			name = nodes[0].name
		}

		pod.Spec.NodeName = name
		s.recordScheduledLocked(pod, name)

		return true
	}

	for i := range nodes {
		n := &nodes[i]
		if !labelsMatch(pod.Spec.NodeSelector, n.labels) {
			continue
		}

		if !podToleratesTaints(pod.Spec.Tolerations, n.taints) {
			continue
		}

		if !s.podFitsNodeLocked(pod, n) {
			continue
		}

		pod.Spec.NodeName = n.name
		s.recordScheduledLocked(pod, n.name)

		return true
	}

	s.recordFailedSchedulingLocked(pod)

	return false
}

// recordScheduledLocked emits the scheduler's Scheduled event for a placed pod.
func (s *ClusterState) recordScheduledLocked(pod *corev1.Pod, node string) {
	s.recordEventLocked(objectReferenceForPod(pod), "Scheduled",
		"Successfully assigned "+pod.Namespace+"/"+pod.Name+" to "+node)
}

// recordFailedSchedulingLocked emits the FailedScheduling event a pod gets when
// no node can accept it.
func (s *ClusterState) recordFailedSchedulingLocked(pod *corev1.Pod) {
	s.recordEventLocked(objectReferenceForPod(pod), "FailedScheduling",
		"0/"+fmt.Sprint(len(s.nodesLocked()))+" nodes are available: no node matches the pod's "+
			"nodeSelector, tolerations, or resource requests.")
}

// podToleratesTaints reports whether the pod's tolerations cover every
// scheduling-repelling (NoSchedule/NoExecute) taint on a node. PreferNoSchedule
// is soft and never repels.
func podToleratesTaints(tolerations []corev1.Toleration, taints []corev1.Taint) bool {
	for i := range taints {
		t := &taints[i]
		if t.Effect != corev1.TaintEffectNoSchedule && t.Effect != corev1.TaintEffectNoExecute {
			continue
		}

		if !taintTolerated(t, tolerations) {
			return false
		}
	}

	return true
}

// taintTolerated reports whether any toleration matches the given taint.
func taintTolerated(taint *corev1.Taint, tolerations []corev1.Toleration) bool {
	for i := range tolerations {
		if tolerationMatchesTaint(&tolerations[i], taint) {
			return true
		}
	}

	return false
}

// tolerationMatchesTaint mirrors the upstream Toleration.ToleratesTaint rules:
// the effect must match (empty tolerates all effects), and the key/value must
// match per the operator (Exists with empty key tolerates everything).
func tolerationMatchesTaint(tol *corev1.Toleration, taint *corev1.Taint) bool {
	if tol.Effect != "" && tol.Effect != taint.Effect {
		return false
	}

	if tol.Operator == corev1.TolerationOpExists {
		return tol.Key == "" || tol.Key == taint.Key
	}

	// Default operator is Equal.
	return tol.Key == taint.Key && tol.Value == taint.Value
}

// podFitsNodeLocked reports whether the pod's CPU/memory requests fit within the
// node's allocatable capacity after accounting for the requests of the Pods
// already scheduled there. Callers hold s.mu.
func (s *ClusterState) podFitsNodeLocked(pod *corev1.Pod, n *schedNode) bool {
	reqCPU, reqMem := podRequests(pod)

	usedCPU, usedMem := s.consumedOnNodeLocked(n.name)
	usedCPU.Add(reqCPU)
	usedMem.Add(reqMem)

	return usedCPU.Cmp(n.allocCPU) <= 0 && usedMem.Cmp(n.allocMem) <= 0
}

// consumedOnNodeLocked sums the CPU/memory requests of every non-terminal Pod
// already scheduled onto node. Callers hold s.mu.
func (s *ClusterState) consumedOnNodeLocked(node string) (cpu, mem resource.Quantity) {
	for _, pod := range s.pods {
		if pod.Spec.NodeName != node {
			continue
		}

		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}

		c, m := podRequests(pod)
		cpu.Add(c)
		mem.Add(m)
	}

	return cpu, mem
}

// podRequests sums a Pod's container CPU and memory requests.
func podRequests(pod *corev1.Pod) (cpu, mem resource.Quantity) {
	for i := range pod.Spec.Containers {
		reqs := pod.Spec.Containers[i].Resources.Requests
		cpu.Add(*reqs.Cpu())
		mem.Add(*reqs.Memory())
	}

	return cpu, mem
}

// seedNodeLeaseLocked creates a node's kube-node-lease Lease (kubelet heartbeat
// object), so `kubectl -n kube-node-lease get leases` shows every node's lease
// like a real cluster. Callers hold s.mu.
func (s *ClusterState) seedNodeLeaseLocked(name string) {
	store := s.reg.getStore(apiGroupCoordination, "v1", "leases")
	if store == nil {
		return
	}

	now := s.now()

	lease := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiGroupCoordination + "/v1",
		"kind":       "Lease",
		"metadata": map[string]any{
			"name":      name,
			"namespace": "kube-node-lease",
		},
		"spec": map[string]any{
			"holderIdentity":       name,
			"leaseDurationSeconds": int64(nodeLeaseDurationSeconds),
			"renewTime":            now.UTC().Format("2006-01-02T15:04:05.000000Z"),
		},
	}}
	lease.SetUID(types.UID(newUID()))
	lease.SetCreationTimestamp(now)
	s.stampRegistryRVLocked(lease)

	store.items[objKey("kube-node-lease", name)] = lease
}
