package resourcegraph

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// ResourcesHandler serves the generic Microsoft.Resources listing API (the
// `az resource list` surface) at two scopes:
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
	// rgExists, when set, reports whether a resource group exists. It gates the
	// resource-group-scoped listing so a nonexistent group yields the real
	// 404 ResourceGroupNotFound rather than an empty 200. The central RG gate
	// (server/azure/rggate.go) cannot cover this path: it has no /providers/
	// segment, so ParsePath returns Provider == "" and the gate exempts it.
	rgExists func(sub, rg string) bool
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

// SetResourceGroupChecker installs a nil-safe resource-group existence check.
// When set, the resource-group-scoped listing returns 404 ResourceGroupNotFound
// for a group that does not exist, matching real Azure. A nil checker (the
// default) skips the check, so handlers built without it keep working.
func (h *ResourcesHandler) SetResourceGroupChecker(fn func(sub, rg string) bool) {
	h.rgExists = fn
}

// resourcesRoute reports the subscription and resource group scope for a
// generic-resources path, or ok=false when the path is not a generic-resources
// listing. An empty group means the subscription-wide listing.
func resourcesRoute(urlPath string) (sub, group string, ok bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "subscriptions") {
		return "", "", false
	}

	// /subscriptions/{sub}/resources
	if len(parts) == 3 && strings.EqualFold(parts[2], "resources") {
		return parts[1], "", true
	}

	// /subscriptions/{sub}/resourceGroups/{rg}/resources
	if len(parts) == 5 && strings.EqualFold(parts[2], "resourcegroups") &&
		strings.EqualFold(parts[4], "resources") {
		return parts[1], parts[3], true
	}

	return "", "", false
}

// Matches claims a GET of the generic-resources listing at either scope.
func (*ResourcesHandler) Matches(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}

	_, _, ok := resourcesRoute(r.URL.Path)

	return ok
}

func (h *ResourcesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sub, group, ok := resourcesRoute(r.URL.Path)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unknown resources path: "+r.URL.Path)
		return
	}

	// Real Azure rejects an RG-scoped listing against a nonexistent group with
	// 404 ResourceGroupNotFound. The subscription-wide listing (group == "") has
	// no group to check and is never gated.
	if group != "" && h.rgExists != nil && !h.rgExists(sub, group) {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceGroupNotFound",
			fmt.Sprintf("Resource group '%s' could not be found.", group))

		return
	}

	match, err := parseARMFilter(r.URL.Query().Get("$filter"))
	if err != nil {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidFilterInQueryString", err.Error())
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

		row := resourceToWire(&all[i], h.subscriptionID)

		// Subnets are child resources of a virtual network; the generic listing
		// only returns top-level tracked resources.
		if row["type"] == azureTypeSubnet || !match(row) {
			continue
		}

		value = append(value, row)
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": value})
}

// compile-time check that ResourcesHandler satisfies the dispatch contract.
var _ interface {
	Matches(*http.Request) bool
	http.Handler
} = (*ResourcesHandler)(nil)

const azureTypeSubnet = "microsoft.network/subnets"

// reARMFilterAnd splits an ARM $filter into clauses; reARMFilterClause matches
// one `field eq 'value'` clause.
var (
	reARMFilterAnd    = regexp.MustCompile(`(?i)\s+and\s+`)
	reARMFilterClause = regexp.MustCompile(`(?i)^\s*(resourceType|name|location|tagName|tagValue)\s+eq\s+'([^']*)'\s*$`)
)

// parseARMFilter compiles the $filter of a generic-resources listing: clauses
// `resourceType eq`, `name eq`, `location eq`, `tagName eq` and `tagValue eq`
// joined by `and`. Values compare case-insensitively, except tag values. An
// empty filter matches everything.
func parseARMFilter(filter string) (func(map[string]any) bool, error) {
	var preds []func(map[string]any) bool

	tagName, tagValue := "", ""

	for _, clause := range reARMFilterAnd.Split(strings.TrimSpace(filter), -1) {
		if clause == "" {
			continue
		}

		m := reARMFilterClause.FindStringSubmatch(clause)
		if m == nil {
			return nil, fmt.Errorf("invalid $filter clause %q", clause)
		}

		field, want := strings.ToLower(m[1]), m[2]

		switch field {
		case "tagname":
			tagName = want
		case "tagvalue":
			tagValue = want
		default:
			col := map[string]string{"resourcetype": "type", "name": "name", "location": "location"}[field]
			preds = append(preds, func(row map[string]any) bool {
				return strings.EqualFold(valueString(row[col]), want)
			})
		}
	}

	if tagName != "" {
		preds = append(preds, func(row map[string]any) bool {
			v, ok := row["tags"].(map[string]string)[tagName]
			return ok && (tagValue == "" || v == tagValue)
		})
	}

	return func(row map[string]any) bool {
		for _, p := range preds {
			if !p(row) {
				return false
			}
		}

		return true
	}, nil
}
