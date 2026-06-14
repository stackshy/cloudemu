// Package hostmeta serves the Databricks host-metadata discovery endpoint
// (GET /.well-known/databricks-config) as a server.Handler.
//
// The real github.com/databricks/databricks-sdk-go config resolver fetches
// this endpoint on first use to detect the host type (workspace vs. account)
// and back-fill the cloud, workspace id, and OIDC discovery URL. Without it the
// SDK logs a "Failed to resolve host metadata" WARN and falls back to user
// config. Serving a workspace-host stub silences the warning and exercises the
// SDK's real host-metadata resolution path against the in-memory backend.
package hostmeta

import (
	"encoding/json"
	"net/http"
)

// metadataPath is the discovery endpoint the SDK fetches at {host}/...
const metadataPath = "/.well-known/databricks-config"

// stubWorkspaceID is the workspace id reported by the in-memory backend. Real
// workspaces use a numeric id; a fixed value is enough for the SDK's
// workspace-vs-account detection.
const stubWorkspaceID = "0"

// hostTypeWorkspace is the wire value for a workspace host. The SDK normalizes
// it (lower-cased) to config.WorkspaceHost — it does not accept the
// "WORKSPACE_HOST" enum spelling on the wire.
const hostTypeWorkspace = "workspace"

// Handler serves the Databricks host-metadata discovery endpoint.
type Handler struct{}

// New returns a host-metadata handler.
func New() *Handler { return &Handler{} }

// Matches claims GET /.well-known/databricks-config.
func (*Handler) Matches(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == metadataPath
}

// metadata mirrors the fields databricks-sdk-go reads from the discovery
// endpoint (config.HostMetadata).
type metadata struct {
	HostType                            string   `json:"host_type"`
	Cloud                               string   `json:"cloud"`
	WorkspaceID                         string   `json:"workspace_id"`
	OIDCEndpoint                        string   `json:"oidc_endpoint"`
	TokenFederationDefaultOIDCAudiences []string `json:"token_federation_default_oidc_audiences"`
}

// ServeHTTP returns a workspace-host metadata document for the requesting host.
func (*Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	tokenURL := scheme + "://" + r.Host + "/oidc/v1/token"

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(metadata{
		HostType:                            hostTypeWorkspace,
		Cloud:                               "Azure",
		WorkspaceID:                         stubWorkspaceID,
		OIDCEndpoint:                        tokenURL,
		TokenFederationDefaultOIDCAudiences: []string{tokenURL},
	})
}
