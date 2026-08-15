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
	"k8s.io/apimachinery/pkg/runtime"
)

// Server-side apply with field ownership. Each apply request carries a
// fieldManager; the server records which leaf fields that manager owns in
// metadata.managedFields. A later apply by a DIFFERENT manager that changes a
// field the first manager owns is a conflict (HTTP 409) unless force=true, which
// transfers ownership. A re-apply by the SAME manager that OMITS a field it
// previously owned removes that field from the object (upstream apply semantics),
// unless another manager also owns it. Plain PUT/PATCH updates record an
// Update-operation managedFields entry for their fieldManager so ownership
// reflects reality; they take/share ownership rather than conflicting (only
// Apply-vs-Apply is a 409). The one residual shortcut versus upstream SSA is
// granularity: ownership and conflict detection track leaf fields (map keys and
// whole arrays), so per-element structural merging of list items is not modeled.

// pathSep joins path segments internally. A null byte can't appear in a JSON key
// (labels contain dots and slashes, so dot-joining would be ambiguous).
const pathSep = "\x00"

// defaultFieldManager is used when a request omits ?fieldManager=.
const defaultFieldManager = "cloudemu"

// managedFields operation values.
const (
	applyOperation  = "Apply"
	updateOperation = "Update"
)

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
	w http.ResponseWriter, r *http.Request, apiVersion string, cur *unstructured.Unstructured,
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

	// Snapshot server-owned identity BEFORE the merge — mergeRFC7396 mutates
	// cur.Object in place, so reading these after the merge would see the
	// applied body's values (e.g. the `creationTimestamp: null` kubectl sends).
	prevUID := cur.GetUID()
	prevCreation := cur.GetCreationTimestamp()
	prevDeletion := cur.GetDeletionTimestamp()

	manager := r.URL.Query().Get("fieldManager")
	if manager == "" {
		manager = defaultFieldManager
	}

	force := r.URL.Query().Get("force") == "true"
	appliedLeaves := ownedLeaves(applied)
	prevLeaves := managerApplyLeaves(cur, manager)

	if conflicts := s.applyConflicts(cur, applied, appliedLeaves, manager); len(conflicts) > 0 && !force {
		writeStatus(w, http.StatusConflict, metav1.StatusReasonConflict,
			"Apply failed with conflicts: fields managed by another manager: "+strings.Join(conflicts, ", "))

		return nil, false
	}

	merged, mok := decodeUnstructuredMap(w, mergeRFC7396(cur.Object, applied))
	if !mok {
		return nil, false
	}

	// Server-owned identity metadata is never settable by an apply. Re-assert the
	// pre-merge snapshot here — the one place every apply path (typed and
	// registry) flows through — so an applied body carrying `creationTimestamp:
	// null` (kubectl always sends it) or omitting uid/deletionTimestamp cannot
	// blank or re-identify the object via the RFC-7396 merge. ssaSkipMeta already
	// keeps these out of ownership tracking; this protects the values.
	merged.SetUID(prevUID)
	merged.SetCreationTimestamp(prevCreation)
	merged.SetDeletionTimestamp(prevDeletion)

	setManagedFields(merged, updateManagedFields(cur, manager, appliedLeaves, apiVersion, s.now()))
	// Upstream apply removes fields this manager previously owned but now omits,
	// unless another manager still owns them.
	removeDroppedLeaves(merged, diffLeaves(prevLeaves, appliedLeaves))

	return merged, true
}

// managerApplyLeaves returns the leaves currently owned by manager's Apply entry.
func managerApplyLeaves(obj *unstructured.Unstructured, manager string) map[string]bool {
	out := map[string]bool{}

	entries, _, _ := unstructured.NestedSlice(obj.Object, "metadata", "managedFields")
	for _, e := range entries {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}

		mgr, _, _ := unstructured.NestedString(em, "manager")
		if op, _, _ := unstructured.NestedString(em, "operation"); mgr != manager || op != applyOperation {
			continue
		}

		for leaf := range leavesOfEntry(em) {
			out[leaf] = true
		}
	}

	return out
}

// diffLeaves returns leaves present in prev but not in keep.
func diffLeaves(prev, keep map[string]bool) map[string]bool {
	out := map[string]bool{}

	for leaf := range prev {
		if !keep[leaf] {
			out[leaf] = true
		}
	}

	return out
}

// removeDroppedLeaves deletes each dropped leaf from obj unless some managedFields
// entry still owns it (shared ownership survives an omit).
func removeDroppedLeaves(obj *unstructured.Unstructured, dropped map[string]bool) {
	if len(dropped) == 0 {
		return
	}

	owners := ownerByLeaf(obj)

	for leaf := range dropped {
		if _, stillOwned := owners[leaf]; stillOwned {
			continue
		}

		unstructured.RemoveNestedField(obj.Object, strings.Split(leaf, pathSep)...)
	}
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
		"operation":  applyOperation,
		"apiVersion": apiVersion,
		"time":       now.Format(time.RFC3339),
		"fieldsType": "FieldsV1",
		"fieldsV1":   fieldsV1FromLeaves(leaves),
	}
}

// leavesOfEntry returns the leaf set recorded in a managedFields entry's fieldsV1.
func leavesOfEntry(em map[string]any) map[string]bool {
	fields, _, _ := unstructured.NestedMap(em, "fieldsV1")
	out := map[string]bool{}

	for _, leaf := range leavesFromFieldsV1(fields, nil) {
		out[leaf] = true
	}

	return out
}

// unionLeaves returns the union of two leaf sets.
func unionLeaves(a, b map[string]bool) map[string]bool {
	out := make(map[string]bool, len(a)+len(b))

	for leaf := range a {
		out[leaf] = true
	}

	for leaf := range b {
		out[leaf] = true
	}

	return out
}

// updateFieldManager resolves the fieldManager for a plain PUT/PATCH: the query
// param when present, else the leading component of the User-Agent (matching real
// k8s deriving the manager from the client binary), else the default.
func updateFieldManager(r *http.Request) string {
	if m := r.URL.Query().Get("fieldManager"); m != "" {
		return m
	}

	if ua := r.UserAgent(); ua != "" {
		if i := strings.IndexAny(ua, "/ "); i > 0 {
			return ua[:i]
		}

		return ua
	}

	return defaultFieldManager
}

// stampUpdateOwnership records an Update-operation managedFields entry for the
// leaves an update set, merged onto base (the object's prior managedFields). It
// takes/shares ownership without removing other managers' entries.
func (s *ClusterState) stampUpdateOwnership(
	obj *unstructured.Unstructured, base []any, manager, apiVersion string, leaves map[string]bool,
) {
	if len(leaves) == 0 {
		setManagedFields(obj, base)

		return
	}

	setManagedFields(obj, upsertUpdateEntry(base, manager, leaves, apiVersion, s.now()))
}

// upsertUpdateEntry merges leaves into manager's existing Update entry, or appends
// a new one; all other entries are preserved unchanged.
func upsertUpdateEntry(existing []any, manager string, leaves map[string]bool, apiVersion string, now metav1.Time) []any {
	out := make([]any, 0, len(existing)+1)
	merged := false

	for _, e := range existing {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}

		mgr, _, _ := unstructured.NestedString(em, "manager")
		if op, _, _ := unstructured.NestedString(em, "operation"); mgr == manager && op == updateOperation {
			out = append(out, updateEntry(manager, unionLeaves(leavesOfEntry(em), leaves), apiVersion, now))
			merged = true

			continue
		}

		out = append(out, em)
	}

	if !merged {
		out = append(out, updateEntry(manager, leaves, apiVersion, now))
	}

	return out
}

// updateEntry builds an Update-operation managedFields entry.
func updateEntry(manager string, leaves map[string]bool, apiVersion string, now metav1.Time) map[string]any {
	e := managedFieldsEntry(manager, leaves, apiVersion, now)
	e["operation"] = updateOperation

	return e
}

// changedLeaves returns the owned leaves whose value differs between cur and
// patched — the fields a patch actually set.
func changedLeaves(cur, patched map[string]any) map[string]bool {
	out := ownedLeaves(patched)

	for leaf := range out {
		segs := strings.Split(leaf, pathSep)
		a, _, _ := unstructured.NestedFieldNoCopy(cur, segs...)
		b, _, _ := unstructured.NestedFieldNoCopy(patched, segs...)

		if jsonEqual(a, b) {
			delete(out, leaf)
		}
	}

	return out
}

// managedFieldsOf returns an object's current managedFields slice.
func managedFieldsOf(obj *unstructured.Unstructured) []any {
	entries, _, _ := unstructured.NestedSlice(obj.Object, "metadata", "managedFields")

	return entries
}

// objectMap converts a typed object to a generic map via the canonical
// typed↔unstructured converter (not a json round-trip, which silently drops
// unknown fields and doesn't surface shape mismatches).
func objectMap(v any) map[string]any {
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(v)
	if err != nil {
		return nil
	}

	return m
}

// applyOrPatchTyped dispatches a typed PATCH: an `application/apply-patch+yaml`
// body runs through the server-side-apply engine (field ownership + conflicts),
// every other content type is a plain merge/strategic/JSONPatch. apiVersion
// labels the managedFields entry SSA records. This mirrors registryPatch's
// content-type branch for the typed core kinds.
func applyOrPatchTyped[T any](
	s *ClusterState, w http.ResponseWriter, r *http.Request, apiVersion string, cur *T,
) (*T, bool) {
	if r.Header.Get("Content-Type") == contentTypeApplyPatch {
		return serverSideApplyTyped(s, w, r, apiVersion, cur)
	}

	return applyJSONPatch(w, r, cur)
}

// serverSideApplyTyped runs server-side apply for a typed core kind by
// round-tripping the current object through the unstructured SSA engine, which
// carries all the field-ownership and conflict logic. It returns the merged
// typed object (managedFields set), or (nil, false) after the engine has
// already written a 409/400 response.
func serverSideApplyTyped[T any](
	s *ClusterState, w http.ResponseWriter, r *http.Request, apiVersion string, cur *T,
) (*T, bool) {
	curMap := objectMap(cur)
	if curMap == nil {
		writeBadRequest(w, "k8s api: encode current object for apply")

		return nil, false
	}

	merged, ok := s.serverSideApply(w, r, apiVersion, &unstructured.Unstructured{Object: curMap})
	if !ok {
		return nil, false
	}

	return typedFromUnstructured[T](w, merged)
}

// typedFromUnstructured decodes an unstructured object back into a typed T via
// the canonical converter, which errors on a shape mismatch rather than
// silently dropping fields a json round-trip would.
func typedFromUnstructured[T any](w http.ResponseWriter, u *unstructured.Unstructured) (*T, bool) {
	out := new(T)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, out); err != nil {
		writeBadRequest(w, "k8s api: decode merged typed object: "+err.Error())

		return nil, false
	}

	return out, true
}

// typedEntryLeaves returns the leaf set recorded in a typed managedFields entry.
func typedEntryLeaves(e *metav1.ManagedFieldsEntry) map[string]bool {
	out := map[string]bool{}
	if e.FieldsV1 == nil {
		return out
	}

	fields := map[string]any{}
	if json.Unmarshal(e.FieldsV1.Raw, &fields) != nil {
		return out
	}

	for _, leaf := range leavesFromFieldsV1(fields, nil) {
		out[leaf] = true
	}

	return out
}

// typedUpdateEntry builds an Update-operation managedFields entry for a typed object.
func typedUpdateEntry(manager string, leaves map[string]bool, apiVersion string, now metav1.Time) metav1.ManagedFieldsEntry {
	raw, _ := json.Marshal(fieldsV1FromLeaves(leaves))
	stamp := now

	return metav1.ManagedFieldsEntry{
		Manager:    manager,
		Operation:  metav1.ManagedFieldsOperationUpdate,
		APIVersion: apiVersion,
		Time:       &stamp,
		FieldsType: "FieldsV1",
		FieldsV1:   &metav1.FieldsV1{Raw: raw},
	}
}

// upsertTypedUpdateEntry merges leaves into manager's existing Update entry or
// appends a new one, returning a fresh slice (never mutates existing).
func upsertTypedUpdateEntry(
	existing []metav1.ManagedFieldsEntry, manager string, leaves map[string]bool, apiVersion string, now metav1.Time,
) []metav1.ManagedFieldsEntry {
	if len(leaves) == 0 {
		return append([]metav1.ManagedFieldsEntry(nil), existing...)
	}

	out := make([]metav1.ManagedFieldsEntry, 0, len(existing)+1)
	merged := false

	for i := range existing {
		e := &existing[i]
		if e.Manager == manager && e.Operation == metav1.ManagedFieldsOperationUpdate {
			out = append(out, typedUpdateEntry(manager, unionLeaves(typedEntryLeaves(e), leaves), apiVersion, now))
			merged = true

			continue
		}

		out = append(out, *e)
	}

	if !merged {
		out = append(out, typedUpdateEntry(manager, leaves, apiVersion, now))
	}

	return out
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
