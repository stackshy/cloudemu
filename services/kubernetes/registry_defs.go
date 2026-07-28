package kubernetes

// registeredResources lists every registry-backed kind. Adding a Kubernetes
// resource is an entry here (plus a discovery entry and, if it has runtime
// behavior, a reconcile hook) rather than a hand-written handler file.
//
// The typed core/v1, apps/v1 Deployment, and policy/v1 PDB handlers predate the
// registry and keep their own files; everything new lives here.
func registeredResources() []*resourceDef {
	return []*resourceDef{
		{
			group: "apps", version: "v1", kind: "ReplicaSet", listKind: "ReplicaSetList",
			plural: "replicasets", namespaced: true, hasStatus: true, hasScale: true,
			reconcile: reconcileReplicaSet,
		},
		{
			group: "apps", version: "v1", kind: "StatefulSet", listKind: "StatefulSetList",
			plural: "statefulsets", namespaced: true, hasStatus: true, hasScale: true,
			reconcile: reconcileStatefulSet,
		},
		{
			group: "apps", version: "v1", kind: "DaemonSet", listKind: "DaemonSetList",
			plural: "daemonsets", namespaced: true, hasStatus: true,
			reconcile: reconcileDaemonSet,
		},
		{
			group: "", version: "v1", kind: "PersistentVolumeClaim", listKind: "PersistentVolumeClaimList",
			plural: "persistentvolumeclaims", namespaced: true, hasStatus: true,
			reconcile: reconcilePVC,
		},
	}
}
