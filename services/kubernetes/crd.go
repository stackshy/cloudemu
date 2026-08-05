package kubernetes

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// CustomResourceDefinition support. A CRD is pure API surface with no
// container/runtime semantics, so it fits the registry model almost exactly:
// creating a CRD materializes a registry store for its custom-resource GVR, and
// from then on the generic handler serves that kind's CRUD/list/watch/status,
// while discovery (derived from the live registry) advertises it. Deleting the
// CRD deregisters the store and cascade-deletes its custom resources.
//
// Structural schema VALIDATION of custom resources is a documented
// simplification: CRs are accepted and stored as-is (matching how the emulator
// already treats server-side apply as a merge).

// crdRegistryDefs registers the apiextensions.k8s.io/v1 CustomResourceDefinition
// kind itself. Its reconcile hook materializes CR stores; its onDelete hook
// tears them down.
func crdRegistryDefs() []*resourceDef {
	return []*resourceDef{
		{
			group: apiGroupExtensions, version: "v1",
			kind: "CustomResourceDefinition", listKind: "CustomResourceDefinitionList",
			plural: "customresourcedefinitions", namespaced: false, hasStatus: true,
			reconcile: reconcileCRD, onDelete: onDeleteCRD,
		},
	}
}

// reconcileCRD materializes a registry store for every served version of the
// CRD, then marks the CRD Established so kubectl/operators treat it as ready.
func reconcileCRD(s *ClusterState, obj *unstructured.Unstructured) {
	for _, d := range crdResourceDefs(obj) {
		s.reg.addStore(d)
	}

	setCRDEstablished(obj)
}

// onDeleteCRD deregisters the CRD's CR stores and cascade-deletes any custom
// resources that were created against them.
func onDeleteCRD(s *ClusterState, obj *unstructured.Unstructured) {
	for _, d := range crdResourceDefs(obj) {
		if st := s.reg.getStore(d.group, d.version, d.plural); st != nil {
			for key, cr := range st.items {
				delete(st.items, key)
				st.watch.publish(EventDeleted, cr.GetNamespace(), *cr.DeepCopy())
			}
		}

		s.reg.removeStore(d.group, d.version, d.plural)
	}
}

// crdResourceDefs derives the registry resourceDef(s) — one per served version —
// from a CRD object. Returns nil for a structurally-incomplete CRD (missing
// group/plural/kind) rather than registering a shadowing or malformed store.
func crdResourceDefs(obj *unstructured.Unstructured) []*resourceDef {
	group, _, _ := unstructured.NestedString(obj.Object, "spec", "group")
	plural, _, _ := unstructured.NestedString(obj.Object, "spec", "names", "plural")
	kind, _, _ := unstructured.NestedString(obj.Object, "spec", "names", "kind")

	if group == "" || plural == "" || kind == "" {
		return nil
	}

	listKind, _, _ := unstructured.NestedString(obj.Object, "spec", "names", "listKind")
	if listKind == "" {
		listKind = kind + "List"
	}

	scope, _, _ := unstructured.NestedString(obj.Object, "spec", "scope")
	namespaced := scope != "Cluster"

	versions, _, _ := unstructured.NestedSlice(obj.Object, "spec", "versions")

	out := make([]*resourceDef, 0, len(versions))

	for _, v := range versions {
		vm, ok := v.(map[string]any)
		if !ok {
			continue
		}

		if served, _, _ := unstructured.NestedBool(vm, "served"); !served {
			continue
		}

		name, _, _ := unstructured.NestedString(vm, "name")
		if name == "" {
			continue
		}

		_, hasStatus, _ := unstructured.NestedMap(vm, "subresources", "status")
		_, hasScale, _ := unstructured.NestedMap(vm, "subresources", "scale")

		out = append(out, &resourceDef{
			group: group, version: name, kind: kind, listKind: listKind,
			plural: plural, namespaced: namespaced, hasStatus: hasStatus, hasScale: hasScale,
		})
	}

	return out
}

// setCRDEstablished fills the CRD's status the way the apiextensions controller
// would: acceptedNames mirrors spec.names, storedVersions lists the served
// versions, and the NamesAccepted/Established conditions go True.
func setCRDEstablished(obj *unstructured.Unstructured) {
	if names, found, _ := unstructured.NestedMap(obj.Object, "spec", "names"); found {
		_ = unstructured.SetNestedMap(obj.Object, names, "status", "acceptedNames")
	}

	defs := crdResourceDefs(obj)
	stored := make([]any, 0, len(defs))

	for _, d := range defs {
		stored = append(stored, d.version)
	}

	_ = unstructured.SetNestedSlice(obj.Object, stored, "status", "storedVersions")
	_ = unstructured.SetNestedSlice(obj.Object, []any{
		map[string]any{"type": "NamesAccepted", "status": "True", "reason": "NoConflicts"},
		map[string]any{"type": "Established", "status": "True", "reason": "InitialNamesAccepted"},
	}, "status", "conditions")
}
