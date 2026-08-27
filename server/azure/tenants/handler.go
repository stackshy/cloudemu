// Package tenants implements the Azure Resource Manager tenants list endpoint
// (GET /tenants).
//
// The tenants list is a global (non-subscription-scoped) management endpoint a
// caller hits when connecting an account — it is one of the first calls the
// Azure CLI and SDKs make to discover the directory a credential belongs to.
// Because the path does not start with /subscriptions/, it would otherwise fall
// through to the permissive blob-storage data-plane fallback and come back as a
// blob XML error; this handler claims it and answers with the ARM JSON shape.
package tenants

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const basePath = "/tenants"

// Handler serves the global tenants list.
type Handler struct {
	tenantID string
}

// New returns a tenants handler that reports the single directory the emulator
// serves, identified by tenantID.
func New(tenantID string) *Handler {
	return &Handler{tenantID: tenantID}
}

// Matches claims a GET of the /tenants collection. The path carries no deeper
// segments in the real API, so only the bare collection is claimed.
func (*Handler) Matches(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.TrimSuffix(r.URL.Path, "/") == basePath
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	azurearm.WriteJSON(w, http.StatusOK, map[string]any{
		"value": []map[string]any{{
			"id":             basePath + "/" + h.tenantID,
			"tenantId":       h.tenantID,
			"tenantCategory": "Home",
			"displayName":    "CloudEmu Directory",
			"countryCode":    "US",
			"defaultDomain":  "cloudemu.onmicrosoft.com",
			"domains":        []string{"cloudemu.onmicrosoft.com"},
			"tenantType":     "AAD",
		}},
	})
}
