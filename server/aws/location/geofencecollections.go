package location

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

func (h *Handler) geofenceCollectionResource() *resource {
	return &resource{
		coll:     "collections",
		listPath: "list-collections",
		create:   h.createGeofenceCollection,
		list:     h.listGeofenceCollections,
		describe: h.describeGeofenceCollection,
		update:   h.updateGeofenceCollection,
		del:      h.deleteGeofenceCollection,
	}
}

// geofenceCollectionToWire renders the DescribeGeofenceCollection response.
func geofenceCollectionToWire(info *driver.GeofenceCollectionInfo) map[string]any {
	out := map[string]any{
		"CollectionName": info.Name,
		"CollectionArn":  info.Arn,
		"CreateTime":     rfc3339(info.CreateTime),
		"UpdateTime":     rfc3339(info.UpdateTime),
		"GeofenceCount":  info.GeofenceCount,
	}

	putNonEmpty(out, "Description", info.Description)
	putNonEmpty(out, "KmsKeyId", info.KmsKeyID)
	putNonEmpty(out, "PricingPlan", info.PricingPlan)
	putNonEmpty(out, "PricingPlanDataSource", info.PricingPlanDataSource)
	putTags(out, info.Tags)

	return out
}

func (h *Handler) createGeofenceCollection(w http.ResponseWriter, r *http.Request) {
	var in driver.CreateGeofenceCollectionInput
	if !decodeBody(w, r, &in) {
		return
	}

	info, err := h.loc.CreateGeofenceCollection(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"CollectionName": info.Name,
		"CollectionArn":  info.Arn,
		"CreateTime":     rfc3339(info.CreateTime),
	})
}

func (h *Handler) describeGeofenceCollection(w http.ResponseWriter, r *http.Request, name string) {
	info, err := h.loc.DescribeGeofenceCollection(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, geofenceCollectionToWire(info))
}

func (h *Handler) updateGeofenceCollection(w http.ResponseWriter, r *http.Request, name string) {
	var in driver.UpdateGeofenceCollectionInput
	if !decodeBody(w, r, &in) {
		return
	}

	in.CollectionName = name

	info, err := h.loc.UpdateGeofenceCollection(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"CollectionName": info.Name,
		"CollectionArn":  info.Arn,
		"UpdateTime":     rfc3339(info.UpdateTime),
	})
}

func (h *Handler) deleteGeofenceCollection(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.loc.DeleteGeofenceCollection(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listGeofenceCollections(w http.ResponseWriter, r *http.Request) {
	serveList(w, r, h.loc.ListGeofenceCollections, geofenceCollectionEntryToWire)
}

// geofenceCollectionEntryToWire renders a ListGeofenceCollections response entry.
func geofenceCollectionEntryToWire(info *driver.GeofenceCollectionInfo) map[string]any {
	e := map[string]any{
		"CollectionName": info.Name,
		"CreateTime":     rfc3339(info.CreateTime),
		"UpdateTime":     rfc3339(info.UpdateTime),
	}

	putNonEmpty(e, "Description", info.Description)
	putNonEmpty(e, "PricingPlan", info.PricingPlan)
	putNonEmpty(e, "PricingPlanDataSource", info.PricingPlanDataSource)

	return e
}
