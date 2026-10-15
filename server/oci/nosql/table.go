package nosql

import (
	"net/http"
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
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

	if req.Name == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "name is required")
		return
	}

	if !h.requireCompartment(w, r, req.CompartmentID) {
		return
	}

	spec := nosqlprovider.TableSpec{
		Name:              req.Name,
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

	h.accepted(w, r, opCreateTable, table.CompartmentID, workrequest.Resource{
		EntityType: entityTable,
		ActionType: workrequest.ActionCreated,
		Identifier: table.ID,
	})
}

// listTables returns the tables in a compartment. compartmentId is required,
// as it is in real OCI; the optional name and lifecycleState parameters narrow
// the listing, and sortBy and sortOrder order it, timeCreated DESC by default.
func (h *Handler) listTables(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	q := r.URL.Query()

	sortBy, sortOrder, state := q.Get("sortBy"), q.Get("sortOrder"), q.Get("lifecycleState")

	if msg := checkListTables(sortBy, sortOrder, state); msg != "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, msg)
		return
	}

	tables, err := h.extras.ListOCITables(r.Context(), compartmentID, q.Get("name"))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	items := make([]tableBody, 0, len(tables))

	for i := range tables {
		if state != "" && state != stateAll && tables[i].LifecycleState != state {
			continue
		}

		items = append(items, toTableBody(&tables[i]))
	}

	sortTables(items, sortBy, sortOrder)

	ocirest.WriteJSON(w, r, http.StatusOK, tableCollection{Items: paginate(w, r, items)})
}

// checkListTables reports why a ListTables sortBy, sortOrder or
// lifecycleState is outside its enum, or returns the empty string.
func checkListTables(sortBy, sortOrder, state string) string {
	switch {
	case sortBy != "" && sortBy != sortByTimeCreated && sortBy != sortByName:
		return "sortBy " + sortBy + " is not timeCreated or name"
	case sortOrder != "" && sortOrder != sortOrderAsc && sortOrder != sortOrderDesc:
		return "sortOrder " + sortOrder + " is not ASC or DESC"
	case state != "" && !slices.Contains(tableStates, state):
		return "lifecycleState " + state + " is not one of " + strings.Join(tableStates, ", ")
	}

	return ""
}

// sortTables orders a listing by sortBy, timeCreated by default, then by
// name, descending unless sortOrder is ASC.
func sortTables(items []tableBody, sortBy, sortOrder string) {
	slices.SortStableFunc(items, func(a, b tableBody) int {
		c := 0
		if sortBy != sortByName {
			c = strings.Compare(a.TimeCreated, b.TimeCreated)
		}

		if c == 0 {
			c = strings.Compare(a.Name, b.Name)
		}

		if sortOrder != sortOrderAsc {
			c = -c
		}

		return c
	})
}

func (h *Handler) getTable(w http.ResponseWriter, r *http.Request, id string) {
	table, err := h.findTable(r, ocirest.CompartmentID(r), id)
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

	compartmentID := compartmentOf(r, req.CompartmentID)

	if _, err := h.findTable(r, compartmentID, id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	table, err := h.extras.UpdateOCITable(r.Context(), compartmentID, id, upd)
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

	compartmentID := ocirest.CompartmentID(r)

	table, err := h.findTable(r, compartmentID, id)
	if err != nil {
		// isIfExists turns a missing table into a no-op delete.
		if cerrors.IsNotFound(err) && isIfExists(r) {
			h.accepted(w, r, opDeleteTable, compartmentID, workrequest.Resource{
				EntityType: entityTable,
				ActionType: workrequest.ActionDeleted,
				Identifier: id,
			})

			return
		}

		ocirest.WriteDriverError(w, r, err)

		return
	}

	if err := h.extras.DeleteOCITable(r.Context(), compartmentID, id); err != nil {
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

	if !h.requireCompartment(w, r, req.ToCompartmentID) {
		return
	}

	compartmentID := compartmentOf(r, req.FromCompartmentID)

	table, err := h.findTable(r, compartmentID, id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	if err := h.extras.ChangeOCITableCompartment(r.Context(), compartmentID, id, req.ToCompartmentID); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accepted(w, r, opUpdateTable, req.ToCompartmentID, workrequest.Resource{
		EntityType: entityTable,
		ActionType: workrequest.ActionUpdated,
		Identifier: table.ID,
	})
}

// findTable resolves a table by name or OCID within the caller's compartment,
// then checks the table is visible from it — the scoped lookup already is, but
// an OCID resolves across the tenancy. OCI collapses a table in another
// compartment into the same 404 a missing one gets.
func (h *Handler) findTable(r *http.Request, compartmentID, id string) (*nosqlprovider.Table, error) {
	table, err := h.extras.GetOCITable(r.Context(), compartmentID, id)
	if err != nil {
		return nil, err
	}

	if compartmentID != "" && table.CompartmentID != compartmentID {
		return nil, notFound(id)
	}

	return table, nil
}

// isIfExists reads the isIfExists flag OCI's delete routes take.
func isIfExists(r *http.Request) bool { return r.URL.Query().Get("isIfExists") == "true" }

// compartmentOf takes the compartment a request body names, falling back to
// the compartmentId query parameter. OCI puts it in the body on the routes
// that have one and in the query string on the rest.
func compartmentOf(r *http.Request, body string) string {
	if body != "" {
		return body
	}

	return ocirest.CompartmentID(r)
}
