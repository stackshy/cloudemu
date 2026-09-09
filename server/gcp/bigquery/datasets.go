package bigquery

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	"github.com/stackshy/cloudemu/v2/services/bigquery/driver"
)

// insertDataset handles POST /datasets.
func (h *Handler) insertDataset(w http.ResponseWriter, r *http.Request, rt route) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	var wd wireDataset
	if !decodeJSON(w, body, &wd) {
		return
	}

	ds := datasetFromWire(&wd)
	if wd.DatasetReference != nil && wd.DatasetReference.DatasetID != "" {
		ds.DatasetID = wd.DatasetReference.DatasetID
	}

	created, err := h.bq.InsertDataset(r.Context(), rt.project, ds)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, datasetToWire(reqHost(r), created))
}

// getDataset handles GET /datasets/{d}.
func (h *Handler) getDataset(w http.ResponseWriter, r *http.Request, rt route) {
	ds, err := h.bq.GetDataset(r.Context(), rt.project, rt.dataset)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, datasetToWire(reqHost(r), ds))
}

// listDatasets handles GET /datasets.
func (h *Handler) listDatasets(w http.ResponseWriter, r *http.Request, rt route) {
	datasets, err := h.bq.ListDatasets(r.Context(), rt.project)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	items := make([]datasetListEntry, 0, len(datasets))

	for _, ds := range datasets {
		items = append(items, datasetListEntry{
			Kind:             kindDataset,
			ID:               datasetIDStr(ds.ProjectID, ds.DatasetID),
			DatasetReference: &wireDatasetRef{ProjectID: ds.ProjectID, DatasetID: ds.DatasetID},
			FriendlyName:     ds.FriendlyName,
			Labels:           ds.Labels,
			Location:         ds.Location,
		})
	}

	gcprest.WriteJSON(w, http.StatusOK, datasetList{
		Kind:     kindDatasetList,
		Etag:     "cloudemu",
		Datasets: items,
	})
}

// patchDataset handles PATCH (replace=false, merge) and PUT (replace=true).
func (h *Handler) patchDataset(w http.ResponseWriter, r *http.Request, rt route, replace bool) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}

	patch, ok := datasetPatchFromBody(w, body)
	if !ok {
		return
	}

	var (
		ds  *driver.Dataset
		err error
	)

	if replace {
		ds, err = h.bq.UpdateDataset(r.Context(), rt.project, rt.dataset, patch)
	} else {
		ds, err = h.bq.PatchDataset(r.Context(), rt.project, rt.dataset, patch)
	}

	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, datasetToWire(reqHost(r), ds))
}

// deleteDataset handles DELETE /datasets/{d}. deleteContents=true removes a
// non-empty dataset with its tables.
func (h *Handler) deleteDataset(w http.ResponseWriter, r *http.Request, rt route) {
	deleteContents := r.URL.Query().Get("deleteContents") == "true"

	if err := h.bq.DeleteDataset(r.Context(), rt.project, rt.dataset, deleteContents); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// datasetFromWire converts an insert body to a driver dataset.
func datasetFromWire(wd *wireDataset) *driver.Dataset {
	ds := &driver.Dataset{
		FriendlyName: wd.FriendlyName,
		Description:  wd.Description,
		Location:     wd.Location,
		Labels:       wd.Labels,
		Access:       accessFromWire(wd.Access),
	}

	if wd.DatasetReference != nil {
		ds.DatasetID = wd.DatasetReference.DatasetID
	}

	ds.DefaultTableExpirationMs = int64(wd.DefaultTableExpirationMs)

	return ds
}
