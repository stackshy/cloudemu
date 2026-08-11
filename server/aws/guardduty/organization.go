package guardduty

import "net/http"

// serveOrgConfig routes /detector/{id}/admin:
// GET=DescribeOrganizationConfiguration, POST=UpdateOrganizationConfiguration.
func (h *Handler) serveOrgConfig(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		body, err := h.gd.DescribeOrganizationConfiguration(r.Context(), id, pageFromQuery(r))
		h.writeResult(w, body, err)
	case http.MethodPost:
		body, err := h.gd.UpdateOrganizationConfiguration(r.Context(), id, rawBody(r))
		h.writeResult(w, body, err)
	default:
		methodNotAllowed(w)
	}
}
