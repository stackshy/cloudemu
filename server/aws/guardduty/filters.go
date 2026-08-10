package guardduty

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// serveFilter routes /detector/{id}/filter and /detector/{id}/filter/{name}.
func (h *Handler) serveFilter(w http.ResponseWriter, r *http.Request, detectorID string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			h.createFilter(w, r, detectorID)
		case http.MethodGet:
			h.listFilters(w, r, detectorID)
		default:
			methodNotAllowed(w)
		}

		return
	}

	filterName := rest[0]

	switch r.Method {
	case http.MethodGet:
		h.getFilter(w, r, detectorID, filterName)
	case http.MethodPost:
		h.updateFilter(w, r, detectorID, filterName)
	case http.MethodDelete:
		h.deleteFilter(w, r, detectorID, filterName)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) createFilter(w http.ResponseWriter, r *http.Request, detectorID string) {
	var req createFilterRequest
	if !decodeInto(w, r, &req) {
		return
	}

	name, err := h.gd.CreateFilter(r.Context(), driver.CreateFilterInput{
		DetectorID:      detectorID,
		Name:            req.Name,
		Action:          req.Action,
		Description:     req.Description,
		Rank:            req.Rank,
		FindingCriteria: req.FindingCriteria,
		Tags:            req.Tags,
		ClientToken:     req.ClientToken,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"name": name})
}

func (h *Handler) getFilter(w http.ResponseWriter, r *http.Request, detectorID, filterName string) {
	f, err := h.gd.GetFilter(r.Context(), detectorID, filterName)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, filterToWire(f))
}

func (h *Handler) updateFilter(w http.ResponseWriter, r *http.Request, detectorID, filterName string) {
	var req updateFilterRequest
	if !decodeInto(w, r, &req) {
		return
	}

	name, err := h.gd.UpdateFilter(r.Context(), driver.UpdateFilterInput{
		DetectorID:      detectorID,
		FilterName:      filterName,
		Action:          req.Action,
		Description:     req.Description,
		Rank:            req.Rank,
		FindingCriteria: req.FindingCriteria,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{"name": name})
}

func (h *Handler) deleteFilter(w http.ResponseWriter, r *http.Request, detectorID, filterName string) {
	if err := h.gd.DeleteFilter(r.Context(), detectorID, filterName); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listFilters(w http.ResponseWriter, r *http.Request, detectorID string) {
	names, next, err := h.gd.ListFilters(r.Context(), detectorID, pageFromQuery(r))
	if err != nil {
		writeErr(w, err)

		return
	}

	if names == nil {
		names = []string{}
	}

	writeJSON(w, withNext(map[string]any{"filterNames": names}, next))
}
