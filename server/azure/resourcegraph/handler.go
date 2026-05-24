package resourcegraph

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/resourcediscovery"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

// Path prefixes this handler serves. The Resource Graph provider sits at a
// well-known ARM URL; the API-version query string is varied across SDK
// releases so we ignore it for matching.
const (
	pathResources       = "/providers/Microsoft.ResourceGraph/resources"
	pathResourcesHistry = "/providers/Microsoft.ResourceGraph/resourcesHistory"
	pathOperations      = "/providers/Microsoft.ResourceGraph/operations"
)

// Handler serves Azure Resource Graph ARM-JSON requests.
type Handler struct {
	engine         *resourcediscovery.Engine
	subscriptionID string
}

// New returns a Resource Graph handler backed by engine. subscriptionID is
// returned in resource IDs and validated against the request's subscriptions
// list (a request whose subscriptions field is set but does not include this
// ID returns an empty result rather than an error).
func New(engine *resourcediscovery.Engine, subscriptionID string) *Handler {
	return &Handler{engine: engine, subscriptionID: subscriptionID}
}

// Matches accepts ARM requests targeting Microsoft.ResourceGraph. The
// resourcesHistory and operations paths are also routed here.
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path

	return strings.HasPrefix(p, pathResources) ||
		strings.HasPrefix(p, pathResourcesHistry) ||
		strings.HasPrefix(p, pathOperations)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, pathOperations):
		h.listOperations(w, r)
	case strings.HasPrefix(r.URL.Path, pathResourcesHistry):
		h.queryResourcesHistory(w, r)
	case strings.HasPrefix(r.URL.Path, pathResources):
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

// resourceToWire formats one Resource into the Azure Resource Graph row
// shape: { id, name, type, location, resourceGroup, subscriptionId, tags }.
// The portable Type is translated back to the canonical Azure type string.
func resourceToWire(r *resourcediscovery.Resource) map[string]any {
	out := map[string]any{
		"id":             r.ARN,
		"name":           r.ID,
		"type":           portableToAzureType(r.Service, r.Type),
		"location":       r.Region,
		"resourceGroup":  "default",
		"subscriptionId": extractSubscription(r.ARN),
		"tags":           tagsOrEmpty(r.Tags),
	}

	return out
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

// portableToAzureType is the inverse of mapAzureType — it turns the engine's
// (service, type) pair back into the dotted Azure type string a real ARG
// response carries.
func portableToAzureType(service, typ string) string {
	switch service + "/" + typ {
	case "compute/Instance":
		return "microsoft.compute/virtualmachines"
	case "networking/VPC":
		return "microsoft.network/virtualnetworks"
	case "networking/Subnet":
		return "microsoft.network/subnets"
	case "networking/SecurityGroup":
		return "microsoft.network/networksecuritygroups"
	case "storage/Bucket":
		return "microsoft.storage/storageaccounts"
	case "database/Table":
		return "microsoft.documentdb/databaseaccounts"
	case "serverless/Function":
		return "microsoft.web/sites"
	default:
		return strings.ToLower(service + "/" + typ)
	}
}

// Compile-time check that Handler implements the Matches+ServeHTTP pair the
// dispatch chain expects.
var _ interface {
	Matches(*http.Request) bool
	http.Handler
} = (*Handler)(nil)
