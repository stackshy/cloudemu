package grafana

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/grafana/driver"
)

// authenticationRequest is the UpdateWorkspaceAuthentication request body. A
// present samlConfiguration marks the workspace's SAML as configured.
type authenticationRequest struct {
	AuthenticationProviders []string        `json:"authenticationProviders"`
	SamlConfiguration       json.RawMessage `json:"samlConfiguration"`
}

// serveAuthentication routes the /workspaces/{id}/authentication sub-resource.
func (h *Handler) serveAuthentication(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.describeWorkspaceAuthentication(w, r, id)
	case http.MethodPost:
		h.updateWorkspaceAuthentication(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) updateWorkspaceAuthentication(w http.ResponseWriter, r *http.Request, id string) {
	var req authenticationRequest
	if !decodeBody(w, r, &req) {
		return
	}

	out, err := h.g.UpdateWorkspaceAuthentication(r.Context(), &driver.UpdateAuthenticationInput{
		ID:                      id,
		AuthenticationProviders: req.AuthenticationProviders,
		SamlConfigured:          len(req.SamlConfiguration) > 0,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"authentication": authenticationDescription(out)})
}

func (h *Handler) describeWorkspaceAuthentication(w http.ResponseWriter, r *http.Request, id string) {
	out, err := h.g.DescribeWorkspaceAuthentication(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"authentication": authenticationDescription(out)})
}

// ssoClientID is the placeholder IAM Identity Center client id reported for a
// workspace that uses AWS_SSO. It is a stable computed value.
const ssoClientID = "cloudemu-sso-client"

// authenticationDescription renders a workspace's AuthenticationDescription
// object for the authentication operations.
func authenticationDescription(w *driver.Workspace) map[string]any {
	out := authenticationSummary(w)

	for _, p := range w.AuthenticationProviders {
		if p == driver.AuthAWSSSO {
			out["awsSso"] = map[string]any{"ssoClientId": ssoClientID}

			break
		}
	}

	return out
}
