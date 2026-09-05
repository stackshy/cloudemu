package bigquery

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	"github.com/stackshy/cloudemu/v2/services/bigquery/driver"
)

// insertTable handles POST /datasets/{d}/tables.
func (h *Handler) insertTable(w http.ResponseWriter, r *http.Request, rt route) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	var wt wireTable
	if !decodeJSON(w, body, &wt) {
		return
	}

	tbl := tableFromWire(&wt)
	if wt.TableReference != nil && wt.TableReference.TableID != "" {
		tbl.TableID = wt.TableReference.TableID
	}

	created, err := h.bq.InsertTable(r.Context(), rt.project, rt.dataset, tbl)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, tableToWire(reqHost(r), created))
}

// getTable handles GET /datasets/{d}/tables/{t}.
func (h *Handler) getTable(w http.ResponseWriter, r *http.Request, rt route) {
	tbl, err := h.bq.GetTable(r.Context(), rt.project, rt.dataset, rt.table)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, tableToWire(reqHost(r), tbl))
}

// listTables handles GET /datasets/{d}/tables.
func (h *Handler) listTables(w http.ResponseWriter, r *http.Request, rt route) {
	tables, err := h.bq.ListTables(r.Context(), rt.project, rt.dataset)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	items := make([]tableListEntry, 0, len(tables))
	for _, t := range tables {
		items = append(items, tableListItem(t))
	}

	gcprest.WriteJSON(w, http.StatusOK, tableList{
		Kind:       kindTableList,
		Etag:       "cloudemu",
		Tables:     items,
		TotalItems: len(items),
	})
}

// patchTable handles PATCH (merge) and PUT (replace).
func (h *Handler) patchTable(w http.ResponseWriter, r *http.Request, rt route, replace bool) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	patch, ok := tablePatchFromBody(w, body)
	if !ok {
		return
	}

	var (
		tbl *driver.Table
		err error
	)

	if replace {
		tbl, err = h.bq.UpdateTable(r.Context(), rt.project, rt.dataset, rt.table, patch)
	} else {
		tbl, err = h.bq.PatchTable(r.Context(), rt.project, rt.dataset, rt.table, patch)
	}

	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, tableToWire(reqHost(r), tbl))
}

// deleteTable handles DELETE /datasets/{d}/tables/{t}.
func (h *Handler) deleteTable(w http.ResponseWriter, r *http.Request, rt route) {
	if err := h.bq.DeleteTable(r.Context(), rt.project, rt.dataset, rt.table); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// tableListItem projects a table into a tables.list entry.
func tableListItem(t *driver.Table) tableListEntry {
	e := tableListEntry{
		Kind:           kindTable,
		ID:             tableIDStr(t.ProjectID, t.DatasetID, t.TableID),
		TableReference: &wireTableRef{ProjectID: t.ProjectID, DatasetID: t.DatasetID, TableID: t.TableID},
		FriendlyName:   t.FriendlyName,
		Labels:         t.Labels,
		Type:           t.Type,
		CreationTime:   epochMillis(t.CreationTime),
		ExpirationTime: epochMillis(t.ExpirationTime),
	}

	if t.TimePartitioning != nil {
		e.TimePartitioning = timePartitioningToWire(t.TimePartitioning)
	}

	if len(t.Clustering) > 0 {
		e.Clustering = &wireClustering{Fields: t.Clustering}
	}

	if t.View != nil {
		useLegacy := t.View.UseLegacySQL
		e.View = &wireView{Query: t.View.Query, UseLegacySQL: &useLegacy}
	}

	return e
}

// tableFromWire converts an insert body to a driver table.
func tableFromWire(wt *wireTable) *driver.Table {
	tbl := &driver.Table{
		FriendlyName: wt.FriendlyName,
		Description:  wt.Description,
		Type:         wt.Type,
		Labels:       wt.Labels,
	}

	if wt.TableReference != nil {
		tbl.TableID = wt.TableReference.TableID
	}

	if wt.Schema != nil {
		tbl.Schema = fieldsFromWire(wt.Schema.Fields)
	}

	tbl.TimePartitioning = timePartitioningFromWire(wt.TimePartitioning)

	if wt.Clustering != nil {
		tbl.Clustering = wt.Clustering.Fields
	}

	tbl.View = viewFromWire(wt.View)
	tbl.ExpirationTime = millisToTime(int64(wt.ExpirationTime))

	return tbl
}

// tablePatchFromBody builds a TablePatch from a PATCH/PUT body using the
// present-key set for merge/replace fidelity.
func tablePatchFromBody(w http.ResponseWriter, body []byte) (*driver.TablePatch, bool) {
	var wt wireTable
	if !decodeJSON(w, body, &wt) {
		return nil, false
	}

	keys := presentKeys(body)
	patch := &driver.TablePatch{Etag: wt.Etag, Labels: wt.Labels, LabelsSet: keys["labels"]}

	if keys["friendlyName"] {
		v := wt.FriendlyName
		patch.FriendlyName = &v
	}

	if keys["description"] {
		v := wt.Description
		patch.Description = &v
	}

	if keys["schema"] {
		patch.SchemaSet = true
		if wt.Schema != nil {
			patch.Schema = fieldsFromWire(wt.Schema.Fields)
		}
	}

	tablePatchSubResources(patch, &wt, keys)

	return patch, true
}

// tablePatchSubResources fills the partitioning, clustering, view, and
// expiration fields of a table patch from the supplied keys.
func tablePatchSubResources(patch *driver.TablePatch, wt *wireTable, keys map[string]bool) {
	if keys["timePartitioning"] {
		patch.TimePartitioning = timePartitioningFromWire(wt.TimePartitioning)
	}

	if keys["clustering"] {
		patch.ClusteringSet = true
		if wt.Clustering != nil {
			patch.Clustering = wt.Clustering.Fields
		}
	}

	if keys["view"] {
		patch.View = viewFromWire(wt.View)
	}

	if keys["expirationTime"] {
		t := millisToTime(int64(wt.ExpirationTime))
		patch.ExpirationTime = &t
	}
}

// timePartitioningFromWire converts a wire partitioning spec, or nil.
func timePartitioningFromWire(tp *wireTimePartitioning) *driver.TimePartitioning {
	if tp == nil {
		return nil
	}

	return &driver.TimePartitioning{
		Type:         tp.Type,
		Field:        tp.Field,
		ExpirationMs: int64(tp.ExpirationMs),
	}
}

// viewFromWire converts a wire view definition, or nil.
func viewFromWire(v *wireView) *driver.ViewDefinition {
	if v == nil {
		return nil
	}

	out := &driver.ViewDefinition{Query: v.Query}
	if v.UseLegacySQL != nil {
		out.UseLegacySQL = *v.UseLegacySQL
	}

	return out
}
