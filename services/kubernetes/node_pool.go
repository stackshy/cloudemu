package kubernetes

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// Managed node pools let a cloud control plane (EKS managed nodegroups today)
// own a group of worker Nodes in a cluster's data plane. The control plane
// describes the pool it wants and SyncNodePool converges the Node objects to it:
// it adds nodes on scale up, cordons and drains the newest nodes on scale down,
// re-applies labels, taints and node info to the nodes it keeps, and removes
// every node when the count drops to zero.
//
// The first time a pool adds a node the bootstrap nodes (cloudemu-node-N) are
// retired, since a managed cluster only runs the workers its node groups
// launch. From then on the cluster schedules strictly: a Pod that no node
// accepts stays Pending, including when there are no nodes at all.

const (
	// nodePoolAnnotation names the pool a managed Node belongs to.
	nodePoolAnnotation = "cloudemu.io/node-pool"
	// nodePoolOrdinalAnnotation is the cluster-wide launch ordinal of a managed
	// Node. Scale down removes the highest ordinals first.
	nodePoolOrdinalAnnotation = "cloudemu.io/node-pool-ordinal"
	// nodePoolManagedAnnotation records the label keys and taints the pool put
	// on a node, so a later sync can remove the ones the pool dropped without
	// touching labels or taints someone added by hand.
	nodePoolManagedAnnotation = "cloudemu.io/node-pool-managed"
	// nodeProvidedIPAnnotation is the kubelet annotation carrying the node IP.
	nodeProvidedIPAnnotation = "alpha.kubernetes.io/provided-node-ip"
	// bootstrapNodePrefix names the synthetic nodes a fresh cluster seeds.
	bootstrapNodePrefix = "cloudemu-node-"
	// taintKeyUnschedulable is the taint the node lifecycle controller adds to a
	// cordoned node.
	taintKeyUnschedulable = "node.kubernetes.io/unschedulable"
	// labelTopologyZone and labelTopologyZoneBeta carry a node's zone.
	labelTopologyZone     = "topology.kubernetes.io/zone"
	labelTopologyZoneBeta = "failure-domain.beta.kubernetes.io/zone"
	labelHostname         = "kubernetes.io/hostname"
	// managedNodeHostsPerOctet and managedNodeFirstHost shape managed node IPs:
	// ordinal o maps to 10.0.<1 + o/240>.<10 + o%240>, clear of the bootstrap
	// nodes' 10.0.0.x range.
	managedNodeHostsPerOctet = 240
	managedNodeFirstHost     = 10
)

// NodePool is the desired state of one managed group of worker Nodes.
type NodePool struct {
	// Name identifies the pool within the cluster (the nodegroup name).
	Name string
	// Count is the number of Nodes the pool should run.
	Count int
	// Labels are applied to every Node of the pool, on top of the per-node
	// hostname and zone labels.
	Labels map[string]string
	// Taints are applied to every Node of the pool.
	Taints []corev1.Taint
	// Zones are the availability zones the pool spreads its Nodes over, in
	// order. Empty leaves the zone labels off.
	Zones []string
	// Capacity and Allocatable are each Node's resources.
	Capacity    NodeResources
	Allocatable NodeResources
	// Info is reported in each Node's status.nodeInfo.
	Info NodeInfo
	// NodeName builds a Node's name from its InternalIP. Nil uses the IP.
	NodeName func(internalIP string) string
	// ProviderID builds a Node's spec.providerID. Nil leaves it empty.
	ProviderID func(zone, nodeName string) string
}

// NodeResources is a Node's capacity or allocatable resource list.
type NodeResources struct {
	CPU              string
	Memory           string
	Pods             string
	EphemeralStorage string
}

// NodeInfo is the subset of status.nodeInfo a pool controls. Empty fields fall
// back to the synthetic defaults.
type NodeInfo struct {
	KubeletVersion          string
	OSImage                 string
	OperatingSystem         string
	Architecture            string
	KernelVersion           string
	ContainerRuntimeVersion string
}

// nodePoolManaged is the JSON body of nodePoolManagedAnnotation.
type nodePoolManaged struct {
	Labels []string `json:"labels,omitempty"`
	Taints []string `json:"taints,omitempty"`
}

// SyncNodePool converges the cluster's Nodes for pool p to p.Count nodes. It
// adds new nodes, re-applies labels, taints and node info to the nodes it keeps,
// and cordons, drains and deletes the newest nodes beyond the count. Pods on a
// removed node are rescheduled onto a remaining node or left Pending, and
// Pending Pods and DaemonSets are re-run against the new node set.
//
// SyncNodePool takes the cluster lock and never calls back into its caller, so
// a control plane may call it while holding its own lock.
//
//nolint:gocritic // hugeParam: NodePool is a value-typed spec, copied once per sync.
func (s *ClusterState) SyncNodePool(p NodePool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	store := s.reg.getStore("", "v1", "nodes")
	if store == nil {
		return
	}

	count := max(p.Count, 0)
	members := s.poolMembersLocked(store, p.Name)
	keep := min(len(members), count)

	for _, node := range members[:keep] {
		s.applyPoolToNodeLocked(store, node, &p)
	}

	for i := len(members); i < count; i++ {
		s.addPoolNodeLocked(store, &p, i)
	}

	if count > len(members) && !s.managedNodes {
		s.managedNodes = true
		s.retireBootstrapNodesLocked(store)
	}

	// Remove the newest nodes first, after cordoning the whole set.
	removal := make([]*unstructured.Unstructured, 0, len(members)-keep)
	for i := len(members) - 1; i >= keep; i-- {
		removal = append(removal, members[i])
	}

	s.drainAndRemoveNodesLocked(store, removal)

	s.refanDaemonSetsLocked()
	s.reschedulePendingPodsLocked()
}

// poolMembersLocked returns the pool's Nodes sorted by launch ordinal, oldest
// first. Callers hold s.mu.
func (*ClusterState) poolMembersLocked(store *registryStore, pool string) []*unstructured.Unstructured {
	var out []*unstructured.Unstructured

	for _, obj := range store.items {
		if obj.GetAnnotations()[nodePoolAnnotation] == pool {
			out = append(out, obj)
		}
	}

	sort.Slice(out, func(i, j int) bool { return nodeOrdinal(out[i]) < nodeOrdinal(out[j]) })

	return out
}

// nodeOrdinal returns a managed Node's launch ordinal (0 when absent).
func nodeOrdinal(obj *unstructured.Unstructured) int {
	n, _ := strconv.Atoi(obj.GetAnnotations()[nodePoolOrdinalAnnotation])

	return n
}

// managedNodeIP returns the InternalIP of the managed node with ordinal o.
func managedNodeIP(o uint32) string {
	return fmt.Sprintf("10.0.%d.%d", 1+o/managedNodeHostsPerOctet, managedNodeFirstHost+o%managedNodeHostsPerOctet)
}

// addPoolNodeLocked launches the pool's node at position index (which picks its
// zone) with the next cluster-wide ordinal. Callers hold s.mu.
func (s *ClusterState) addPoolNodeLocked(store *registryStore, p *NodePool, index int) {
	ordinal := s.nextNodeOrdinal
	s.nextNodeOrdinal++

	ip := managedNodeIP(ordinal)

	name := ip
	if p.NodeName != nil {
		name = p.NodeName(ip)
	}

	zone := ""
	if len(p.Zones) > 0 {
		zone = p.Zones[index%len(p.Zones)]
	}

	now := s.now()

	node := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata": map[string]any{
			"name": name,
			"annotations": map[string]any{
				nodePoolAnnotation:                                       p.Name,
				nodePoolOrdinalAnnotation:                                strconv.FormatUint(uint64(ordinal), 10),
				nodeProvidedIPAnnotation:                                 ip,
				"node.alpha.kubernetes.io/ttl":                           "0",
				"volumes.kubernetes.io/controller-managed-attach-detach": "true",
			},
		},
		"spec": map[string]any{},
		"status": map[string]any{
			"conditions": nodeReadyConditions(now.UTC().Format(time.RFC3339)),
			"addresses": []any{
				map[string]any{"type": nodeAddressTypeInternalIP, "address": ip},
				map[string]any{"type": "InternalDNS", "address": name},
				map[string]any{"type": "Hostname", "address": name},
			},
			"daemonEndpoints": map[string]any{"kubeletEndpoint": map[string]any{"Port": int64(kubeletPort)}},
		},
	}}

	if p.ProviderID != nil {
		_ = unstructured.SetNestedField(node.Object, p.ProviderID(zone, name), "spec", "providerID")
	}

	node.SetLabels(map[string]string{})
	setPoolZone(node, zone)
	node.SetUID(types.UID(newUID()))
	node.SetCreationTimestamp(now)

	s.writePoolSpecLocked(node, p)
	s.stampRegistryRVLocked(node)
	store.items[objKey("", name)] = node
	store.watch.publish(EventAdded, "", *node.DeepCopy())
	s.seedNodeLeaseLocked(name)
}

// kubeletPort is the kubelet's secure port, reported in daemonEndpoints.
const kubeletPort = 10250

// setPoolZone stamps the node's zone labels. The zone is fixed at launch.
func setPoolZone(node *unstructured.Unstructured, zone string) {
	if zone == "" {
		return
	}

	labels := node.GetLabels()
	labels[labelTopologyZone] = zone
	labels[labelTopologyZoneBeta] = zone
	node.SetLabels(labels)
}

// applyPoolToNodeLocked re-applies the pool's labels, taints, resources and node
// info to a node it keeps, publishing a MODIFIED event only when something
// changed. Callers hold s.mu.
func (s *ClusterState) applyPoolToNodeLocked(store *registryStore, node *unstructured.Unstructured, p *NodePool) {
	before := node.DeepCopy()

	s.writePoolSpecLocked(node, p)

	if reflect.DeepEqual(before.Object, node.Object) {
		return
	}

	s.stampRegistryRVLocked(node)
	store.watch.publish(EventModified, "", *node.DeepCopy())
}

// writePoolSpecLocked writes the pool-owned parts of a node: labels (pool labels
// plus hostname, replacing the labels the pool set last time), taints (the same
// way), capacity, allocatable and node info. Labels and taints the pool never
// set are left alone. Callers hold s.mu.
func (*ClusterState) writePoolSpecLocked(node *unstructured.Unstructured, p *NodePool) {
	prev := readPoolManaged(node)

	labels := node.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	for _, k := range prev.Labels {
		delete(labels, k)
	}

	managed := nodePoolManaged{}

	for k, v := range p.Labels {
		managed.Labels = append(managed.Labels, k)
		labels[k] = v
	}

	labels[labelHostname] = node.GetName()
	node.SetLabels(labels)
	sort.Strings(managed.Labels)

	managed.Taints = writePoolTaints(node, prev.Taints, p.Taints)

	writeNodeResources(node, "capacity", p.Capacity)
	writeNodeResources(node, "allocatable", p.Allocatable)
	writeNodeInfo(node, &p.Info)

	raw, _ := json.Marshal(managed)

	ann := node.GetAnnotations()
	ann[nodePoolManagedAnnotation] = string(raw)
	node.SetAnnotations(ann)
}

// readPoolManaged decodes the node's managed-keys annotation.
func readPoolManaged(node *unstructured.Unstructured) nodePoolManaged {
	var out nodePoolManaged

	_ = json.Unmarshal([]byte(node.GetAnnotations()[nodePoolManagedAnnotation]), &out)

	return out
}

// taintID identifies a taint by key and effect, the way the API does.
func taintID(key, effect string) string { return key + ":" + effect }

// writePoolTaints replaces the taints the pool set last time (prev) with want,
// keeping every other taint on the node, and returns the new managed set.
func writePoolTaints(node *unstructured.Unstructured, prev []string, want []corev1.Taint) []string {
	drop := make(map[string]bool, len(prev))
	for _, id := range prev {
		drop[id] = true
	}

	existing, _, _ := unstructured.NestedSlice(node.Object, "spec", "taints")
	out := make([]any, 0, len(existing)+len(want))

	for _, raw := range existing {
		m, _ := raw.(map[string]any)
		key, _ := m["key"].(string)
		effect, _ := m["effect"].(string)

		if !drop[taintID(key, effect)] {
			out = append(out, raw)
		}
	}

	managed := make([]string, 0, len(want))

	for i := range want {
		t := map[string]any{"key": want[i].Key, "effect": string(want[i].Effect)}
		if want[i].Value != "" {
			t["value"] = want[i].Value
		}

		out = append(out, t)
		managed = append(managed, taintID(want[i].Key, string(want[i].Effect)))
	}

	if len(out) == 0 {
		unstructured.RemoveNestedField(node.Object, "spec", "taints")
	} else {
		_ = unstructured.SetNestedSlice(node.Object, out, "spec", "taints")
	}

	return managed
}

// writeNodeResources sets status.<field> from r, falling back to the synthetic
// defaults for any empty entry.
func writeNodeResources(node *unstructured.Unstructured, field string, r NodeResources) {
	res := nodeResourceMap()

	for k, v := range map[string]string{
		"cpu": r.CPU, "memory": r.Memory, "pods": r.Pods, "ephemeral-storage": r.EphemeralStorage,
	} {
		if v != "" {
			res[k] = v
		}
	}

	_ = unstructured.SetNestedMap(node.Object, res, "status", field)
}

// writeNodeInfo sets status.nodeInfo from info over the synthetic defaults.
func writeNodeInfo(node *unstructured.Unstructured, info *NodeInfo) {
	out := nodeInfoMap()

	for k, v := range map[string]string{
		"kubeletVersion": info.KubeletVersion, "kubeProxyVersion": info.KubeletVersion,
		"osImage": info.OSImage, "operatingSystem": info.OperatingSystem,
		"architecture": info.Architecture, "kernelVersion": info.KernelVersion,
		"containerRuntimeVersion": info.ContainerRuntimeVersion,
	} {
		if v != "" {
			out[k] = v
		}
	}

	_ = unstructured.SetNestedMap(node.Object, out, "status", "nodeInfo")
}

// retireBootstrapNodesLocked removes the seeded cloudemu-node-N nodes once a
// managed pool runs real workers, moving their Pods onto the managed nodes.
// Callers hold s.mu.
func (s *ClusterState) retireBootstrapNodesLocked(store *registryStore) {
	names := make([]string, 0, 1)

	for _, obj := range store.items {
		if _, managed := obj.GetAnnotations()[nodePoolAnnotation]; !managed && strings.HasPrefix(obj.GetName(), bootstrapNodePrefix) {
			names = append(names, obj.GetName())
		}
	}

	sort.Strings(names)

	for _, name := range names {
		s.removeNodeLocked(store, store.items[objKey("", name)])
	}
}

// drainAndRemoveNodesLocked cordons every node in the removal set first, then
// removes them one by one. Cordoning the whole set up front means a Pod evicted
// from one of them can only land on a node that stays, never on another node
// the same scale down is about to remove. Callers hold s.mu.
func (s *ClusterState) drainAndRemoveNodesLocked(store *registryStore, nodes []*unstructured.Unstructured) {
	for _, node := range nodes {
		s.cordonNodeLocked(store, node)
	}

	for _, node := range nodes {
		s.removeNodeLocked(store, node)
	}
}

// cordonNodeLocked marks a node unschedulable (spec.unschedulable plus the
// unschedulable taint) and publishes the change so watchers see the cordon.
// Callers hold s.mu.
func (s *ClusterState) cordonNodeLocked(store *registryStore, node *unstructured.Unstructured) {
	_ = unstructured.SetNestedField(node.Object, true, "spec", "unschedulable")

	taints, _, _ := unstructured.NestedSlice(node.Object, "spec", "taints")
	taints = append(taints, map[string]any{
		"key": taintKeyUnschedulable, "effect": string(corev1.TaintEffectNoSchedule),
		"timeAdded": s.now().UTC().Format(time.RFC3339),
	})
	_ = unstructured.SetNestedSlice(node.Object, taints, "spec", "taints")

	s.stampRegistryRVLocked(node)
	store.watch.publish(EventModified, "", *node.DeepCopy())
}

// removeNodeLocked deletes a node through the same teardown the API delete runs:
// its Pods are evacuated and its lease removed. Callers hold s.mu.
func (s *ClusterState) removeNodeLocked(store *registryStore, node *unstructured.Unstructured) {
	if node == nil {
		return
	}

	s.stampRegistryRVLocked(node)
	delete(store.items, objKey("", node.GetName()))
	store.watch.publish(EventDeleted, "", *node.DeepCopy())
	nodeOnDelete(s, node)
}
