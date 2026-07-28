package kubernetes

// registeredResources lists every registry-backed kind. Adding a Kubernetes
// resource is an entry here (plus, if it has runtime behavior, a reconcile
// hook) — discovery is derived from this list, so a new kind and its API group
// surface automatically.
//
// The typed core/v1, apps/v1 Deployment, and policy/v1 PDB handlers predate the
// registry and keep their own files; everything else lives here.
func registeredResources() []*resourceDef {
	return concat(
		appsRegistryDefs(),
		batchRegistryDefs(),
		networkingRegistryDefs(),
		rbacRegistryDefs(),
		storageRegistryDefs(),
		autoscalingRegistryDefs(),
		discoveryRegistryDefs(),
		coreRegistryDefs(),
	)
}

func appsRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{group: "apps", version: "v1", kind: "ReplicaSet", listKind: "ReplicaSetList", plural: "replicasets", namespaced: true, hasStatus: true, hasScale: true, reconcile: reconcileReplicaSet},
		{group: "apps", version: "v1", kind: "StatefulSet", listKind: "StatefulSetList", plural: "statefulsets", namespaced: true, hasStatus: true, hasScale: true, reconcile: reconcileStatefulSet},
		{group: "apps", version: "v1", kind: "DaemonSet", listKind: "DaemonSetList", plural: "daemonsets", namespaced: true, hasStatus: true, reconcile: reconcileDaemonSet},
	}
}

func batchRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{group: "batch", version: "v1", kind: "Job", listKind: "JobList", plural: "jobs", namespaced: true, hasStatus: true, reconcile: reconcileJob},
		{group: "batch", version: "v1", kind: "CronJob", listKind: "CronJobList", plural: "cronjobs", namespaced: true, hasStatus: true},
	}
}

func networkingRegistryDefs() []*resourceDef {
	const g = "networking.k8s.io"

	return []*resourceDef{
		{group: g, version: "v1", kind: "Ingress", listKind: "IngressList", plural: "ingresses", namespaced: true, hasStatus: true, reconcile: reconcileIngress},
		{group: g, version: "v1", kind: "IngressClass", listKind: "IngressClassList", plural: "ingressclasses", namespaced: false},
		{group: g, version: "v1", kind: "NetworkPolicy", listKind: "NetworkPolicyList", plural: "networkpolicies", namespaced: true},
	}
}

func rbacRegistryDefs() []*resourceDef {
	const g = "rbac.authorization.k8s.io"

	return []*resourceDef{
		{group: g, version: "v1", kind: "Role", listKind: "RoleList", plural: "roles", namespaced: true},
		{group: g, version: "v1", kind: "RoleBinding", listKind: "RoleBindingList", plural: "rolebindings", namespaced: true},
		{group: g, version: "v1", kind: "ClusterRole", listKind: "ClusterRoleList", plural: "clusterroles", namespaced: false},
		{group: g, version: "v1", kind: "ClusterRoleBinding", listKind: "ClusterRoleBindingList", plural: "clusterrolebindings", namespaced: false},
	}
}

func storageRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{group: "storage.k8s.io", version: "v1", kind: "StorageClass", listKind: "StorageClassList", plural: "storageclasses", namespaced: false},
	}
}

func autoscalingRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{group: "autoscaling", version: "v2", kind: "HorizontalPodAutoscaler", listKind: "HorizontalPodAutoscalerList", plural: "horizontalpodautoscalers", namespaced: true, hasStatus: true},
	}
}

func discoveryRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{group: "discovery.k8s.io", version: "v1", kind: "EndpointSlice", listKind: "EndpointSliceList", plural: "endpointslices", namespaced: true},
	}
}

func coreRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{group: "", version: "v1", kind: "PersistentVolumeClaim", listKind: "PersistentVolumeClaimList", plural: "persistentvolumeclaims", namespaced: true, hasStatus: true, reconcile: reconcilePVC},
		{group: "", version: "v1", kind: "PersistentVolume", listKind: "PersistentVolumeList", plural: "persistentvolumes", namespaced: false, hasStatus: true, reconcile: reconcilePV},
		{group: "", version: "v1", kind: "Node", listKind: "NodeList", plural: "nodes", namespaced: false, hasStatus: true},
		{group: "", version: "v1", kind: "Event", listKind: "EventList", plural: "events", namespaced: true},
		{group: "", version: "v1", kind: "ResourceQuota", listKind: "ResourceQuotaList", plural: "resourcequotas", namespaced: true, hasStatus: true},
		{group: "", version: "v1", kind: "LimitRange", listKind: "LimitRangeList", plural: "limitranges", namespaced: true},
	}
}

func concat(groups ...[]*resourceDef) []*resourceDef {
	var out []*resourceDef
	for _, g := range groups {
		out = append(out, g...)
	}

	return out
}
