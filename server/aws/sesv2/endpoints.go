package sesv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// serveEndpoints routes /multi-region-endpoints and its sub-paths.
func (h *Handler) serveEndpoints(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodPost:
			h.createEndpoint(w, r)
		case http.MethodGet:
			h.listEndpoints(w, r)
		default:
			methodNotAllowed(w)
		}
	case 1:
		switch r.Method {
		case http.MethodGet:
			h.getEndpoint(w, r, rest[0])
		case http.MethodDelete:
			h.deleteEndpoint(w, r, rest[0])
		default:
			methodNotAllowed(w)
		}
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var req createEndpointRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	regions := req.Details.regions()

	ep, err := h.ses.CreateMultiRegionEndpoint(r.Context(), driver.MultiRegionEndpointInput{
		EndpointName: req.EndpointName,
		Regions:      regions,
		Tags:         tagsToMap(req.Tags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, createEndpointResponse{EndpointID: ep.EndpointID, Status: ep.Status})
}

func (h *Handler) getEndpoint(w http.ResponseWriter, r *http.Request, name string) {
	ep, err := h.ses.GetMultiRegionEndpoint(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, endpointJSON{
		EndpointName: ep.EndpointName,
		EndpointID:   ep.EndpointID,
		Status:       ep.Status,
		Routes:       regionsToRoutes(ep.Regions),
	})
}

func (h *Handler) deleteEndpoint(w http.ResponseWriter, r *http.Request, name string) {
	status, err := h.ses.DeleteMultiRegionEndpoint(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, deleteEndpointResponse{Status: status})
}

func (h *Handler) listEndpoints(w http.ResponseWriter, r *http.Request) {
	eps, err := h.ses.ListMultiRegionEndpoints(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]endpointJSON, 0, len(eps))
	for i := range eps {
		out = append(out, endpointJSON{
			EndpointName: eps[i].EndpointName,
			EndpointID:   eps[i].EndpointID,
			Status:       eps[i].Status,
			Routes:       regionsToRoutes(eps[i].Regions),
		})
	}

	writeJSON(w, listEndpointsResponse{MultiRegionEndpoints: out})
}
