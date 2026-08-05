package resourcegraph

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// Path prefixes this handler serves. The Resource Graph provider sits at a
// well-known ARM URL; the API-version query string is varied across SDK
// releases so we ignore it for matching.
const (
	pathResources        = "/providers/Microsoft.ResourceGraph/resources"
	pathResourcesHistory = "/providers/Microsoft.ResourceGraph/resourcesHistory"
	pathOperations       = "/providers/Microsoft.ResourceGraph/operations"
)

// Handler serves Azure Resource Graph ARM-JSON requests.
type Handler struct {
	engine         *resourcediscovery.Engine
	subscriptionID string
}

// New returns a Resource Graph handler backed by engine. subscriptionID is
// validated against the request's subscriptions list (a request whose
// subscriptions field is set but does not include this ID returns an empty
// result rather than an error). If subscriptionID is empty, the engine's
// own AccountID is used — that's the same value the engine was built with
// when it constructed Azure-shaped resource IDs, so the two stay aligned
// without callers having to pass the ID twice.
func New(engine *resourcediscovery.Engine, subscriptionID string) *Handler {
	if subscriptionID == "" && engine != nil {
		subscriptionID = engine.AccountID()
	}

	return &Handler{engine: engine, subscriptionID: subscriptionID}
}

// Matches accepts ARM requests targeting Microsoft.ResourceGraph. Uses
// exact path equality, not prefix match, so /resources cannot shadow
// /resourcesHistory (the longer path starts with the shorter one).
// r.URL.Path strips the query string, so api-version is not in the way.
func (*Handler) Matches(r *http.Request) bool {
	switch r.URL.Path {
	case pathResources, pathResourcesHistory, pathOperations:
		return true
	default:
		return false
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case pathOperations:
		h.listOperations(w, r)
	case pathResourcesHistory:
		h.queryResourcesHistory(w, r)
	case pathResources:
		h.queryResources(w, r)
	default:
		azurearm.WriteError(w, http.StatusNotFound, "NotFound", "unknown Resource Graph path: "+r.URL.Path)
	}
}

type queryRequest struct {
	Subscriptions []string `json:"subscriptions"`
	Query         string   `json:"query"`
	Options       struct {
		Top  int `json:"$top"`
		Skip int `json:"$skip"`
	} `json:"options"`
}

func (h *Handler) queryResources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "POST required")
		return
	}

	var req queryRequest
	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	if !h.subscriptionAllowed(req.Subscriptions) {
		azurearm.WriteJSON(w, http.StatusOK, emptyResponse())
		return
	}

	parsed := parseKQL(req.Query)

	// Contradiction in chained where-clauses (e.g. two type filters AND-ed
	// together) — short-circuit before hitting the engine. See parsedKQL.
	if parsed.ForceEmpty {
		azurearm.WriteJSON(w, http.StatusOK, emptyResponse())
		return
	}

	results, err := h.engine.List(r.Context(), parsed.Query)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	results = applyLimit(results, parsed.Limit, req.Options.Top, req.Options.Skip)

	data := make([]map[string]any, 0, len(results))
	for i := range results {
		data = append(data, resourceToWire(&results[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{
		"totalRecords":    len(data),
		"count":           len(data),
		"data":            data,
		"facets":          []any{},
		"resultTruncated": "false",
	})
}

// queryResourcesHistory returns the current inventory as a single point-in-time
// snapshot. The mock has no time-travel state; real Resource Graph History
// requires Azure Diagnostic Settings to be configured, which is out of scope.
func (h *Handler) queryResourcesHistory(w http.ResponseWriter, r *http.Request) {
	h.queryResources(w, r)
}

// listOperations returns the descriptors for the three ops this handler
// supports. Real Resource Graph returns many more (private link, etc.); we
// surface only the ones a discovery-focused caller exercises.
func (*Handler) listOperations(w http.ResponseWriter, _ *http.Request) {
	azurearm.WriteJSON(w, http.StatusOK, map[string]any{
		"value": []map[string]any{
			operationDescriptor("Microsoft.ResourceGraph/resources/read",
				"resources", "Query", "Submits a Resource Graph query."),
			operationDescriptor("Microsoft.ResourceGraph/resourcesHistory/read",
				"resourcesHistory", "Query history", "Submits a Resource Graph history query."),
			operationDescriptor("Microsoft.ResourceGraph/operations/read",
				"operations", "List", "Lists supported Resource Graph operations."),
		},
	})
}

func operationDescriptor(name, resource, op, desc string) map[string]any {
	return map[string]any{
		"name": name,
		"display": map[string]string{
			"provider":    "Microsoft.ResourceGraph",
			"resource":    resource,
			"operation":   op,
			"description": desc,
		},
	}
}

// subscriptionAllowed returns true if the request's subscription list is
// empty (caller doesn't care about scoping) or includes this handler's
// subscription ID. Mismatch returns an empty result rather than an error,
// matching real Resource Graph behavior when the caller scopes to
// subscriptions they can't see.
func (h *Handler) subscriptionAllowed(reqSubs []string) bool {
	if len(reqSubs) == 0 {
		return true
	}

	for _, s := range reqSubs {
		if s == h.subscriptionID {
			return true
		}
	}

	return false
}

func emptyResponse() map[string]any {
	return map[string]any{
		"totalRecords":    0,
		"count":           0,
		"data":            []any{},
		"facets":          []any{},
		"resultTruncated": "false",
	}
}

// applyLimit applies the caller-specified $top/$skip and any `| limit N` /
// `| take N` from the KQL. The smaller of the two limits wins; $skip is
// applied before slicing.
func applyLimit(results []resourcediscovery.Resource, kqlLimit, top, skip int) []resourcediscovery.Resource {
	if skip > 0 {
		if skip >= len(results) {
			return nil
		}

		results = results[skip:]
	}

	limit := top
	if kqlLimit > 0 && (limit == 0 || kqlLimit < limit) {
		limit = kqlLimit
	}

	if limit > 0 && limit < len(results) {
		results = results[:limit]
	}

	return results
}

// resourceToWire formats one Resource into the Azure Resource Graph row shape.
// The fixed columns (id, name, type, location, resourceGroup, subscriptionId,
// tags) are always present; the resource-shape columns (sku, properties,
// managedBy, kind, zones) are emitted from the Resource's generic attribute
// slots only when set — the same rendering for every resource type, with no
// per-type branching. id is the ARM resource ID and resourceGroup is derived
// from it (real Resource Graph consumers parse both).
func resourceToWire(r *resourcediscovery.Resource) map[string]any {
	out := map[string]any{
		"id":             r.ARN,
		"name":           r.ID,
		"type":           portableToAzureType(r.Service, r.Type),
		"location":       r.Region,
		"resourceGroup":  resourceGroupOrDefault(r.ARN),
		"subscriptionId": extractSubscription(r.ARN),
		"tags":           tagsOrEmpty(r.Tags),
	}

	if r.SKU != "" || r.SKUTier != "" || r.SKUCapacity > 0 {
		sku := map[string]any{}
		if r.SKU != "" {
			sku["name"] = r.SKU
		}

		if r.SKUTier != "" {
			sku["tier"] = r.SKUTier
		}

		if r.SKUCapacity > 0 {
			sku["capacity"] = r.SKUCapacity
		}

		out["sku"] = sku
	}

	if r.ManagedBy != "" {
		out["managedBy"] = r.ManagedBy
	}

	if r.Kind != "" {
		out["kind"] = r.Kind
	}

	if len(r.Zones) > 0 {
		out["zones"] = r.Zones
	}

	if len(r.Properties) > 0 {
		out["properties"] = r.Properties
	}

	return out
}

// resourceGroupOrDefault pulls the resource group out of an Azure resource ID
// (/subscriptions/<id>/resourceGroups/<rg>/...), case-insensitively. Falls back
// to "default" for IDs that don't carry one.
func resourceGroupOrDefault(id string) string {
	const key = "/resourcegroups/"

	i := strings.Index(strings.ToLower(id), key)
	if i < 0 {
		return "default"
	}

	rest := id[i+len(key):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		return rest[:j]
	}

	return rest
}

func tagsOrEmpty(tags map[string]string) map[string]string {
	if tags == nil {
		return map[string]string{}
	}

	return tags
}

// extractSubscription pulls /subscriptions/<id>/... out of an Azure resource
// ID. Returns empty string for non-conforming IDs.
func extractSubscription(arn string) string {
	const prefix = "/subscriptions/"
	if !strings.HasPrefix(arn, prefix) {
		return ""
	}

	rest := arn[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}

	return rest
}

// portableToAzureTypeMap is the inverse of mapAzureType's mapping — the engine's
// (service, type) pair back to the dotted Azure type string a real ARG response
// carries. A map lookup rather than a switch keeps gocyclo under the gate as the
// pairs grow.
var portableToAzureTypeMap = map[string]string{ //nolint:gochecknoglobals // static lookup table
	"compute/Instance":                    "microsoft.compute/virtualmachines",
	"compute/Volume":                      "microsoft.compute/disks",
	"compute/ScaleSet":                    "microsoft.compute/virtualmachinescalesets",
	"networking/VPC":                      "microsoft.network/virtualnetworks",
	"networking/Subnet":                   "microsoft.network/subnets",
	"networking/SecurityGroup":            "microsoft.network/networksecuritygroups",
	"networking/NetworkInterface":         "microsoft.network/networkinterfaces",
	"networking/ElasticIP":                "microsoft.network/publicipaddresses",
	"storage/Bucket":                      "microsoft.storage/storageaccounts",
	"database/Table":                      "microsoft.documentdb/databaseaccounts",
	"serverless/Function":                 "microsoft.web/sites",
	"serverless/AppServicePlan":           "microsoft.web/serverfarms",
	"databricks/Workspace":                "microsoft.databricks/workspaces",
	"kubernetes/Cluster":                  "microsoft.containerservice/managedclusters",
	"kubernetes/NodeGroup":                "microsoft.containerservice/managedclusters/agentpools",
	"relationaldb/SqlServer":              "microsoft.sql/servers",
	"relationaldb/SqlManagedInstance":     "microsoft.sql/managedinstances",
	"relationaldb/SqlDatabase":            "microsoft.sql/servers/databases",
	"relationaldb/MySqlFlexibleServer":    "microsoft.dbformysql/flexibleservers",
	"relationaldb/PostgresFlexibleServer": "microsoft.dbforpostgresql/flexibleservers",
}

func portableToAzureType(service, typ string) string {
	key := service + "/" + typ
	if azureType, ok := portableToAzureTypeMap[key]; ok {
		return azureType
	}

	return strings.ToLower(key)
}

// Compile-time check that Handler implements the Matches+ServeHTTP pair the
// dispatch chain expects.
var _ interface {
	Matches(*http.Request) bool
	http.Handler
} = (*Handler)(nil)
