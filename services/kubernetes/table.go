package kubernetes

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/duration"
)

// This file is the writeList/writeObject seam (#871 server-side Table printing +
// #872 list-level resourceVersion). Every list handler funnels its response
// through writeList and every single-object GET through writeObject, so both the
// list resourceVersion stamp and Accept-driven Table conversion live in one
// place. writeJSON stays the raw encoder for structurally-different payloads
// (scale/status subresources, SubjectAccessReview, discovery, metrics) that must
// NOT be table-projected.

// tableProjector renders one kind into `kubectl get` columns. Registry-backed
// kinds hang one off resourceDef.tableColumns (declared next to the kind); the
// hand-written typed kinds resolve theirs from projectorForKind.
type tableProjector struct {
	columns []metav1.TableColumnDefinition
	// cells returns the row values for one object, in column order. age is the
	// object's creationTimestamp-relative age, precomputed from the cluster clock
	// so it is deterministic under a FakeClock.
	cells func(u *unstructured.Unstructured, age string) []any
}

// resourceVersionSetter is satisfied by every typed *…List (via the embedded
// metav1.ListMeta) and by *unstructured.UnstructuredList, so writeList can stamp
// the list-level resourceVersion uniformly.
type resourceVersionSetter interface {
	SetResourceVersion(string)
}

const (
	tableGroup      = "meta.k8s.io"
	tableAPIVersion = tableGroup + "/v1"
	// includeObject modes for a Table response (metav1.TableOptions.IncludeObject).
	includeObjectMetadata = "Metadata"
	includeObjectNone     = "None"
)

// wantsTable reports whether the request's Accept header asks for a
// meta.k8s.io Table (`Accept: application/json;as=Table;v=v1;g=meta.k8s.io`),
// which kubectl sends for `kubectl get`.
func wantsTable(r *http.Request) bool {
	for _, media := range strings.Split(r.Header.Get("Accept"), ",") {
		if !strings.Contains(media, "as=Table") {
			continue
		}

		if acceptParam(media, "g") == tableGroup || strings.Contains(media, "meta.k8s.io") {
			return true
		}
	}

	return false
}

// includeObjectMode is the TableOptions.IncludeObject the client asked for. It
// may ride the Accept header params or the query string; default is Object (the
// full object embedded in each row), matching apiserver defaulting.
func includeObjectMode(r *http.Request) string {
	if q := r.URL.Query().Get("includeObject"); q != "" {
		return q
	}

	for _, media := range strings.Split(r.Header.Get("Accept"), ",") {
		if v := acceptParam(media, "includeObject"); v != "" {
			return v
		}
	}

	return "Object"
}

// acceptParam extracts a `;key=value` parameter from one Accept media-type
// clause (trimming whitespace).
func acceptParam(media, key string) string {
	for _, part := range strings.Split(media, ";") {
		p := strings.TrimSpace(part)
		if k, v, ok := strings.Cut(p, "="); ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}

	return ""
}

// writeList stamps the list-level resourceVersion and renders the list — as a
// Table when the client's Accept asks for one, else as the plain typed/unstructured
// list. Callers hold s.mu (read lock), so clusterRVLocked reads a consistent RV.
func (s *ClusterState) writeList(w http.ResponseWriter, r *http.Request, list any) {
	s.writeListWithColumns(w, r, list, nil)
}

// writeListWithColumns is writeList with an explicit Table projector, supplied by
// registry-backed kinds (resourceDef.tableColumns). A nil override falls back to
// the typed-kind switch, then to the NAME/AGE generic table.
func (s *ClusterState) writeListWithColumns(w http.ResponseWriter, r *http.Request, list any, override *tableProjector) {
	if rv, ok := list.(resourceVersionSetter); ok {
		rv.SetResourceVersion(s.clusterRVLocked())
	}

	if !wantsTable(r) {
		writeJSON(w, http.StatusOK, list)

		return
	}

	ul, err := asUnstructuredList(list)
	if err != nil {
		// Never 500 a `kubectl get`: fall back to the plain list encoding.
		writeJSON(w, http.StatusOK, list)

		return
	}

	kind := strings.TrimSuffix(ul.GetKind(), "List")
	proj := resolveProjector(kind, override)

	table := &metav1.Table{
		TypeMeta: metav1.TypeMeta{Kind: "Table", APIVersion: tableAPIVersion},
		ListMeta: metav1.ListMeta{
			ResourceVersion: ul.GetResourceVersion(),
			Continue:        ul.GetContinue(),
		},
		ColumnDefinitions: proj.columns,
	}

	mode := includeObjectMode(r)

	for i := range ul.Items {
		item := &ul.Items[i]
		item.SetAPIVersion(ul.GetAPIVersion())
		item.SetKind(kind)
		table.Rows = append(table.Rows, s.tableRow(item, proj, mode))
	}

	writeJSON(w, http.StatusOK, table)
}

// writeObject renders a single object — as a one-row Table when the client asks
// for one, else as the plain object. Used by the GET-item handlers.
func (s *ClusterState) writeObject(w http.ResponseWriter, r *http.Request, obj any) {
	s.writeObjectWithColumns(w, r, obj, nil)
}

func (s *ClusterState) writeObjectWithColumns(w http.ResponseWriter, r *http.Request, obj any, override *tableProjector) {
	if !wantsTable(r) {
		writeJSON(w, http.StatusOK, obj)

		return
	}

	u, err := asUnstructured(obj)
	if err != nil {
		writeJSON(w, http.StatusOK, obj)

		return
	}

	proj := resolveProjector(u.GetKind(), override)

	table := &metav1.Table{
		TypeMeta:          metav1.TypeMeta{Kind: "Table", APIVersion: tableAPIVersion},
		ListMeta:          metav1.ListMeta{ResourceVersion: u.GetResourceVersion()},
		ColumnDefinitions: proj.columns,
		Rows:              []metav1.TableRow{s.tableRow(u, proj, includeObjectMode(r))},
	}

	writeJSON(w, http.StatusOK, table)
}

// tableRow builds one Table row: the projected cells plus the embedded object
// (full object, PartialObjectMetadata, or nothing) per the includeObject mode.
func (s *ClusterState) tableRow(u *unstructured.Unstructured, proj *tableProjector, mode string) metav1.TableRow {
	row := metav1.TableRow{Cells: proj.cells(u, ageOf(u, s.now().Time.UnixNano()))}

	switch mode {
	case includeObjectNone:
		// no object embedded
	case includeObjectMetadata:
		row.Object = runtime.RawExtension{Object: partialObjectMetadata(u)}
	default:
		row.Object = runtime.RawExtension{Object: u}
	}

	return row
}

// partialObjectMetadata projects an object down to the meta.k8s.io
// PartialObjectMetadata shape a Table row carries when includeObject=Metadata.
func partialObjectMetadata(u *unstructured.Unstructured) *metav1.PartialObjectMetadata {
	pom := &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{Kind: "PartialObjectMetadata", APIVersion: tableAPIVersion},
	}

	if meta, ok := u.Object["metadata"]; ok {
		if b, err := json.Marshal(map[string]any{"metadata": meta}); err == nil {
			_ = json.Unmarshal(b, pom)
		}
	}

	return pom
}

// asUnstructuredList coerces a typed *…List or an *unstructured.UnstructuredList
// into an UnstructuredList so the Table projector can read items uniformly.
func asUnstructuredList(list any) (*unstructured.UnstructuredList, error) {
	if ul, ok := list.(*unstructured.UnstructuredList); ok {
		return ul, nil
	}

	b, err := json.Marshal(list)
	if err != nil {
		return nil, err
	}

	ul := &unstructured.UnstructuredList{}
	if err := ul.UnmarshalJSON(b); err != nil {
		return nil, err
	}

	return ul, nil
}

// asUnstructured coerces a single typed or unstructured object into an
// *unstructured.Unstructured.
func asUnstructured(obj any) (*unstructured.Unstructured, error) {
	if u, ok := obj.(*unstructured.Unstructured); ok {
		return u, nil
	}

	b, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}

	u := &unstructured.Unstructured{}
	if err := u.UnmarshalJSON(b); err != nil {
		return nil, err
	}

	return u, nil
}

// resolveProjector picks the Table projector for kind: an explicit registry
// override wins, else the typed-kind switch, else the generic NAME/AGE table
// (so an unknown kind or CRD never 500s a `kubectl get`).
func resolveProjector(kind string, override *tableProjector) *tableProjector {
	if override != nil {
		return override
	}

	if p := projectorForKind(kind); p != nil {
		return p
	}

	return fallbackProjector()
}

// ageOf renders an object's creationTimestamp-relative age (kubectl's AGE
// column). nowUnixNano is the cluster clock's current time so the value is
// deterministic under a FakeClock.
func ageOf(u *unstructured.Unstructured, nowUnixNano int64) string {
	ct := u.GetCreationTimestamp()
	if ct.IsZero() {
		return "<unknown>"
	}

	d := nowUnixNano - ct.UnixNano()
	if d < 0 {
		d = 0
	}

	return duration.HumanDuration(time.Duration(d))
}
