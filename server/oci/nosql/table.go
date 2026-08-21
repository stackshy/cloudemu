package nosql

import (
	"net/http"

	nosqlprovider "github.com/stackshy/cloudemu/v2/providers/oci/nosql"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// createTable creates a table from its DDL statement. Real OCI runs it
// asynchronously, so the response is a 202 carrying the work request.
func (h *Handler) createTable(w http.ResponseWriter, r *http.Request) {
	if !h.requireWorkRequests(w, r) {
		return
	}

	var req createTableRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	if req.TableLimits == nil {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "tableLimits is required")
		return
	}

	spec := nosqlprovider.TableSpec{
		CompartmentID:     req.CompartmentID,
		DDLStatement:      req.DDLStatement,
		Limits:            fromLimitsBody(req.TableLimits),
		IsAutoReclaimable: req.IsAutoReclaimable,
		FreeformTags:      req.FreeformTags,
	}

	table, err := h.extras.CreateOCITable(r.Context(), spec)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	// CreateTableDetails carries a name alongside the DDL; the two must agree,
	// since the DDL is what actually names the table.
	if req.Name != "" && req.Name != table.Name {
		_ = h.extras.DeleteOCITable(r.Context(), table.Name)

		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"name "+req.Name+" does not match the table named by ddlStatement, "+table.Name)

		return
	}

	h.accepted(w, r, opCreateTable, table.CompartmentID, workrequest.Resource{
		EntityType: entityTable,
		ActionType: workrequest.ActionCreated,
		Identifier: table.ID,
	})
}

// listTables returns the tables in a compartment. compartmentId is required,
// as it is in real OCI; the optional name parameter narrows the listing.
func (h *Handler) listTables(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	tables, err := h.extras.ListOCITables(r.Context(), compartmentID, r.URL.Query().Get("name"))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	items := make([]tableBody, 0, len(tables))
	for i := range tables {
		items = append(items, toTableBody(&tables[i]))
	}

	ocirest.WriteJSON(w, r, http.StatusOK, tableCollection{Items: paginate(w, r, items)})
}

func (h *Handler) getTable(w http.ResponseWriter, r *http.Request, id string) {
	table, err := h.findTable(r, id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, toTableBody(table))
}

// updateTable applies an ALTER TABLE statement, new limits and new tags.
func (h *Handler) updateTable(w http.ResponseWriter, r *http.Request, id string) {
	if !h.requireWorkRequests(w, r) {
		return
	}

	var req updateTableRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	upd := nosqlprovider.TableUpdate{
		DDLStatement:      req.DDLStatement,
		IsAutoReclaimable: req.IsAutoReclaimable,
		FreeformTags:      req.FreeformTags,
	}

	if req.TableLimits != nil {
		limits := fromLimitsBody(req.TableLimits)
		upd.Limits = &limits
	}

	table, err := h.extras.UpdateOCITable(r.Context(), id, upd)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accepted(w, r, opUpdateTable, table.CompartmentID, workrequest.Resource{
		EntityType: entityTable,
		ActionType: workrequest.ActionUpdated,
		Identifier: table.ID,
	})
}

func (h *Handler) deleteTable(w http.ResponseWriter, r *http.Request, id string) {
	if !h.requireWorkRequests(w, r) {
		return
	}

	table, err := h.findTable(r, id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if err := h.extras.DeleteOCITable(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accepted(w, r, opDeleteTable, table.CompartmentID, workrequest.Resource{
		EntityType: entityTable,
		ActionType: workrequest.ActionDeleted,
		Identifier: table.ID,
	})
}

func (h *Handler) changeCompartment(w http.ResponseWriter, r *http.Request, id string) {
	if !h.requireWorkRequests(w, r) {
		return
	}

	var req changeCompartmentRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.ToCompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "toCompartmentId is required")
		return
	}

	table, err := h.findTable(r, id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if err := h.extras.ChangeOCITableCompartment(r.Context(), id, req.ToCompartmentID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accepted(w, r, opChangeCompartment, req.ToCompartmentID, workrequest.Resource{
		EntityType: entityTable,
		ActionType: workrequest.ActionUpdated,
		Identifier: table.ID,
	})
}

// findTable resolves a table by name or OCID and, when the caller names a
// compartment, checks the table is visible from it. OCI collapses a table in
// another compartment into the same 404 a missing one gets.
func (h *Handler) findTable(r *http.Request, id string) (*nosqlprovider.Table, error) {
	table, err := h.extras.GetOCITable(r.Context(), id)
	if err != nil {
		return nil, err
	}

	if compartmentID := ocirest.CompartmentID(r); compartmentID != "" && table.CompartmentID != compartmentID {
		return nil, notFound(id)
	}

	return table, nil
}
