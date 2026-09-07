package location

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

func (h *Handler) placeIndexResource() *resource {
	return &resource{
		coll:     "indexes",
		listPath: "list-indexes",
		create:   h.createPlaceIndex,
		list:     h.listPlaceIndexes,
		describe: h.describePlaceIndex,
		update:   h.updatePlaceIndex,
		del:      h.deletePlaceIndex,
	}
}

// placeIndexToWire renders a place index as its DescribePlaceIndex response.
func placeIndexToWire(info *driver.PlaceIndexInfo) map[string]any {
	out := map[string]any{
		"IndexName":               info.Name,
		"IndexArn":                info.Arn,
		"DataSource":              info.DataSource,
		"DataSourceConfiguration": map[string]any{"IntendedUse": info.DataSourceConfiguration.IntendedUse},
		"CreateTime":              rfc3339(info.CreateTime),
		"UpdateTime":              rfc3339(info.UpdateTime),
	}

	putNonEmpty(out, "Description", info.Description)
	putNonEmpty(out, "PricingPlan", info.PricingPlan)
	putTags(out, info.Tags)

	return out
}

func (h *Handler) createPlaceIndex(w http.ResponseWriter, r *http.Request) {
	var in driver.CreatePlaceIndexInput
	if !decodeBody(w, r, &in) {
		return
	}

	info, err := h.loc.CreatePlaceIndex(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"IndexName":  info.Name,
		"IndexArn":   info.Arn,
		"CreateTime": rfc3339(info.CreateTime),
	})
}

func (h *Handler) describePlaceIndex(w http.ResponseWriter, r *http.Request, name string) {
	info, err := h.loc.DescribePlaceIndex(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, placeIndexToWire(info))
}

func (h *Handler) updatePlaceIndex(w http.ResponseWriter, r *http.Request, name string) {
	var in driver.UpdatePlaceIndexInput
	if !decodeBody(w, r, &in) {
		return
	}

	in.IndexName = name

	info, err := h.loc.UpdatePlaceIndex(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"IndexName":  info.Name,
		"IndexArn":   info.Arn,
		"UpdateTime": rfc3339(info.UpdateTime),
	})
}

func (h *Handler) deletePlaceIndex(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.loc.DeletePlaceIndex(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listPlaceIndexes(w http.ResponseWriter, r *http.Request) {
	serveList(w, r, h.loc.ListPlaceIndexes, placeIndexEntryToWire)
}

// placeIndexEntryToWire renders a ListPlaceIndexes response entry.
func placeIndexEntryToWire(info *driver.PlaceIndexInfo) map[string]any {
	e := map[string]any{
		"IndexName":  info.Name,
		"DataSource": info.DataSource,
		"CreateTime": rfc3339(info.CreateTime),
		"UpdateTime": rfc3339(info.UpdateTime),
	}

	putNonEmpty(e, "Description", info.Description)
	putNonEmpty(e, "PricingPlan", info.PricingPlan)

	return e
}
