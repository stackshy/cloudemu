package compute

import (
	"net/http"
	"path"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// operationList is the compute#operationList wire shape.
type operationList struct {
	Kind          string               `json:"kind"`
	ID            string               `json:"id"`
	Items         []*gcprest.Operation `json:"items"`
	NextPageToken string               `json:"nextPageToken,omitempty"`
	SelfLink      string               `json:"selfLink"`
}

// operationScopedList is one scope's entry in an aggregated operation list.
type operationScopedList struct {
	Operations []*gcprest.Operation `json:"operations"`
}

// operationAggregatedList is the compute#operationAggregatedList wire shape.
type operationAggregatedList struct {
	Kind          string                         `json:"kind"`
	ID            string                         `json:"id"`
	Items         map[string]operationScopedList `json:"items"`
	NextPageToken string                         `json:"nextPageToken,omitempty"`
	SelfLink      string                         `json:"selfLink"`
}

// serveOperations handles the zone/region/global operations collection: get,
// the POST wait verb, list and delete. The mock runs synchronously, so every
// stored operation is DONE and wait returns it at once. gcloud and the typed
// google clients confirm a mutation with wait, and Terraform polls get and
// reads targetLink, so both return the operation exactly as it was minted. A
// name that was never issued, or was deleted, is 404 as on real GCP.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serveOperations(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	verb := r.Method
	if rp.Action != "" {
		verb += " " + strings.ToLower(rp.Action)
	}

	switch {
	case rp.ResourceName == "" && verb == http.MethodGet:
		h.listOperations(w, r, rp)
	case rp.ResourceName != "" && verb == http.MethodDelete:
		h.deleteOperation(w, rp)
	case rp.ResourceName != "" && (verb == http.MethodGet || verb == http.MethodPost+" wait"):
		h.getOperation(w, rp)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getOperation(w http.ResponseWriter, rp gcprest.ResourcePath) {
	op, ok := h.ops.Get(rp.Project, rp.Scope, rp.ScopeName, rp.ResourceName)
	if !ok {
		writeOperationNotFound(w, rp.ResourceName)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// deleteOperation removes a stored operation. Real GCP answers with an empty
// body.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteOperation(w http.ResponseWriter, rp gcprest.ResourcePath) {
	if !h.ops.Delete(rp.Project, rp.Scope, rp.ScopeName, rp.ResourceName) {
		writeOperationNotFound(w, rp.ResourceName)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
}

func writeOperationNotFound(w http.ResponseWriter, name string) {
	gcprest.WriteError(w, http.StatusNotFound, "notFound",
		"The resource 'operations/"+name+"' was not found")
}

// listOperations handles GET .../{scope}/operations: the operations stored for
// this project and scope, filtered and paged in creation order.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listOperations(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	page, next, ok := filterPage(w, r, operationPtrs(h.ops.List(rp.Project, rp.Scope, rp.ScopeName)), operationOrder)
	if !ok {
		return
	}

	id := "projects/" + rp.Project + "/" + rp.Scope
	if rp.ScopeName != "" {
		id += "/" + rp.ScopeName
	}

	gcprest.WriteJSON(w, http.StatusOK, operationList{
		Kind:          "compute#operationList",
		ID:            id + "/operations",
		Items:         page,
		NextPageToken: next,
		SelfLink:      gcprest.SelfLink(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName, resourceOperations, ""),
	})
}

// aggregatedListOperations handles GET .../aggregated/operations: every
// operation in the project grouped under "zones/{z}", "regions/{r}" or
// "global".
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) aggregatedListOperations(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	ops := operationPtrs(h.ops.List(rp.Project, "", ""))
	all := make([]scopedItem[*gcprest.Operation], 0, len(ops))

	for _, op := range ops {
		all = append(all, scopedItem[*gcprest.Operation]{scope: operationScopeKey(op), item: op})
	}

	grouped, next, ok := aggregatedPage(w, r, all, operationOrder)
	if !ok {
		return
	}

	items := make(map[string]operationScopedList, len(grouped))
	for key, list := range grouped {
		items[key] = operationScopedList{Operations: list}
	}

	gcprest.WriteJSON(w, http.StatusOK, operationAggregatedList{
		Kind:          "compute#operationAggregatedList",
		ID:            "projects/" + rp.Project + "/aggregated/operations",
		Items:         items,
		NextPageToken: next,
		SelfLink: strings.TrimSuffix(hostFromRequest(r), "/") +
			"/compute/v1/projects/" + rp.Project + "/aggregated/operations",
	})
}

func operationPtrs(ops []gcprest.Operation) []*gcprest.Operation {
	out := make([]*gcprest.Operation, len(ops))
	for i := range ops {
		out[i] = &ops[i]
	}

	return out
}

// operationOrder keys an operation by its id (the mint time in nanoseconds,
// zero-padded so it sorts as a string) and then its name, so lists and their
// page tokens follow creation order.
func operationOrder(op *gcprest.Operation) string {
	return strings.Repeat("0", max(0, opIDWidth-len(op.ID))) + op.ID + "\x00" + op.Name
}

// opIDWidth is the decimal width of the largest uint64 operation id.
const opIDWidth = 20

// operationScopeKey is the aggregated-list key of op: its zone, its region, or
// global.
func operationScopeKey(op *gcprest.Operation) string {
	switch {
	case op.Zone != "":
		return gcprest.ScopeZones + "/" + path.Base(op.Zone)
	case op.Region != "":
		return gcprest.ScopeRegions + "/" + path.Base(op.Region)
	default:
		return gcprest.ScopeGlobal
	}
}
