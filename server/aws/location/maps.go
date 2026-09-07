package location

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

func (h *Handler) mapsResource() *resource {
	return &resource{
		coll:     "maps",
		listPath: "list-maps",
		create:   h.createMap,
		list:     h.listMaps,
		describe: h.describeMap,
		update:   h.updateMap,
		del:      h.deleteMap,
	}
}

// mapConfigToWire renders a map configuration block.
func mapConfigToWire(c driver.MapConfiguration) map[string]any {
	out := map[string]any{"Style": c.Style}
	putNonEmpty(out, "PoliticalView", c.PoliticalView)

	if len(c.CustomLayers) > 0 {
		out["CustomLayers"] = c.CustomLayers
	}

	return out
}

// mapToWire renders a map as its DescribeMap response object.
func mapToWire(info *driver.MapInfo) map[string]any {
	out := map[string]any{
		"MapName":       info.Name,
		"MapArn":        info.Arn,
		"Configuration": mapConfigToWire(info.Configuration),
		"DataSource":    info.DataSource,
		"CreateTime":    rfc3339(info.CreateTime),
		"UpdateTime":    rfc3339(info.UpdateTime),
	}

	putNonEmpty(out, "Description", info.Description)
	putNonEmpty(out, "PricingPlan", info.PricingPlan)
	putTags(out, info.Tags)

	return out
}

func (h *Handler) createMap(w http.ResponseWriter, r *http.Request) {
	var in driver.CreateMapInput
	if !decodeBody(w, r, &in) {
		return
	}

	info, err := h.loc.CreateMap(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"MapName":    info.Name,
		"MapArn":     info.Arn,
		"CreateTime": rfc3339(info.CreateTime),
	})
}

func (h *Handler) describeMap(w http.ResponseWriter, r *http.Request, name string) {
	info, err := h.loc.DescribeMap(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, mapToWire(info))
}

func (h *Handler) updateMap(w http.ResponseWriter, r *http.Request, name string) {
	var in driver.UpdateMapInput
	if !decodeBody(w, r, &in) {
		return
	}

	in.MapName = name

	info, err := h.loc.UpdateMap(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"MapName":    info.Name,
		"MapArn":     info.Arn,
		"UpdateTime": rfc3339(info.UpdateTime),
	})
}

func (h *Handler) deleteMap(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.loc.DeleteMap(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listMaps(w http.ResponseWriter, r *http.Request) {
	serveList(w, r, h.loc.ListMaps, mapEntryToWire)
}

// mapEntryToWire renders a ListMaps response entry.
func mapEntryToWire(info *driver.MapInfo) map[string]any {
	e := map[string]any{
		"MapName":    info.Name,
		"DataSource": info.DataSource,
		"CreateTime": rfc3339(info.CreateTime),
		"UpdateTime": rfc3339(info.UpdateTime),
	}

	putNonEmpty(e, "Description", info.Description)
	putNonEmpty(e, "PricingPlan", info.PricingPlan)

	return e
}
