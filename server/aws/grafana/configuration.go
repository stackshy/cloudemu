package grafana

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/grafana/driver"
)

// configurationRequest is the UpdateWorkspaceConfiguration request body. A nil
// GrafanaVersion keeps the workspace's current version.
type configurationRequest struct {
	Configuration  string  `json:"configuration"`
	GrafanaVersion *string `json:"grafanaVersion"`
}

// serveConfiguration routes the /workspaces/{id}/configuration sub-resource.
func (h *Handler) serveConfiguration(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.describeWorkspaceConfiguration(w, r, id)
	case http.MethodPut:
		h.updateWorkspaceConfiguration(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) updateWorkspaceConfiguration(w http.ResponseWriter, r *http.Request, id string) {
	var req configurationRequest
	if !decodeBody(w, r, &req) {
		return
	}

	err := h.g.UpdateWorkspaceConfiguration(r.Context(), &driver.UpdateConfigurationInput{
		ID:             id,
		Configuration:  req.Configuration,
		GrafanaVersion: req.GrafanaVersion,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{})
}

func (h *Handler) describeWorkspaceConfiguration(w http.ResponseWriter, r *http.Request, id string) {
	configuration, grafanaVersion, err := h.g.DescribeWorkspaceConfiguration(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"configuration":  configuration,
		"grafanaVersion": grafanaVersion,
	})
}
