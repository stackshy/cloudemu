package nosql

import (
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nosqlprovider "github.com/stackshy/cloudemu/v2/providers/oci/nosql"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// serveIndexes routes the index collection and a single index within it.
func (h *Handler) serveIndexes(w http.ResponseWriter, r *http.Request, rt route) {
	if rt.Name == "" {
		switch r.Method {
		case http.MethodPost:
			h.createIndex(w, r, rt.ID)
		case http.MethodGet:
			h.listIndexes(w, r, rt.ID)
		default:
			methodNotAllowed(w, r)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getIndex(w, r, rt.ID, rt.Name)
	case http.MethodDelete:
		h.deleteIndex(w, r, rt.ID, rt.Name)
	default:
		methodNotAllowed(w, r)
	}
}

func (h *Handler) createIndex(w http.ResponseWriter, r *http.Request, tableID string) {
	if !h.requireWorkRequests(w, r) {
		return
	}

	var req createIndexRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	spec := nosqlprovider.IndexSpec{Name: req.Name}
	for _, k := range req.Keys {
		spec.Columns = append(spec.Columns, k.ColumnName)
	}

	table, err := h.findTable(r, tableID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if _, err := h.extras.CreateOCIIndex(r.Context(), tableID, spec, req.IsIfNotExists); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accepted(w, r, opCreateIndex, table.CompartmentID, workrequest.Resource{
		EntityType: entityIndex,
		ActionType: workrequest.ActionCreated,
		Identifier: table.ID,
	})
}

// listIndexes returns a table's indexes. Real OCI marks compartmentId
// optional here; CloudEmu requires it so every list is compartment-scoped.
func (h *Handler) listIndexes(w http.ResponseWriter, r *http.Request, tableID string) {
	if _, given := ocirest.RequireCompartmentID(w, r); !given {
		return
	}

	if _, err := h.findTable(r, tableID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	indexes, err := h.extras.ListOCIIndexes(r.Context(), tableID, r.URL.Query().Get("name"))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	items := make([]indexBody, 0, len(indexes))
	for i := range indexes {
		items = append(items, toIndexBody(&indexes[i]))
	}

	ocirest.WriteJSON(w, r, http.StatusOK, indexCollection{Items: paginate(w, r, items)})
}

func (h *Handler) getIndex(w http.ResponseWriter, r *http.Request, tableID, name string) {
	if _, err := h.findTable(r, tableID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	idx, err := h.extras.GetOCIIndex(r.Context(), tableID, name)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toIndexBody(idx))
}

func (h *Handler) deleteIndex(w http.ResponseWriter, r *http.Request, tableID, name string) {
	if !h.requireWorkRequests(w, r) {
		return
	}

	table, err := h.findTable(r, tableID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ifExists := r.URL.Query().Get("isIfExists") == "true"

	if err := h.extras.DeleteOCIIndex(r.Context(), tableID, name, ifExists); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accepted(w, r, opDeleteIndex, table.CompartmentID, workrequest.Resource{
		EntityType: entityIndex,
		ActionType: workrequest.ActionDeleted,
		Identifier: table.ID,
	})
}

// notFound is the error a table hidden by a compartment filter reports, which
// WriteDriverError collapses into OCI's single NotAuthorizedOrNotFound.
func notFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "table %q not found", id)
}
