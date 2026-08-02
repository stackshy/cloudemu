package kubernetes

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Server-side apply with field ownership. Each apply request carries a
// fieldManager; the server records which leaf fields that manager owns in
// metadata.managedFields. A later apply by a DIFFERENT manager that changes a
// field the first manager owns is a conflict (HTTP 409) unless force=true, which
// transfers ownership. This is a pragmatic subset of upstream SSA: ownership and
// conflict detection are tracked at leaf-field granularity (map keys, whole
// arrays), which is enough for the apply/re-apply/conflict flows tooling relies
// on; sub-array structural merging is not modeled.

// pathSep joins path segments internally. A null byte can't appear in a JSON key
// (labels contain dots and slashes, so dot-joining would be ambiguous).
const pathSep = "\x00"

// defaultFieldManager is used when an apply request omits ?fieldManager=.
const defaultFieldManager = "cloudemu"

// ssaSkipTop are the top-level fields never tracked as owned (identity + server-
// owned metadata + status, which flows through its own subresource).
//
//nolint:gochecknoglobals // immutable lookup set.
var ssaSkipTop = map[string]bool{"apiVersion": true, "kind": true, "status": true}

// ssaSkipMeta are metadata subfields never tracked as owned.
//
//nolint:gochecknoglobals // immutable lookup set.
var ssaSkipMeta = map[string]bool{
	"managedFields": true, "resourceVersion": true, "creationTimestamp": true,
	"uid": true, "generation": true, "selfLink": true,
}

// serverSideApply handles an apply-patch: it detects conflicts against other
// field managers, merges the applied config, and rewrites managedFields. Returns
// (merged, true) on success; on conflict or wire error it has already written
// the response and returns (nil, false).
func (s *ClusterState) serverSideApply(
	w http.ResponseWriter, r *http.Request, st *registryStore, cur *unstructured.Unstructured,
) (*unstructured.Unstructured, bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeBadRequest(w, "k8s api: read apply body: "+err.Error())

		return nil, false
	}

	applied := map[string]any{}
	if err := json.Unmarshal(body, &applied); err != nil {
		writeBadRequest(w, "k8s api: decode apply body: "+err.Error())

		return nil, false
	}

	manager := r.URL.Query().Get("fieldManager")
	if manager == "" {
		manager = defaultFieldManager
	}

	force := r.URL.Query().Get("force") == "true"
	appliedLeaves := ownedLeaves(applied)

	if conflicts := s.applyConflicts(cur, applied, appliedLeaves, manager); len(conflicts) > 0 && !force {
		writeStatus(w, http.StatusConflict, metav1.StatusReasonConflict,
			"Apply failed with conflicts: fields managed by another manager: "+strings.Join(conflicts, ", "))

		return nil, false
	}

	merged, mok := decodeUnstructuredMap(w, mergeRFC7396(cur.Object, applied))
	if !mok {
		return nil, false
	}

	setManagedFields(merged, updateManagedFields(cur, manager, appliedLeaves, st.def.apiVersion(), s.now()))

	return merged, true
}

// applyConflicts returns the human-readable paths the applied config would
// change that are currently owned by a different manager (value actually
// differs). An empty result means no conflict.
func (*ClusterState) applyConflicts(
	cur *unstructured.Unstructured, applied map[string]any, appliedLeaves map[string]bool, manager string,
) []string {
	owners := ownerByLeaf(cur)

	var out []string

	for leaf := range appliedLeaves {
		other, ok := owners[leaf]
		if !ok || other == manager {
			continue
		}

		segs := strings.Split(leaf, pathSep)
		curVal, _, _ := unstructured.NestedFieldNoCopy(cur.Object, segs...)
		newVal, _, _ := unstructured.NestedFieldNoCopy(applied, segs...)

		if !jsonEqual(curVal, newVal) {
			out = append(out, strings.Join(segs, ".")+" (owned by "+other+")")
		}
	}

	sort.Strings(out)

	return out
}

// ownedLeaves returns the set of owned leaf paths in an applied config, as
// pathSep-joined segment keys, skipping identity/server-owned fields.
func ownedLeaves(applied map[string]any) map[string]bool {
	out := map[string]bool{}
	collectLeaves(applied, nil, out)

	return out
}

func collectLeaves(node any, prefix []string, out map[string]bool) {
	m, ok := node.(map[string]any)
	if !ok {
		if len(prefix) > 0 {
			out[strings.Join(prefix, pathSep)] = true
		}

		return
	}

	for k, v := range m {
		if skipLeaf(prefix, k) {
			continue
		}

		collectLeaves(v, append(append([]string{}, prefix...), k), out)
	}
}

// skipLeaf reports whether a (prefix, key) should not be tracked as an owned
// field — identity, server-owned metadata, and status.
func skipLeaf(prefix []string, key string) bool {
	if len(prefix) == 0 {
		return ssaSkipTop[key]
	}

	if len(prefix) == 1 && prefix[0] == "metadata" {
		return ssaSkipMeta[key]
	}

	return false
}

// ownerByLeaf inverts the object's managedFields into leaf → manager.
func ownerByLeaf(obj *unstructured.Unstructured) map[string]string {
	out := map[string]string{}

	entries, _, _ := unstructured.NestedSlice(obj.Object, "metadata", "managedFields")
	for _, e := range entries {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}

		mgr, _, _ := unstructured.NestedString(em, "manager")
		fields, _, _ := unstructured.NestedMap(em, "fieldsV1")

		for _, leaf := range leavesFromFieldsV1(fields, nil) {
			out[leaf] = mgr
		}
	}

	return out
}

// updateManagedFields returns the new managedFields slice: this manager owns
// appliedLeaves, and those leaves are removed from every other manager (an apply
// transfers ownership).
func updateManagedFields(
	cur *unstructured.Unstructured, manager string, appliedLeaves map[string]bool, apiVersion string, now metav1.Time,
) []any {
	existing, _, _ := unstructured.NestedSlice(cur.Object, "metadata", "managedFields")

	out := make([]any, 0, len(existing)+1)

	for _, e := range existing {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}

		if mgr, _, _ := unstructured.NestedString(em, "manager"); mgr == manager {
			continue // replaced below
		}

		if kept := subtractLeaves(em, appliedLeaves); kept != nil {
			out = append(out, kept)
		}
	}

	return append(out, managedFieldsEntry(manager, appliedLeaves, apiVersion, now))
}

// subtractLeaves rebuilds a managedFields entry with the given leaves removed,
// or nil if the manager no longer owns anything.
func subtractLeaves(entry map[string]any, remove map[string]bool) map[string]any {
	fields, _, _ := unstructured.NestedMap(entry, "fieldsV1")
	kept := map[string]bool{}

	for _, leaf := range leavesFromFieldsV1(fields, nil) {
		if !remove[leaf] {
			kept[leaf] = true
		}
	}

	if len(kept) == 0 {
		return nil
	}

	entry["fieldsV1"] = fieldsV1FromLeaves(kept)

	return entry
}

func managedFieldsEntry(manager string, leaves map[string]bool, apiVersion string, now metav1.Time) map[string]any {
	return map[string]any{
		"manager":    manager,
		"operation":  "Apply",
		"apiVersion": apiVersion,
		"time":       now.Format(time.RFC3339),
		"fieldsType": "FieldsV1",
		"fieldsV1":   fieldsV1FromLeaves(leaves),
	}
}

func setManagedFields(obj *unstructured.Unstructured, entries []any) {
	_ = unstructured.SetNestedSlice(obj.Object, entries, "metadata", "managedFields")
}

// fieldsV1FromLeaves builds the upstream f:-prefixed nested form from a leaf set.
func fieldsV1FromLeaves(leaves map[string]bool) map[string]any {
	root := map[string]any{}

	for leaf := range leaves {
		cur := root

		for _, seg := range strings.Split(leaf, pathSep) {
			key := "f:" + seg

			next, ok := cur[key].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[key] = next
			}

			cur = next
		}
	}

	return root
}

// leavesFromFieldsV1 inverts fieldsV1FromLeaves back to pathSep-joined leaves.
func leavesFromFieldsV1(fields map[string]any, prefix []string) []string {
	var out []string

	for k, v := range fields {
		seg := strings.TrimPrefix(k, "f:")
		child, ok := v.(map[string]any)

		if !ok || len(child) == 0 {
			out = append(out, strings.Join(append(append([]string{}, prefix...), seg), pathSep))

			continue
		}

		out = append(out, leavesFromFieldsV1(child, append(append([]string{}, prefix...), seg))...)
	}

	return out
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)

	return bytes.Equal(ab, bb)
}

func decodeUnstructuredMap(w http.ResponseWriter, merged any) (*unstructured.Unstructured, bool) {
	m, ok := merged.(map[string]any)
	if !ok {
		writeBadRequest(w, "k8s api: apply produced a non-object result")

		return nil, false
	}

	return &unstructured.Unstructured{Object: m}, true
}
