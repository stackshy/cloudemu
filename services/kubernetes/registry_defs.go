package kubernetes

// API group names for the registry-backed kinds. apps and policy have their own
// constants next to their typed handlers (apiGroupApps, apiGroupPolicy).
const (
	apiGroupBatch       = "batch"
	apiGroupNetworking  = "networking.k8s.io"
	apiGroupRBAC        = "rbac.authorization.k8s.io"
	apiGroupStorage     = "storage.k8s.io"
	apiGroupAutoscaling = "autoscaling"
	apiGroupDiscovery   = "discovery.k8s.io"
	apiGroupExtensions  = "apiextensions.k8s.io"
)

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
		crdRegistryDefs(),
	)
}

func appsRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupApps, version: "v1", kind: "ReplicaSet", listKind: "ReplicaSetList",
			plural: "replicasets", namespaced: true, hasStatus: true, hasScale: true, reconcile: reconcileReplicaSet,
		},
		{
			group: apiGroupApps, version: "v1", kind: "StatefulSet", listKind: "StatefulSetList",
			plural: "statefulsets", namespaced: true, hasStatus: true, hasScale: true, reconcile: reconcileStatefulSet,
		},
		{
			group: apiGroupApps, version: "v1", kind: "DaemonSet", listKind: "DaemonSetList",
			plural: "daemonsets", namespaced: true, hasStatus: true, reconcile: reconcileDaemonSet,
		},
	}
}

func batchRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupBatch, version: "v1", kind: "Job", listKind: "JobList",
			plural: "jobs", namespaced: true, hasStatus: true, reconcile: reconcileJob,
		},
		{
			group: apiGroupBatch, version: "v1", kind: "CronJob", listKind: "CronJobList",
			plural: "cronjobs", namespaced: true, hasStatus: true,
		},
	}
}

func networkingRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupNetworking, version: "v1", kind: "Ingress", listKind: "IngressList",
			plural: "ingresses", namespaced: true, hasStatus: true, reconcile: reconcileIngress,
		},
		{
			group: apiGroupNetworking, version: "v1", kind: "IngressClass", listKind: "IngressClassList",
			plural: "ingressclasses", namespaced: false,
		},
		{
			group: apiGroupNetworking, version: "v1", kind: "NetworkPolicy", listKind: "NetworkPolicyList",
			plural: "networkpolicies", namespaced: true,
		},
	}
}

func rbacRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupRBAC, version: "v1", kind: "Role", listKind: "RoleList",
			plural: "roles", namespaced: true,
		},
		{
			group: apiGroupRBAC, version: "v1", kind: "RoleBinding", listKind: "RoleBindingList",
			plural: "rolebindings", namespaced: true,
		},
		{
			group: apiGroupRBAC, version: "v1", kind: "ClusterRole", listKind: "ClusterRoleList",
			plural: "clusterroles", namespaced: false,
		},
		{
			group: apiGroupRBAC, version: "v1", kind: "ClusterRoleBinding", listKind: "ClusterRoleBindingList",
			plural: "clusterrolebindings", namespaced: false,
		},
	}
}

func storageRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupStorage, version: "v1", kind: "StorageClass", listKind: "StorageClassList",
			plural: "storageclasses", namespaced: false,
		},
	}
}

func autoscalingRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupAutoscaling, version: "v2", kind: "HorizontalPodAutoscaler",
			listKind: "HorizontalPodAutoscalerList", plural: "horizontalpodautoscalers", namespaced: true, hasStatus: true,
			reconcile: reconcileHPA,
		},
	}
}

func discoveryRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupDiscovery, version: "v1", kind: "EndpointSlice", listKind: "EndpointSliceList",
			plural: "endpointslices", namespaced: true,
		},
	}
}

func coreRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: "", version: "v1", kind: "PersistentVolumeClaim", listKind: "PersistentVolumeClaimList",
			plural: "persistentvolumeclaims", namespaced: true, hasStatus: true, reconcile: reconcilePVC,
		},
		{
			group: "", version: "v1", kind: "PersistentVolume", listKind: "PersistentVolumeList",
			plural: "persistentvolumes", namespaced: false, hasStatus: true, reconcile: reconcilePV,
		},
		{
			group: "", version: "v1", kind: "Node", listKind: "NodeList",
			plural: "nodes", namespaced: false, hasStatus: true,
		},
		{
			group: "", version: "v1", kind: "Event", listKind: "EventList",
			plural: "events", namespaced: true,
		},
		{
			group: "", version: "v1", kind: "ResourceQuota", listKind: "ResourceQuotaList",
			plural: "resourcequotas", namespaced: true, hasStatus: true,
		},
		{
			group: "", version: "v1", kind: "LimitRange", listKind: "LimitRangeList",
			plural: "limitranges", namespaced: true,
		},
	}
}

func concat(groups ...[]*resourceDef) []*resourceDef {
	var out []*resourceDef
	for _, g := range groups {
		out = append(out, g...)
	}

	return out
}
