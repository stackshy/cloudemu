// Package costmanagement serves the Azure Cost Management query API
// (Microsoft.CostManagement/query) so the real armcostmanagement SDK's
// QueryClient.Usage works against the emulator. It reports deterministic cost
// data derived from the current inventory, so FinOps flows (a monthly-cost
// query, a cost breakdown grouped by service or resource type) can be exercised
// offline.
//
// The handler owns no pricing or aggregation logic: it prices the estate
// through the provider-agnostic services/cost estimator (which in turn prices
// each discovered resource via services/pricing) and only shapes the resulting
// per-resource costs into the Cost Management columns/rows wire format,
// honoring the request's granularity and grouping. This is the same
// engine-backed pattern the Resource Graph handler follows.
package costmanagement

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	"github.com/stackshy/cloudemu/v2/services/cost"
)

// pathSuffix is the well-known Cost Management query URL tail. The SDK targets
// /{scope}/providers/Microsoft.CostManagement/query where {scope} is a
// subscription, resource group, management group, or billing scope; the scope
// prefix and the api-version query string vary, so we match on the suffix. The
// Microsoft.CostManagement provider name is disjoint from every other Azure
// handler (ResourceGraph, resourcegroups, tags, locks, …), so this cannot
// shadow them.
const pathSuffix = "/providers/Microsoft.CostManagement/query"

// currency is the single currency the emulator prices in; services/pricing
// returns USD figures.
const currency = "USD"

// defaultCostColumn is the response column name for the aggregated cost when a
// request omits its aggregation (or an aggregation omits its column name).
const defaultCostColumn = "Cost"

// resultName is the query result's resource name. Real Cost Management returns
// a per-request GUID here; a fixed value keeps the emulator deterministic and
// no client keys off it.
const resultName = "cloudemu-cost-query"

// Estimator prices an inventory into per-resource cost lines. It is satisfied
// by the services/cost package; the interface keeps this handler decoupled from
// the concrete estimator and trivially fakeable.
type Estimator interface {
	Estimate(ctx context.Context) ([]cost.Line, float64, error)
}

// engineEstimator adapts a services/cost.Inventory (the discovery engine) into
// an Estimator by binding it to the shared cost.Estimate walk.
type engineEstimator struct {
	inv cost.Inventory
}

func (e engineEstimator) Estimate(ctx context.Context) ([]cost.Line, float64, error) {
	return cost.Estimate(ctx, e.inv)
}

// Handler serves Cost Management query requests over a cost Estimator.
type Handler struct {
	est Estimator
}

// New returns a Cost Management handler backed by inv (the provider's resource
// discovery engine). Returns nil when inv is nil so the caller can skip
// registration, matching the other discovery-backed Azure handlers.
func New(inv cost.Inventory) *Handler {
	if inv == nil {
		return nil
	}

	return &Handler{est: engineEstimator{inv: inv}}
}

// Matches accepts POST requests whose path ends with the Cost Management query
// suffix, at any scope.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasSuffix(r.URL.Path, pathSuffix)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "POST required")

		return
	}

	var def queryDefinition
	if !azurearm.DecodeJSON(w, r, &def) {
		return
	}

	lines, _, err := h.est.Estimate(r.Context())
	if err != nil {
		azurearm.WriteCErr(w, err)

		return
	}

	columns, rows := shape(&def, lines)

	scope := strings.TrimSuffix(r.URL.Path, pathSuffix)

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{
		"id":   scope + "/providers/Microsoft.CostManagement/Query/" + resultName,
		"name": resultName,
		"type": "Microsoft.CostManagement/query",
		"properties": map[string]any{
			"nextLink": nil,
			"columns":  columns,
			"rows":     rows,
		},
	})
}

// queryDefinition is the subset of the SDK QueryDefinition this handler reads.
// Only the fields that change the shape of the response (granularity, grouping,
// aggregation column names) are decoded; filters/timeframe are accepted and
// ignored because the emulator holds a single point-in-time estate.
type queryDefinition struct {
	Type    string `json:"type"`
	Dataset struct {
		Granularity string                     `json:"granularity"`
		Aggregation map[string]aggregationSpec `json:"aggregation"`
		Grouping    []groupingSpec             `json:"grouping"`
	} `json:"dataset"`
}

type aggregationSpec struct {
	Name     string `json:"name"`
	Function string `json:"function"`
}

type groupingSpec struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// column is one Cost Management result column descriptor.
type column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Compile-time check that Handler implements the Matches+ServeHTTP pair the
// dispatch chain expects.
var _ interface {
	Matches(*http.Request) bool
	http.Handler
} = (*Handler)(nil)
