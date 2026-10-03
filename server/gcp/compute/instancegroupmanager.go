package compute

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	gcecompute "github.com/stackshy/cloudemu/v2/providers/gcp/compute"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// migBackend is the GCP-local capability the GCE Mock implements to store zonal
// managed instance groups (compute#instanceGroupManager). Reached via a type
// assertion so the shared compute driver interface stays unchanged, mirroring
// the volumeResizer / resourceLabelMutator pattern used for disks/images.
type migBackend interface {
	CreateInstanceGroupManagerGCP(igm gcecompute.InstanceGroupManager) error
	GetInstanceGroupManagerGCP(project, zone, name string) (gcecompute.InstanceGroupManager, bool)
	ListInstanceGroupManagersGCP(project, zone string) []gcecompute.InstanceGroupManager
	AllInstanceGroupManagersGCP(project string) []gcecompute.InstanceGroupManager
	DeleteInstanceGroupManagerGCP(project, zone, name string) error
	ResizeInstanceGroupManagerGCP(project, zone, name string, size int) error
	PatchInstanceGroupManagerGCP(project, scope, name string, patched gcecompute.InstanceGroupManager) error
}

// migRequest mirrors the subset of compute#instanceGroupManager we accept on
// insert. targetSize is a flexInt so both the typed client (quoted) and the
// Terraform provider (bare number) encodings decode.
type migRequest struct {
	Name             string  `json:"name"`
	BaseInstanceName string  `json:"baseInstanceName,omitempty"`
	InstanceTemplate string  `json:"instanceTemplate,omitempty"`
	TargetSize       flexInt `json:"targetSize,omitempty"`
	Versions         []struct {
		InstanceTemplate string `json:"instanceTemplate,omitempty"`
	} `json:"versions,omitempty"`
}

// template returns the instance template the group runs: the top-level field,
// or the first version's template (what Terraform sends).
func (req *migRequest) template() string {
	if req.InstanceTemplate == "" && len(req.Versions) > 0 {
		return req.Versions[0].InstanceTemplate
	}

	return req.InstanceTemplate
}

// migResponse mirrors the subset of compute#instanceGroupManager we return.
type migResponse struct {
	Kind              string             `json:"kind"`
	ID                string             `json:"id"`
	CreationTimestamp string             `json:"creationTimestamp,omitempty"`
	Name              string             `json:"name"`
	Zone              string             `json:"zone,omitempty"`
	Region            string             `json:"region,omitempty"`
	BaseInstanceName  string             `json:"baseInstanceName,omitempty"`
	InstanceTemplate  string             `json:"instanceTemplate,omitempty"`
	InstanceGroup     string             `json:"instanceGroup"`
	TargetSize        int                `json:"targetSize"`
	Fingerprint       string             `json:"fingerprint,omitempty"`
	CurrentActions    *migCurrentActions `json:"currentActions,omitempty"`
	Status            *migStatus         `json:"status,omitempty"`
	SelfLink          string             `json:"selfLink"`

	// spec is the stored insert body; MarshalJSON echoes its fields under the
	// computed ones above.
	spec json.RawMessage
}

// MarshalJSON writes the computed fields over the stored insert body.
//
//nolint:gocritic // value receiver so both values and pointers marshal
func (m migResponse) MarshalJSON() ([]byte, error) {
	type plain migResponse

	return mergeSpecJSON(m.spec, plain(m))
}

// migCurrentActions is compute#instanceGroupManagerActionsSummary. The emulator
// applies resizes synchronously, so every target is "none" (stable). None
// equals the target size and every transient counter is zero.
type migCurrentActions struct {
	None                   int `json:"none"`
	Creating               int `json:"creating"`
	CreatingWithoutRetries int `json:"creatingWithoutRetries"`
	Deleting               int `json:"deleting"`
	Abandoning             int `json:"abandoning"`
	Restarting             int `json:"restarting"`
	Refreshing             int `json:"refreshing"`
	Verifying              int `json:"verifying"`
	Recreating             int `json:"recreating"`
}

type migStatus struct {
	IsStable bool `json:"isStable"`
}

type migListResponse struct {
	Kind          string        `json:"kind"`
	ID            string        `json:"id"`
	Items         []migResponse `json:"items"`
	NextPageToken string        `json:"nextPageToken,omitempty"`
	SelfLink      string        `json:"selfLink"`
}

// serveInstanceGroupManagersRoute dispatches the zonal instanceGroupManagers
// resource. Registered ahead of the compute-space fallback so first-match-wins
// keeps these paths here; disjoint from the load-balancing handler's
// instanceGroups collection, so registration order is unconstrained.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveInstanceGroupManagersRoute(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	backend, ok := h.compute.(migBackend)
	if !ok {
		writeNotImplemented(w, "instanceGroupManagers")
		return
	}

	if rp.ResourceName == "" {
		switch r.Method {
		case http.MethodPost:
			h.insertMIG(w, r, rp, backend)
		case http.MethodGet:
			h.listMIGs(w, r, rp, backend)
		default:
			writeNotImplemented(w, r.Method+" "+r.URL.Path)
		}

		return
	}

	if r.Method == http.MethodPost && rp.Action != "" {
		h.serveMIGAction(w, r, rp, backend)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getMIG(w, r, rp, backend)
	case http.MethodDelete:
		h.deleteMIG(w, r, rp, backend)
	case http.MethodPatch:
		h.patchMIG(w, r, rp, backend)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

// patchMIG handles PATCH .../instanceGroupManagers/{name}, the Terraform update
// path. The body is a JSON merge patch over the stored group: a field present
// replaces the stored one, null clears it. targetSize keeps the current value
// (which a resize may have changed) unless the patch sets it.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) patchMIG(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	var patch map[string]json.RawMessage
	if !gcprest.DecodeJSON(w, r, &patch) {
		return
	}

	igm, ok := backend.GetInstanceGroupManagerGCP(rp.Project, rp.ScopeName, rp.ResourceName)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"The resource 'instanceGroupManagers/"+rp.ResourceName+"' was not found")

		return
	}

	spec, err := mergePatch(igm.Spec, patch)
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}

	var req migRequest
	if err := json.Unmarshal(spec, &req); err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}

	if _, set := patch["targetSize"]; set {
		igm.TargetSize = int(req.TargetSize)
	}

	if req.BaseInstanceName != "" {
		igm.BaseInstanceName = req.BaseInstanceName
	}

	if t := req.template(); t != "" {
		igm.InstanceTemplate = lastSegment(t)
	}

	igm.Spec = spec

	if err := backend.PatchInstanceGroupManagerGCP(rp.Project, rp.ScopeName, rp.ResourceName, igm); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"instanceGroupManagers", rp.ResourceName, "patch")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// mergePatch applies a top-level JSON merge patch to the stored spec: present
// fields replace, null fields are removed.
func mergePatch(spec json.RawMessage, patch map[string]json.RawMessage) (json.RawMessage, error) {
	merged := map[string]json.RawMessage{}
	if len(spec) > 0 {
		_ = json.Unmarshal(spec, &merged)
	}

	for k, v := range patch {
		if string(v) == "null" {
			delete(merged, k)
		} else {
			merged[k] = v
		}
	}

	return json.Marshal(merged)
}

// serveMIGAction routes the POST MIG verbs. resize is the real zonal-MIG method
// (size is a query parameter); setTargetSize is accepted as a body-carried
// alias for clients that prefer it.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveMIGAction(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	switch strings.ToLower(rp.Action) {
	case actionResize:
		h.resizeMIG(w, r, rp, backend)
	case "settargetsize":
		h.setMIGTargetSize(w, r, rp, backend)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertMIG(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	if rp.Scope != gcprest.ScopeZones && rp.Scope != gcprest.ScopeRegions {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "instance group managers must be created in a zone or region")
		return
	}

	var spec json.RawMessage
	if !gcprest.DecodeJSON(w, r, &spec) {
		return
	}

	var req migRequest
	if err := json.Unmarshal(spec, &req); err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}

	if req.Name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "instance group manager name required")
		return
	}

	baseName := req.BaseInstanceName
	if baseName == "" {
		baseName = req.Name
	}

	igm := gcecompute.InstanceGroupManager{
		Project:          rp.Project,
		Name:             req.Name,
		TargetSize:       int(req.TargetSize),
		BaseInstanceName: baseName,
		InstanceTemplate: lastSegment(req.template()),
		Spec:             spec,
	}

	if rp.Scope == gcprest.ScopeRegions {
		igm.Region = rp.ScopeName
	} else {
		igm.Zone = rp.ScopeName
	}

	err := backend.CreateInstanceGroupManagerGCP(igm)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"instanceGroupManagers", req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) getMIG(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	igm, ok := backend.GetInstanceGroupManagerGCP(rp.Project, rp.ScopeName, rp.ResourceName)
	if !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"The resource 'instanceGroupManagers/"+rp.ResourceName+"' was not found")

		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toMIGResponse(&igm, rp.Project, hostFromRequest(r)))
}

//nolint:gocritic // rp is a request-scoped value
func (*Handler) listMIGs(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	igms := backend.ListInstanceGroupManagersGCP(rp.Project, rp.ScopeName)
	host := hostFromRequest(r)
	out := make([]migResponse, 0, len(igms))

	for i := range igms {
		out = append(out, toMIGResponse(&igms[i], rp.Project, host))
	}

	items, next, ok := filterPage(w, r, out, func(m migResponse) string { return m.Name })
	if !ok {
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, migListResponse{
		Kind:          "compute#instanceGroupManagerList",
		ID:            "projects/" + rp.Project + "/" + rp.Scope + "/" + rp.ScopeName + "/instanceGroupManagers",
		Items:         items,
		NextPageToken: next,
		SelfLink:      gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, "instanceGroupManagers", ""),
	})
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteMIG(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	if _, ok := backend.GetInstanceGroupManagerGCP(rp.Project, rp.ScopeName, rp.ResourceName); !ok {
		gcprest.WriteError(w, http.StatusNotFound, "notFound",
			"The resource 'instanceGroupManagers/"+rp.ResourceName+"' was not found")

		return
	}

	if err := backend.DeleteInstanceGroupManagerGCP(rp.Project, rp.ScopeName, rp.ResourceName); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"instanceGroupManagers", rp.ResourceName, "delete")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// resizeMIG handles POST .../instanceGroupManagers/{name}/resize?size=N, the
// real zonal MIG resize where size is a required query parameter.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) resizeMIG(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	raw := r.URL.Query().Get("size")
	if raw == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "size query parameter required")
		return
	}

	size, err := strconv.Atoi(raw)
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "size must be an integer")
		return
	}

	h.applyMIGResize(w, r, rp, backend, size)
}

// migTargetSizeRequest is the setTargetSize body alias.
type migTargetSizeRequest struct {
	TargetSize flexInt `json:"targetSize,omitempty"`
}

// setMIGTargetSize handles POST .../instanceGroupManagers/{name}/setTargetSize
// with the target in the body, a convenience alias over resize.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) setMIGTargetSize(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend) {
	var req migTargetSizeRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	h.applyMIGResize(w, r, rp, backend, int(req.TargetSize))
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) applyMIGResize(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, backend migBackend, size int) {
	if err := backend.ResizeInstanceGroupManagerGCP(rp.Project, rp.ScopeName, rp.ResourceName, size); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"instanceGroupManagers", rp.ResourceName, "resize")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// migScopedList is one zone's bucket in an aggregated MIG list.
type migScopedList struct {
	InstanceGroupManagers []migResponse      `json:"instanceGroupManagers,omitempty"`
	Warning               *scopedListWarning `json:"warning,omitempty"`
}

type migAggregatedListResponse struct {
	Kind          string                   `json:"kind"`
	ID            string                   `json:"id"`
	Items         map[string]migScopedList `json:"items"`
	NextPageToken string                   `json:"nextPageToken,omitempty"`
	SelfLink      string                   `json:"selfLink"`
}

// aggregatedListMIGs handles GET /aggregated/instanceGroupManagers, grouping
// every MIG by its "zones/{zone}" scope.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) aggregatedListMIGs(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	backend, ok := h.compute.(migBackend)
	if !ok {
		writeNotImplemented(w, "instanceGroupManagers")
		return
	}

	igms := backend.AllInstanceGroupManagersGCP(rp.Project)
	host := hostFromRequest(r)
	all := make([]scopedItem[migResponse], 0, len(igms))

	for i := range igms {
		key := "zones/" + igms[i].Zone
		if igms[i].Zone == "" {
			key = "regions/" + igms[i].Region
		}

		all = append(all, scopedItem[migResponse]{scope: key, item: toMIGResponse(&igms[i], rp.Project, host)})
	}

	grouped, next, ok := aggregatedPage(w, r, all, func(m migResponse) string { return m.Name })
	if !ok {
		return
	}

	items := make(map[string]migScopedList, len(grouped))
	for key, list := range grouped {
		items[key] = migScopedList{InstanceGroupManagers: list}
	}

	gcprest.WriteJSON(w, http.StatusOK, migAggregatedListResponse{
		Kind:          "compute#instanceGroupManagerAggregatedList",
		ID:            "projects/" + rp.Project + "/aggregated/instanceGroupManagers",
		Items:         items,
		NextPageToken: next,
		SelfLink:      strings.TrimSuffix(host, "/") + "/compute/v1/projects/" + rp.Project + "/aggregated/instanceGroupManagers",
	})
}

// toMIGResponse maps a stored MIG to compute#instanceGroupManager wire JSON.
// The instanceGroup selfLink points at the same-named instanceGroups resource
// in the group's zone or region, as real GCP does (the managed group owns an
// unmanaged instance group of the same name).
func toMIGResponse(igm *gcecompute.InstanceGroupManager, project, host string) migResponse {
	scope, scopeName := gcprest.ScopeZones, igm.Zone
	if igm.Zone == "" {
		scope, scopeName = gcprest.ScopeRegions, igm.Region
	}

	scopeURL := strings.TrimSuffix(host, "/") + "/compute/v1/projects/" + project + "/" + scope + "/" + scopeName

	resp := migResponse{
		Kind:              "compute#instanceGroupManager",
		ID:                numericID(scopeName + "/" + igm.Name),
		CreationTimestamp: igm.CreatedAt,
		Name:              igm.Name,
		BaseInstanceName:  igm.BaseInstanceName,
		InstanceTemplate:  igm.InstanceTemplate,
		InstanceGroup:     gcprest.SelfLink(host, project, scope, scopeName, "instanceGroups", igm.Name),
		TargetSize:        igm.TargetSize,
		CurrentActions:    &migCurrentActions{None: igm.TargetSize},
		Status:            &migStatus{IsStable: true},
		SelfLink:          gcprest.SelfLink(host, project, scope, scopeName, "instanceGroupManagers", igm.Name),
		spec:              igm.Spec,
	}

	if igm.Zone != "" {
		resp.Zone = scopeURL
	} else {
		resp.Region = scopeURL
	}

	return resp
}

// mergeSpecJSON marshals typed and lays its fields over the stored request body
// spec, so a read echoes what the caller sent while the computed fields win.
// Null and empty fields of the body are dropped, as GCP omits unset fields.
func mergeSpecJSON(spec json.RawMessage, typed any) ([]byte, error) {
	out, err := json.Marshal(typed)
	if err != nil || len(spec) == 0 {
		return out, err
	}

	dec := json.NewDecoder(bytes.NewReader(spec))
	dec.UseNumber()

	var body map[string]any
	if dec.Decode(&body) != nil {
		return out, nil
	}

	merged, _ := pruneEmpty(body).(map[string]any)
	if merged == nil {
		merged = map[string]any{}
	}

	computed := map[string]json.RawMessage{}
	if err := json.Unmarshal(out, &computed); err != nil {
		return nil, err
	}

	for k, v := range computed {
		merged[k] = v
	}

	return json.Marshal(merged)
}

// pruneEmpty removes nulls, empty objects and empty arrays from a decoded JSON
// value, recursively. It returns nil when v itself ends up empty.
func pruneEmpty(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			if p := pruneEmpty(e); p == nil {
				delete(t, k)
			} else {
				t[k] = p
			}
		}

		if len(t) == 0 {
			return nil
		}
	case []any:
		out := t[:0]

		for _, e := range t {
			if p := pruneEmpty(e); p != nil {
				out = append(out, p)
			}
		}

		if len(out) == 0 {
			return nil
		}

		return out
	}

	return v
}
