package location

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

func (h *Handler) trackerResource() *resource {
	return &resource{
		coll:     "trackers",
		listPath: "list-trackers",
		create:   h.createTracker,
		list:     h.listTrackers,
		describe: h.describeTracker,
		update:   h.updateTracker,
		del:      h.deleteTracker,
	}
}

// trackerToWire renders the DescribeTracker response.
func trackerToWire(info *driver.TrackerInfo) map[string]any {
	out := map[string]any{
		"TrackerName":                   info.Name,
		"TrackerArn":                    info.Arn,
		"PositionFiltering":             info.PositionFiltering,
		"EventBridgeEnabled":            info.EventBridgeEnabled,
		"KmsKeyEnableGeospatialQueries": info.KmsKeyEnableGeospatialQueries,
		"CreateTime":                    rfc3339(info.CreateTime),
		"UpdateTime":                    rfc3339(info.UpdateTime),
	}

	putNonEmpty(out, "Description", info.Description)
	putNonEmpty(out, "KmsKeyId", info.KmsKeyID)
	putNonEmpty(out, "PricingPlan", info.PricingPlan)
	putNonEmpty(out, "PricingPlanDataSource", info.PricingPlanDataSource)
	putTags(out, info.Tags)

	return out
}

func (h *Handler) createTracker(w http.ResponseWriter, r *http.Request) {
	var in driver.CreateTrackerInput
	if !decodeBody(w, r, &in) {
		return
	}

	info, err := h.loc.CreateTracker(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"TrackerName": info.Name,
		"TrackerArn":  info.Arn,
		"CreateTime":  rfc3339(info.CreateTime),
	})
}

func (h *Handler) describeTracker(w http.ResponseWriter, r *http.Request, name string) {
	info, err := h.loc.DescribeTracker(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, trackerToWire(info))
}

func (h *Handler) updateTracker(w http.ResponseWriter, r *http.Request, name string) {
	var in driver.UpdateTrackerInput
	if !decodeBody(w, r, &in) {
		return
	}

	in.TrackerName = name

	info, err := h.loc.UpdateTracker(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"TrackerName": info.Name,
		"TrackerArn":  info.Arn,
		"UpdateTime":  rfc3339(info.UpdateTime),
	})
}

func (h *Handler) deleteTracker(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.loc.DeleteTracker(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listTrackers(w http.ResponseWriter, r *http.Request) {
	serveList(w, r, h.loc.ListTrackers, trackerEntryToWire)
}

// trackerEntryToWire renders a ListTrackers response entry.
func trackerEntryToWire(info *driver.TrackerInfo) map[string]any {
	e := map[string]any{
		"TrackerName": info.Name,
		"CreateTime":  rfc3339(info.CreateTime),
		"UpdateTime":  rfc3339(info.UpdateTime),
	}

	putNonEmpty(e, "Description", info.Description)
	putNonEmpty(e, "PricingPlan", info.PricingPlan)
	putNonEmpty(e, "PricingPlanDataSource", info.PricingPlanDataSource)

	return e
}
