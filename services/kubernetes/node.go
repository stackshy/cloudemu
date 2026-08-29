package kubernetes

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// The emulator runs a single synthetic Node (KWOK-style: a node object with a
// Ready status and no kubelet). Seam 5 makes it look like a real node —
// creationTimestamp so `kubectl get nodes` AGE isn't <unknown>, a Ready
// condition, capacity/allocatable/nodeInfo/addresses, a control-plane role, and
// a kube-node-lease Lease — and routes Pod placement through scheduleNodeLocked
// so scheduling emits a Scheduled event. True multi-node scheduling
// (nodeSelector/taints, per-node DaemonSet fan-out, FailedScheduling) is a
// deferred follow-up; it would require rewriting DaemonSet/endpoint node
// stamping, which is invasive for low local-dev value.

const (
	// nodeKubeletVersion is the Kubernetes version the synthetic node reports;
	// clients that gate on server/node version see a modern, stable release.
	nodeKubeletVersion = "v1.31.0"
	// nodeControlPlaneRoleLabel marks the single node as control-plane so
	// `kubectl get nodes` shows a role instead of <none>.
	nodeControlPlaneRoleLabel = "node-role.kubernetes.io/control-plane"
	// nodeLeaseDurationSeconds is the kubelet lease duration reported on the
	// node's kube-node-lease Lease (the real-cluster default).
	nodeLeaseDurationSeconds = 40
)

// newNodeObject builds the single synthetic Node the emulator schedules every
// Pod onto, populated so node-inspecting tooling (kubectl get nodes [-o wide],
// scheduler-style field selectors) sees a consistent, healthy, real-looking
// node. Callers stamp its resourceVersion. Callers hold s.mu.
func (s *ClusterState) newNodeObject() *unstructured.Unstructured {
	now := s.now()
	ready := now.UTC().Format(time.RFC3339)

	node := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata": map[string]any{
			"name": nodeName,
			"labels": map[string]any{
				"kubernetes.io/hostname":  nodeName,
				"kubernetes.io/os":        "linux",
				"kubernetes.io/arch":      "amd64",
				nodeControlPlaneRoleLabel: "",
			},
		},
		"status": map[string]any{
			"conditions": []any{
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
			},
			"addresses": []any{
				map[string]any{"type": "InternalIP", "address": nodeHostIP},
				map[string]any{"type": "Hostname", "address": nodeName},
			},
			"capacity": map[string]any{
				"cpu": "4", "memory": "8144264Ki", "pods": "110", "ephemeral-storage": "50000000Ki",
			},
			"allocatable": map[string]any{
				"cpu": "4", "memory": "8041864Ki", "pods": "110", "ephemeral-storage": "46000000Ki",
			},
			"nodeInfo": map[string]any{
				"kubeletVersion":          nodeKubeletVersion,
				"kubeProxyVersion":        nodeKubeletVersion,
				"osImage":                 "Cloudemu Linux",
				"operatingSystem":         "linux",
				"architecture":            "amd64",
				"kernelVersion":           "6.1.0-cloudemu",
				"containerRuntimeVersion": "containerd://1.7.0",
			},
		},
	}}
	node.SetUID(types.UID(newUID()))
	node.SetCreationTimestamp(now)

	return node
}

// scheduleNodeLocked assigns pod to the single synthetic node (honoring an
// explicit spec.nodeName) and emits a Scheduled event the first time a pod is
// placed. It replaces the old unconditional const nodeName stamp. Callers hold
// s.mu.
func (s *ClusterState) scheduleNodeLocked(pod *corev1.Pod) {
	if pod.Spec.NodeName != "" {
		return
	}

	pod.Spec.NodeName = nodeName
	s.recordEventLocked(objectReferenceForPod(pod), "Scheduled",
		"Successfully assigned "+pod.Namespace+"/"+pod.Name+" to "+nodeName)
}

// seedNodeLeaseLocked creates the node's kube-node-lease Lease (kubelet
// heartbeat object), so `kubectl -n kube-node-lease get leases` shows the node's
// lease like a real cluster. Callers hold s.mu.
func (s *ClusterState) seedNodeLeaseLocked() {
	store := s.reg.getStore(apiGroupCoordination, "v1", "leases")
	if store == nil {
		return
	}

	now := s.now()

	lease := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiGroupCoordination + "/v1",
		"kind":       "Lease",
		"metadata": map[string]any{
			"name":      nodeName,
			"namespace": "kube-node-lease",
		},
		"spec": map[string]any{
			"holderIdentity":       nodeName,
			"leaseDurationSeconds": int64(nodeLeaseDurationSeconds),
			"renewTime":            now.UTC().Format("2006-01-02T15:04:05.000000Z"),
		},
	}}
	lease.SetUID(types.UID(newUID()))
	lease.SetCreationTimestamp(now)
	s.stampRegistryRVLocked(lease)

	store.items[objKey("kube-node-lease", nodeName)] = lease
}
