package resourcegraph

import (
	"encoding/base64"
	"net/http"
	"strconv"
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

// internalTagPrefix is the marker the Azure wire handlers use for internal ARM
// bookkeeping tags (resource name, resource group, disk name, …). These are an
// implementation detail of how the cross-cloud driver models are mapped to ARM
// and must never appear on a resource row a client reads.
const internalTagPrefix = "cloudemu:"

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
		Top       int    `json:"$top"`
		Skip      int    `json:"$skip"`
		SkipToken string `json:"$skipToken"`
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

	// The emulator serves a single estate, and the management plane accepts any
	// subscription a client uses (it echoes it straight back into the resource
	// id). So Resource Graph is subscription-transparent: it never rejects a
	// requested subscription — it reports the estate under whichever one the
	// caller scoped to, so "create under sub X" then "discover under sub X"
	// stays consistent for every service.
	subscription := h.effectiveSubscription(req.Subscriptions)

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

	// The KQL `| limit N` caps the matching set; $top/$skip/$skipToken page over
	// it. totalRecords is the size of that matching set (not the page), so a
	// paged client can see how many rows exist beyond the current page.
	matched := applyKQLLimit(results, parsed.Limit)
	skip := effectiveSkip(req.Options.Skip, req.Options.SkipToken)
	page := pageResults(matched, req.Options.Top, skip)

	data := make([]map[string]any, 0, len(page))
	for i := range page {
		data = append(data, resourceToWire(&page[i], subscription))
	}

	resp := map[string]any{
		"totalRecords":    len(matched),
		"count":           len(data),
		"data":            data,
		"facets":          []any{},
		"resultTruncated": "false",
	}

	if next := skip + len(page); next < len(matched) {
		resp["$skipToken"] = encodeSkipToken(next)
	}

	azurearm.WriteJSON(w, http.StatusOK, resp)
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

// effectiveSubscription reports the subscription id the estate is rendered
// under for this query. The emulator is single-tenant, so it does not partition
// resources by subscription: when the caller scopes to exactly one subscription
// the estate is reported under that one (so the ids a client sees match the
// subscription it created resources under, and its own scoped queries match);
// otherwise it falls back to the handler's configured subscription.
func (h *Handler) effectiveSubscription(reqSubs []string) string {
	if len(reqSubs) == 1 && reqSubs[0] != "" {
		return reqSubs[0]
	}

	return h.subscriptionID
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

// applyKQLLimit caps the result set to the `| limit N` / `| take N` from the KQL
// query. This is the query's own row cap, distinct from the $top paging control,
// so it defines the total record count a client pages over.
func applyKQLLimit(results []resourcediscovery.Resource, kqlLimit int) []resourcediscovery.Resource {
	if kqlLimit > 0 && kqlLimit < len(results) {
		return results[:kqlLimit]
	}

	return results
}

// pageResults returns the $top/$skip page of the matching set. skip past the end
// yields an empty page; a top of 0 means no page cap.
func pageResults(matched []resourcediscovery.Resource, top, skip int) []resourcediscovery.Resource {
	if skip >= len(matched) {
		return nil
	}

	page := matched[skip:]

	if top > 0 && top < len(page) {
		page = page[:top]
	}

	return page
}

// effectiveSkip resolves the paging offset: a $skipToken (an opaque continuation
// cursor this handler issued) wins over a raw $skip.
func effectiveSkip(skip int, token string) int {
	if token != "" {
		if n, ok := decodeSkipToken(token); ok {
			return n
		}
	}

	if skip > 0 {
		return skip
	}

	return 0
}

// encodeSkipToken / decodeSkipToken wrap the next-page offset in an opaque
// base64 cursor, matching how real Resource Graph returns $skipToken.
func encodeSkipToken(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeSkipToken(token string) (int, bool) {
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0, false
	}

	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0, false
	}

	return n, true
}

// resourceToWire formats one Resource into the Azure Resource Graph row shape.
// The fixed columns (id, name, type, location, resourceGroup, subscriptionId,
// tags) are always present; the resource-shape columns (sku, properties,
// managedBy, kind, zones) are emitted from the Resource's generic attribute
// slots only when set — the same rendering for every resource type, with no
// per-type branching. id is the ARM resource ID and resourceGroup is derived
// from it (real Resource Graph consumers parse both).
func resourceToWire(r *resourcediscovery.Resource, subscription string) map[string]any {
	// The emulator is single-tenant; each mock's ARN embeds an inconsistent
	// subscription placeholder (empty, a region, a zero id). Render every
	// resource under the subscription this query is scoped to, so both the id
	// and subscriptionId match what the caller (and the management plane, which
	// echoes the client's subscription on create) uses. Fall back to the ARN's
	// own subscription only when the query carries none.
	if subscription == "" {
		subscription = extractSubscription(r.ARN)
	}

	out := map[string]any{
		"id":             rewriteSubscription(r.ARN, subscription),
		"name":           r.ID,
		"type":           portableToAzureType(r.Service, r.Type),
		"location":       r.Region,
		"resourceGroup":  resourceGroupOrDefault(r.ARN),
		"subscriptionId": subscription,
		"tags":           tagsOrEmpty(r.Tags),
	}

	if sku := skuMap(r); sku != nil {
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

// skuMap renders a resource's SKU fields, or nil when none are set.
func skuMap(r *resourcediscovery.Resource) map[string]any {
	if r.SKU == "" && r.SKUTier == "" && r.SKUCapacity == 0 {
		return nil
	}

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

	return sku
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

// tagsOrEmpty renders a resource's tags for the ARG row, dropping the internal
// "cloudemu:"-prefixed tags the Azure wire handlers stamp on resources (e.g. the
// ARM name and resource-group markers) so they never leak to a client, while
// preserving every real user tag. A nil/all-internal map renders as {}.
func tagsOrEmpty(tags map[string]string) map[string]string {
	if out := StripInternalTags(tags); out != nil {
		return out
	}

	return map[string]string{}
}

// StripInternalTags returns tags with the internal "cloudemu:"-prefixed markers
// removed, preserving every real user tag; it returns nil when nothing remains.
// Exported so other Azure handlers that render resourcediscovery.Resource tags
// (e.g. the resource-group exportTemplate) drop the same internal bookkeeping
// tags Resource Graph does, rather than leaking them.
func StripInternalTags(tags map[string]string) map[string]string {
	var out map[string]string

	for k, v := range tags {
		if strings.HasPrefix(k, internalTagPrefix) {
			continue
		}

		if out == nil {
			out = make(map[string]string, len(tags))
		}

		out[k] = v
	}

	return out
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

// rewriteSubscription replaces the subscription segment of an Azure resource id
// with sub, so the id a Resource Graph client sees is scoped to the subscription
// it queried. An id that isn't subscription-shaped, or an empty sub, is returned
// unchanged.
func rewriteSubscription(arn, sub string) string {
	const prefix = "/subscriptions/"
	if sub == "" || !strings.HasPrefix(arn, prefix) {
		return arn
	}

	rest := arn[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return prefix + sub + rest[i:]
	}

	return prefix + sub
}

// portableToAzureTypeMap is the inverse of mapAzureType's mapping — the engine's
// (service, type) pair back to the dotted Azure type string a real ARG response
// carries. A map lookup rather than a switch keeps gocyclo under the gate as the
// pairs grow.
var portableToAzureTypeMap = map[string]string{ //nolint:gochecknoglobals // static lookup table
	"compute/Instance":                    "microsoft.compute/virtualmachines",
	"compute/Volume":                      "microsoft.compute/disks",
	"compute/Snapshot":                    "microsoft.compute/snapshots",
	"compute/ScaleSet":                    "microsoft.compute/virtualmachinescalesets",
	"compute/SqlVirtualMachine":           "microsoft.sqlvirtualmachine/sqlvirtualmachines",
	"networking/VPC":                      "microsoft.network/virtualnetworks",
	"networking/Subnet":                   "microsoft.network/subnets",
	"networking/SecurityGroup":            "microsoft.network/networksecuritygroups",
	"networking/NetworkInterface":         "microsoft.network/networkinterfaces",
	"networking/ElasticIP":                "microsoft.network/publicipaddresses",
	"storage/Bucket":                      "microsoft.storage/storageaccounts",
	"database/Table":                      "microsoft.documentdb/databaseaccounts",
	"serverless/Function":                 "microsoft.web/sites",
	"appservice/AppServicePlan":           "microsoft.web/serverfarms",
	"databricks/Workspace":                "microsoft.databricks/workspaces",
	"kubernetes/Cluster":                  "microsoft.containerservice/managedclusters",
	"kubernetes/NodeGroup":                "microsoft.containerservice/managedclusters/agentpools",
	"relationaldb/SqlServer":              "microsoft.sql/servers",
	"relationaldb/SqlManagedInstance":     "microsoft.sql/managedinstances",
	"relationaldb/SqlDatabase":            "microsoft.sql/servers/databases",
	"relationaldb/MySqlFlexibleServer":    "microsoft.dbformysql/flexibleservers",
	"relationaldb/PostgresFlexibleServer": "microsoft.dbforpostgresql/flexibleservers",
	"secrets/Secret":                      "microsoft.keyvault/vaults/secrets",
	"secrets/Vault":                       "microsoft.keyvault/vaults",
	"containerregistry/Repository":        "microsoft.containerregistry/registries",
	"messagequeue/Queue":                  "microsoft.servicebus/namespaces/queues",
	"notification/Topic":                  "microsoft.notificationhubs/namespaces/notificationhubs",
	"dns/Zone":                            "microsoft.network/dnszones",
	"logging/LogGroup":                    "microsoft.operationalinsights/workspaces",
	"cache/CacheCluster":                  "microsoft.cache/redis",
	"loadbalancer/LoadBalancer":           "microsoft.network/loadbalancers",
	"monitoring/Alarm":                    "microsoft.insights/metricalerts",
	"iam/UserAssignedIdentity":            "microsoft.managedidentity/userassignedidentities",
	"iam/Role":                            "microsoft.authorization/roledefinitions",
	"networking/NatGateway":               "microsoft.network/natgateways",
	"networking/ApplicationSecurityGroup": "microsoft.network/applicationsecuritygroups",
	"networking/PublicIPPrefix":           "microsoft.network/publicipprefixes",
	"networking/RouteTable":               "microsoft.network/routetables",
	"networking/PeeringConnection":        "microsoft.network/virtualnetworks/virtualnetworkpeerings",
	"machinelearningservices/Workspace":   "microsoft.machinelearningservices/workspaces",
	"machinelearningservices/Endpoint":    "microsoft.machinelearningservices/workspaces/onlineendpoints",
	"cognitiveservices/Account":           "microsoft.cognitiveservices/accounts",
	"containerapps/ManagedEnvironment":    "microsoft.app/managedenvironments",
	"containerapps/ContainerApp":          "microsoft.app/containerapps",
}

func portableToAzureType(service, typ string) string {
	key := service + "/" + typ
	if azureType, ok := portableToAzureTypeMap[key]; ok {
		return azureType
	}

	return strings.ToLower(key)
}

// AzureType translates an engine Resource's (Service, Type) into the ARM type
// string a real Azure client expects (e.g. "compute"/"Instance" ->
// "microsoft.compute/virtualmachines"). Exported so other Azure handlers that
// render resourcediscovery.Resource rows into Azure-shaped output — e.g. the
// resource-groups exportTemplate — use the same naming Resource Graph does,
// rather than a second, possibly-diverging mapping.
func AzureType(service, typ string) string {
	return portableToAzureType(service, typ)
}

// ResourceGroupOf reports the resource group embedded in an Azure resource ID,
// or "default" for an ID that doesn't carry one. Exported for the same reason
// as AzureType: other Azure handlers that need "which group does this
// resource belong to" (e.g. exportTemplate) reuse the parsing Resource Graph
// and the generic-resources listing already rely on.
func ResourceGroupOf(id string) string {
	return resourceGroupOrDefault(id)
}

// Compile-time check that Handler implements the Matches+ServeHTTP pair the
// dispatch chain expects.
var _ interface {
	Matches(*http.Request) bool
	http.Handler
} = (*Handler)(nil)
