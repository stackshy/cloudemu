package kubernetes

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

// resourceDef describes a registry-backed resource kind. A generic store and
// one generic handler serve CRUD / list / watch / patch / delete / subresources
// for every registered kind, so a new kind is a registration (plus an optional
// reconcile hook) rather than a hand-written ~300-line handler file.
type resourceDef struct {
	group      string // "" for core, else "apps", "batch", "networking.k8s.io", …
	version    string // "v1", "v1beta1", …
	kind       string // "ReplicaSet"
	listKind   string // "ReplicaSetList"
	plural     string // "replicasets"
	namespaced bool
	hasStatus  bool // serves the /status subresource
	hasScale   bool // serves the /scale subresource (spec.replicas)
	// reconcile runs (under s.mu) after a successful create/update/patch to
	// materialize children and refresh status. nil = plain CRUD store.
	reconcile func(s *ClusterState, obj *unstructured.Unstructured)
}

func (d *resourceDef) apiVersion() string {
	if d.group == "" {
		return d.version
	}

	return d.group + "/" + d.version
}

// registryStore holds the objects of one registered kind. It is guarded by the
// owning ClusterState's mutex (the same lock the typed handlers use), so a
// reconcile hook can touch typed maps (pods, endpoints) and registry stores
// atomically.
type registryStore struct {
	def   *resourceDef
	items map[string]*unstructured.Unstructured // key: "<ns>/<name>" ("" ns for cluster-scoped)
	watch *broadcaster
	rv    int // monotonic resourceVersion source, bumped on every mutation
}

// registry maps a group/version/plural to its store.
type registry struct {
	stores map[string]*registryStore
}

func regKey(group, version, plural string) string { return group + "/" + version + "/" + plural }

func newRegistry(defs []*resourceDef) *registry {
	r := &registry{stores: make(map[string]*registryStore, len(defs))}
	for _, d := range defs {
		r.stores[regKey(d.group, d.version, d.plural)] = &registryStore{
			def:   d,
			items: make(map[string]*unstructured.Unstructured),
			watch: newBroadcaster(),
		}
	}

	return r
}

func (r *registry) lookup(route *Route) *registryStore {
	if r == nil {
		return nil
	}

	return r.stores[regKey(route.APIGroup, route.APIVersion, route.Resource)]
}

func objKey(namespace, name string) string { return namespace + "/" + name }

// serveRegistry is the generic handler entry point for a registry-backed kind.
func (s *ClusterState) serveRegistry(w http.ResponseWriter, r *http.Request, route *Route, st *registryStore) {
	if route.Subresource != "" {
		s.registrySubresource(w, r, route, st)

		return
	}

	if st.def.namespaced && route.Namespace == "" {
		// Cluster-wide list/watch across namespaces (GET only).
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, "k8s api: "+st.def.plural+" cluster-wide: method not allowed: "+r.Method)

			return
		}

		s.registryList(w, r, st, "")

		return
	}

	if st.def.namespaced && !s.namespaceExists(route.Namespace) {
		writeNotFound(w, "k8s api: namespace not found: "+route.Namespace)

		return
	}

	if route.Name == "" {
		switch r.Method {
		case http.MethodGet:
			s.registryList(w, r, st, route.Namespace)
		case http.MethodPost:
			s.registryCreate(w, r, st, route.Namespace)
		default:
			writeMethodNotAllowed(w, "k8s api: "+st.def.plural+" collection: method not allowed: "+r.Method)
		}

		return
	}

	s.serveRegistryItem(w, r, route, st)
}

// serveRegistryItem dispatches the single-object (named) methods for a
// registry-backed kind.
func (s *ClusterState) serveRegistryItem(w http.ResponseWriter, r *http.Request, route *Route, st *registryStore) {
	switch r.Method {
	case http.MethodGet:
		s.registryGet(w, st, route.Namespace, route.Name)
	case http.MethodPut:
		s.registryUpdate(w, r, st, route.Namespace, route.Name)
	case http.MethodPatch:
		s.registryPatch(w, r, st, route.Namespace, route.Name)
	case http.MethodDelete:
		s.registryDelete(w, r, st, route.Namespace, route.Name)
	default:
		writeMethodNotAllowed(w, "k8s api: "+st.def.plural+" item: method not allowed: "+r.Method)
	}
}

func (s *ClusterState) registryList(w http.ResponseWriter, r *http.Request, st *registryStore, namespace string) {
	if r.URL.Query().Get("watch") == watchQueryValue {
		sel, fields := parseListSelectors(r)
		keep := func(u unstructured.Unstructured) bool {
			return sel.Matches(labels.Set(u.GetLabels())) && matchesFields(&u, fields)
		}

		s.mu.RLock()
		sub := st.watch.subscribe(namespace)
		items := st.snapshotLocked(namespace, r)
		rv := st.rv
		s.mu.RUnlock()
		streamWatch(r.Context(), w, sub, items, keep, watchOpts{
			resume:      watchResume(r),
			bookmarks:   watchBookmarksEnabled(r),
			bookmarkObj: registryBookmark(st, rv),
		})

		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	items := st.snapshotLocked(namespace, r)
	items, cont := listPage(items, r)

	list := &unstructured.UnstructuredList{}
	list.SetAPIVersion(st.def.apiVersion())
	list.SetKind(st.def.listKind)
	list.SetResourceVersion(strconv.Itoa(st.rv))
	list.SetContinue(cont)
	list.Items = items

	writeJSON(w, http.StatusOK, list)
}

// registryBookmark builds the minimal object a BOOKMARK watch event carries for
// a registry-backed kind: just the kind's apiVersion/kind and the store's
// current resourceVersion.
func registryBookmark(st *registryStore, rv int) *unstructured.Unstructured {
	bm := &unstructured.Unstructured{}
	bm.SetAPIVersion(st.def.apiVersion())
	bm.SetKind(st.def.kind)
	bm.SetResourceVersion(strconv.Itoa(rv))

	return bm
}

// snapshotLocked returns a sorted, selector-filtered copy of the store's items
// in namespace ("" = all). Callers hold s.mu.
func (st *registryStore) snapshotLocked(namespace string, r *http.Request) []unstructured.Unstructured {
	sel, _ := labels.Parse(r.URL.Query().Get("labelSelector"))
	fields := parseFieldSelector(r.URL.Query().Get("fieldSelector"))

	keys := make([]string, 0, len(st.items))
	for k := range st.items {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]unstructured.Unstructured, 0, len(keys))

	for _, k := range keys {
		obj := st.items[k]
		if namespace != "" && obj.GetNamespace() != namespace {
			continue
		}

		if sel != nil && !sel.Matches(labels.Set(obj.GetLabels())) {
			continue
		}

		if !matchesFields(obj, fields) {
			continue
		}

		out = append(out, *obj.DeepCopy())
	}

	return out
}

func (s *ClusterState) registryCreate(w http.ResponseWriter, r *http.Request, st *registryStore, namespace string) {
	obj := &unstructured.Unstructured{}
	if !readJSON(w, r, obj) {
		return
	}

	if obj.GetName() == "" {
		writeBadRequest(w, "k8s api: "+st.def.kind+": metadata.name is required")

		return
	}

	if st.def.namespaced {
		obj.SetNamespace(namespace)
	}

	key := objKey(obj.GetNamespace(), obj.GetName())

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := st.items[key]; ok {
		writeAlreadyExists(w, "k8s api: "+st.def.kind+" already exists: "+key)

		return
	}

	obj.SetAPIVersion(st.def.apiVersion())
	obj.SetKind(st.def.kind)
	obj.SetUID(types.UID(newUID()))
	obj.SetCreationTimestamp(s.now())
	obj.SetGeneration(1)

	if isDryRun(r) {
		obj.SetResourceVersion(strconv.Itoa(st.rv + 1))
		writeJSON(w, http.StatusCreated, obj)

		return
	}

	st.stampRVLocked(obj)

	st.items[key] = obj

	if st.def.reconcile != nil {
		st.def.reconcile(s, obj)
	}

	st.watch.publish(EventAdded, obj.GetNamespace(), *obj.DeepCopy())
	writeJSON(w, http.StatusCreated, obj)
}

func (s *ClusterState) registryGet(w http.ResponseWriter, st *registryStore, namespace, name string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, ok := st.items[objKey(namespace, name)]
	if !ok {
		writeNotFound(w, "k8s api: "+st.def.kind+" not found: "+objKey(namespace, name))

		return
	}

	writeJSON(w, http.StatusOK, obj.DeepCopy())
}

func (s *ClusterState) registryUpdate(w http.ResponseWriter, r *http.Request, st *registryStore, namespace, name string) {
	in := &unstructured.Unstructured{}
	if !readJSON(w, r, in) {
		return
	}

	if in.GetName() != name {
		writeBadRequest(w, "k8s api: "+st.def.kind+" name in body does not match URL")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := st.items[objKey(namespace, name)]
	if !ok {
		writeNotFound(w, "k8s api: "+st.def.kind+" not found: "+objKey(namespace, name))

		return
	}

	in.SetNamespace(cur.GetNamespace())
	in.SetUID(cur.GetUID())
	in.SetCreationTimestamp(cur.GetCreationTimestamp())
	in.SetAPIVersion(st.def.apiVersion())
	in.SetKind(st.def.kind)
	// deletionTimestamp is server-owned: a PUT can drop a finalizer but must not
	// resurrect a Terminating object by omitting the timestamp.
	in.SetDeletionTimestamp(cur.GetDeletionTimestamp())
	// Bump generation when the spec changed — controllers compare it against
	// status.observedGeneration.
	if specChanged(cur, in) {
		in.SetGeneration(cur.GetGeneration() + 1)
	} else {
		in.SetGeneration(cur.GetGeneration())
	}

	if isDryRun(r) {
		in.SetResourceVersion(strconv.Itoa(st.rv + 1))
		writeJSON(w, http.StatusOK, in)

		return
	}

	st.stampRVLocked(in)

	// Last finalizer removed on a Terminating object → complete the delete.
	if finalizersDrainedUnstructured(in) {
		delete(st.items, objKey(namespace, name))
		s.garbageCollectLocked(in.GetUID())
		st.watch.publish(EventDeleted, in.GetNamespace(), *in.DeepCopy())
		writeJSON(w, http.StatusOK, in)

		return
	}

	st.items[objKey(namespace, name)] = in

	if st.def.reconcile != nil {
		st.def.reconcile(s, in)
	}

	st.watch.publish(EventModified, in.GetNamespace(), *in.DeepCopy())
	writeJSON(w, http.StatusOK, in)
}

func (s *ClusterState) registryPatch(w http.ResponseWriter, r *http.Request, st *registryStore, namespace, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, ok := st.items[objKey(namespace, name)]
	if !ok {
		writeNotFound(w, "k8s api: "+st.def.kind+" not found: "+objKey(namespace, name))

		return
	}

	patched, ok := s.applyUnstructuredPatch(w, r, cur)
	if !ok {
		return
	}

	if specChanged(cur, patched) {
		patched.SetGeneration(cur.GetGeneration() + 1)
	}

	if isDryRun(r) {
		patched.SetResourceVersion(strconv.Itoa(st.rv + 1))
		writeJSON(w, http.StatusOK, patched)

		return
	}

	st.stampRVLocked(patched)

	// A patch that removes the last finalizer from a Terminating object completes
	// the delete (the patch was applied onto cur, so it inherits its
	// deletionTimestamp).
	if finalizersDrainedUnstructured(patched) {
		delete(st.items, objKey(namespace, name))
		s.garbageCollectLocked(patched.GetUID())
		st.watch.publish(EventDeleted, patched.GetNamespace(), *patched.DeepCopy())
		writeJSON(w, http.StatusOK, patched)

		return
	}

	st.items[objKey(namespace, name)] = patched

	if st.def.reconcile != nil {
		st.def.reconcile(s, patched)
	}

	st.watch.publish(EventModified, patched.GetNamespace(), *patched.DeepCopy())
	writeJSON(w, http.StatusOK, patched)
}

func (s *ClusterState) registryDelete(w http.ResponseWriter, r *http.Request, st *registryStore, namespace, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := objKey(namespace, name)

	obj, ok := st.items[key]
	if !ok {
		writeNotFound(w, "k8s api: "+st.def.kind+" not found: "+key)

		return
	}

	if isDryRun(r) {
		writeJSON(w, http.StatusOK, obj.DeepCopy())

		return
	}

	// Finalizer-gated deletion: an object with finalizers goes Terminating
	// (deletionTimestamp stamped) and stays until the last finalizer is removed
	// via a later update/patch, rather than being deleted now.
	if s.markForDeletionUnstructured(obj) {
		st.stampRVLocked(obj)
		st.watch.publish(EventModified, obj.GetNamespace(), *obj.DeepCopy())
		writeJSON(w, http.StatusOK, obj.DeepCopy())

		return
	}

	delete(st.items, key)
	st.bumpRVLocked()

	// Cascade: garbage-collect anything this object owns (its controlled Pods
	// and any registry-backed children carrying its ownerReference).
	s.garbageCollectLocked(obj.GetUID())

	st.watch.publish(EventDeleted, obj.GetNamespace(), *obj.DeepCopy())
	writeJSON(w, http.StatusOK, obj.DeepCopy())
}

// stampRVLocked bumps the store's resourceVersion counter and stamps it on obj.
// bumpRVLocked bumps without an object (for deletes). Callers hold s.mu.
func (st *registryStore) stampRVLocked(obj *unstructured.Unstructured) {
	st.rv++
	obj.SetResourceVersion(strconv.Itoa(st.rv))
}

func (st *registryStore) bumpRVLocked() { st.rv++ }

// applyUnstructuredPatch merges a JSON-merge-patch body into cur and returns the
// result. Strategic-merge and apply patches are accepted and treated as a merge
// (a documented emulation: the mock has no strategic metadata / field managers).
func (*ClusterState) applyUnstructuredPatch(
	w http.ResponseWriter, r *http.Request, cur *unstructured.Unstructured,
) (*unstructured.Unstructured, bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeBadRequest(w, "k8s api: read patch body: "+err.Error())

		return nil, false
	}

	curBytes, err := json.Marshal(cur.Object)
	if err != nil {
		writeBadRequest(w, "k8s api: marshal current object: "+err.Error())

		return nil, false
	}

	// JSONPatch (RFC 6902) is op-based; everything else (merge, strategic-merge,
	// apply) is applied as an RFC 7396 merge — unstructured has no struct tags
	// for real strategic merging, so strategic degrades to merge (documented).
	var merged []byte

	switch ct := r.Header.Get("Content-Type"); ct {
	case contentTypeJSONPatch:
		m, jok := applyRFC6902(w, curBytes, body)
		if !jok {
			return nil, false
		}

		return decodeUnstructured(w, m)
	case "", contentTypeJSON, contentTypeJSONMergePatch,
		contentTypeStrategicMerge, contentTypeApplyPatch:
		merged, err = mergePatch(curBytes, body)
		if err != nil {
			writeBadRequest(w, "k8s api: apply patch: "+err.Error())

			return nil, false
		}
	default:
		writeBadRequest(w, "k8s api: unsupported patch content-type: "+ct)

		return nil, false
	}

	return decodeUnstructured(w, merged)
}

// decodeUnstructured decodes patched JSON via Unstructured (not plain
// json.Unmarshal into a map): the unstructured scheme preserves whole-number
// JSON as int64, whereas encoding/json yields float64. unstructured.NestedInt64
// accepts only int64, so a float64 would make every integer field (notably
// spec.replicas) read as 0 — a merge-patch to scale up would silently scale the
// workload to zero.
func decodeUnstructured(w http.ResponseWriter, merged []byte) (*unstructured.Unstructured, bool) {
	out := &unstructured.Unstructured{}
	if err := out.UnmarshalJSON(merged); err != nil {
		writeBadRequest(w, "k8s api: decode patched object: "+err.Error())

		return nil, false
	}

	return out, true
}

// applyRFC6902 applies a JSONPatch (RFC 6902) op array to curBytes.
func applyRFC6902(w http.ResponseWriter, curBytes, patch []byte) ([]byte, bool) {
	p, err := jsonpatch.DecodePatch(patch)
	if err != nil {
		writeBadRequest(w, "k8s api: decode json patch: "+err.Error())

		return nil, false
	}

	merged, err := p.Apply(curBytes)
	if err != nil {
		writeBadRequest(w, "k8s api: apply json patch: "+err.Error())

		return nil, false
	}

	return merged, true
}

// specChanged reports whether the two objects' spec differ (used to decide
// whether to bump .metadata.generation).
func specChanged(a, b *unstructured.Unstructured) bool {
	as, _, _ := unstructured.NestedFieldNoCopy(a.Object, "spec")
	bs, _, _ := unstructured.NestedFieldNoCopy(b.Object, "spec")
	ab, _ := json.Marshal(as)
	bb, _ := json.Marshal(bs)

	return !bytes.Equal(ab, bb)
}

// parseFieldSelector parses a comma-separated key=value field selector. Only
// the metadata.name / metadata.namespace / status.phase fields are honored —
// the ones real clients actually select on for the supported kinds.
func parseFieldSelector(sel string) map[string]string {
	if sel == "" {
		return nil
	}

	out := make(map[string]string)

	for _, clause := range strings.Split(sel, ",") {
		k, v, found := strings.Cut(clause, "=")
		if found {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}

	return out
}

// Field selector keys the store can answer. Others fail closed (match nothing).
const (
	fieldMetadataName      = "metadata.name"
	fieldMetadataNamespace = "metadata.namespace"
	fieldStatusPhase       = "status.phase"
	fieldSpecNodeName      = "spec.nodeName"
	// Event field selectors — `kubectl get events --field-selector` and
	// controllers filtering their own Events rely on these. Without them the
	// generic store fell closed (returned nothing) for any Event filter.
	fieldInvolvedName      = "involvedObject.name"
	fieldInvolvedNamespace = "involvedObject.namespace"
	fieldInvolvedKind      = "involvedObject.kind"
	fieldInvolvedUID       = "involvedObject.uid"
	fieldEventReason       = "reason"
	fieldEventType         = "type"
)

func matchesFields(obj *unstructured.Unstructured, fields map[string]string) bool {
	for k, v := range fields {
		if !matchesField(obj, k, v) {
			return false
		}
	}

	return true
}

// matchesField answers a single field-selector clause. Unknown keys fail closed
// (match nothing) rather than silently returning everything — a data-correctness
// hazard for callers that expect the filter to be honored.
func matchesField(obj *unstructured.Unstructured, key, want string) bool {
	switch key {
	case fieldMetadataName:
		return obj.GetName() == want
	case fieldMetadataNamespace:
		return obj.GetNamespace() == want
	case fieldStatusPhase:
		return nestedStringField(obj, want, "status", "phase")
	case fieldInvolvedName:
		return nestedStringField(obj, want, "involvedObject", "name")
	case fieldInvolvedNamespace:
		return nestedStringField(obj, want, "involvedObject", "namespace")
	case fieldInvolvedKind:
		return nestedStringField(obj, want, "involvedObject", "kind")
	case fieldInvolvedUID:
		return nestedStringField(obj, want, "involvedObject", "uid")
	case fieldEventReason:
		return nestedStringField(obj, want, "reason")
	case fieldEventType:
		return nestedStringField(obj, want, "type")
	default:
		return false
	}
}

func nestedStringField(obj *unstructured.Unstructured, want string, path ...string) bool {
	got, _, _ := unstructured.NestedString(obj.Object, path...)

	return got == want
}
