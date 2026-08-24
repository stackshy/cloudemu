package resourcegraph

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// ResourcesHandler serves the generic Microsoft.Resources listing API — the
// `az resource list` surface — at two scopes:
//
//	GET /subscriptions/{sub}/resources
//	GET /subscriptions/{sub}/resourceGroups/{rg}/resources
//
// Resource Graph (the Handler above) requires a KQL POST; this is the plain
// per-subscription / per-group inventory a CLI reaches for by default. Both are
// backed by the same discovery engine and the same row renderer, so a resource
// shows up identically whichever surface a caller uses.
type ResourcesHandler struct {
	engine         *resourcediscovery.Engine
	subscriptionID string
}

// NewResources returns a generic-resources handler backed by engine.
// subscriptionID scopes rendered resource ids, mirroring the Resource Graph
// handler; an empty value falls back to the engine's own account id.
func NewResources(engine *resourcediscovery.Engine, subscriptionID string) *ResourcesHandler {
	if subscriptionID == "" && engine != nil {
		subscriptionID = engine.AccountID()
	}

	return &ResourcesHandler{engine: engine, subscriptionID: subscriptionID}
}

// resourcesRoute reports the resource group scope for a generic-resources path,
// or ok=false when the path is not a generic-resources listing. An empty group
// means the subscription-wide listing.
func resourcesRoute(urlPath string) (group string, ok bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "subscriptions") {
		return "", false
	}

	// /subscriptions/{sub}/resources
	if len(parts) == 3 && strings.EqualFold(parts[2], "resources") {
		return "", true
	}

	// /subscriptions/{sub}/resourceGroups/{rg}/resources
	if len(parts) == 5 && strings.EqualFold(parts[2], "resourcegroups") &&
		strings.EqualFold(parts[4], "resources") {
		return parts[3], true
	}

	return "", false
}

// Matches claims a GET of the generic-resources listing at either scope.
func (*ResourcesHandler) Matches(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}

	_, ok := resourcesRoute(r.URL.Path)

	return ok
}

func (h *ResourcesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	group, ok := resourcesRoute(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unknown resources path: "+r.URL.Path)
		return
	}

	all, err := h.engine.ListAll(r.Context())
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	value := make([]map[string]any, 0, len(all))

	for i := range all {
		if group != "" && !strings.EqualFold(resourceGroupOrDefault(all[i].ARN), group) {
			continue
		}

		value = append(value, resourceToWire(&all[i], h.subscriptionID))
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": value})
}

// compile-time check that ResourcesHandler satisfies the dispatch contract.
var _ interface {
	Matches(*http.Request) bool
	http.Handler
} = (*ResourcesHandler)(nil)
