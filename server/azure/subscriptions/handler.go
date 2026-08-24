// Package subscriptions implements the Azure Resource Manager subscriptions
// endpoints: the collection list, a single subscription Get, and the per-
// subscription locations list.
//
// A caller connecting an Azure account verifies the credential by listing the
// subscriptions it can reach (or getting the target subscription directly) and
// checking the target is among them. CLIs and IaC tools also call the locations
// endpoint at startup to validate a region. Without these the first step of any
// Azure workflow fails before any resource work begins.
//
// The emulator serves a single estate under one subscription id and is
// subscription-transparent: Get echoes back whichever subscription id the
// caller scoped to (the same value the management plane echoes into resource
// ids), and List reports the one reachable subscription. This keeps
// "connect account X" then "create under X" consistent for every service.
package subscriptions

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	basePath = "/subscriptions"

	// DefaultSubscriptionID is reported when the server is configured with no
	// subscription id. It matches the cmd/cloudemu default so a caller sees a
	// stable, well-formed GUID.
	DefaultSubscriptionID = "00000000-0000-0000-0000-000000000000"

	stateEnabled        = "Enabled"
	authorizationSource = "RoleBased"

	kindList      = "list"
	kindGet       = "get"
	kindLocations = "locations"

	partsGet       = 2
	partsLocations = 3
)

// Handler serves the subscriptions collection, a single subscription, and the
// per-subscription locations list.
type Handler struct {
	subscriptionID string
	tenantID       string
}

// New returns a subscriptions handler. An empty subscriptionID falls back to
// DefaultSubscriptionID; tenantID is echoed as the subscription's tenant.
func New(subscriptionID, tenantID string) *Handler {
	if subscriptionID == "" {
		subscriptionID = DefaultSubscriptionID
	}

	return &Handler{subscriptionID: subscriptionID, tenantID: tenantID}
}

// route classifies a request path into one of the three shapes this handler
// serves, or returns ok=false for anything deeper (which belongs to the
// resource-group / resource handlers).
func route(urlPath string) (kind, id string, ok bool) {
	if strings.TrimSuffix(urlPath, "/") == basePath {
		return kindList, "", true
	}

	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < partsGet || parts[0] != "subscriptions" {
		return "", "", false
	}

	switch len(parts) {
	case partsGet:
		return kindGet, parts[1], true
	case partsLocations:
		if strings.EqualFold(parts[2], kindLocations) {
			return kindLocations, parts[1], true
		}
	}

	return "", "", false
}

// Matches claims a GET of the subscriptions collection, a single subscription,
// or a subscription's locations list. Deeper paths
// (/subscriptions/{id}/resourceGroups/...) belong to the resource handlers.
func (*Handler) Matches(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}

	_, _, ok := route(r.URL.Path)

	return ok
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	kind, id, ok := route(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unknown subscriptions path: "+r.URL.Path)
		return
	}

	switch kind {
	case kindList:
		azurearm.WriteJSON(w, http.StatusOK, map[string]any{
			"value": []map[string]any{h.subscription(h.subscriptionID)},
		})
	case kindGet:
		azurearm.WriteJSON(w, http.StatusOK, h.subscription(id))
	case kindLocations:
		azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": locations(id)})
	}
}

// subscription renders the Subscription object real ARM returns for Get/List.
// The id is echoed so the emulator stays subscription-transparent.
func (h *Handler) subscription(id string) map[string]any {
	return map[string]any{
		"id":                  basePath + "/" + id,
		"subscriptionId":      id,
		"displayName":         "CloudEmu Subscription",
		"state":               stateEnabled,
		"tenantId":            h.tenantID,
		"authorizationSource": authorizationSource,
		"subscriptionPolicies": map[string]any{
			"locationPlacementId": "Public_2014-09-01",
			"quotaId":             "Public_2014-09-01",
			"spendingLimit":       "Off",
		},
	}
}
