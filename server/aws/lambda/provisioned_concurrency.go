package lambda

import (
	"context"
	"net/http"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// provisionedConcurrencySuffix is the function sub-resource path segment
// Put/Get/DeleteProvisionedConcurrencyConfig share, all under the
// concurrencyReadPrefix (2019-09-30) version. ListProvisionedConcurrencyConfigs
// lives on the SAME path (real Lambda distinguishes it with the
// provisionedConcurrencyListParam query parameter) rather than its own suffix.
const provisionedConcurrencySuffix = "/provisioned-concurrency"

// provisionedConcurrencyListParam / provisionedConcurrencyListParamAll are the
// query parameter real Lambda uses to route a GET on the provisioned-concurrency
// path to ListProvisionedConcurrencyConfigs instead of
// GetProvisionedConcurrencyConfig.
const (
	provisionedConcurrencyListParam    = "List"
	provisionedConcurrencyListParamAll = "ALL"
)

// provisionedConcurrencyManager is the AWS-specific provisioned-concurrency
// surface. It has no Azure/GCP equivalent, so the handler type-asserts for it
// rather than adding it to the portable Serverless driver, mirroring
// eventInvokeConfigManager.
type provisionedConcurrencyManager interface {
	PutFunctionProvisionedConcurrencyConfig(
		ctx context.Context, cfg sdrv.ProvisionedConcurrencyConfig,
	) (*sdrv.ProvisionedConcurrencyConfig, error)
	GetFunctionProvisionedConcurrencyConfig(
		ctx context.Context, functionName, qualifier string,
	) (*sdrv.ProvisionedConcurrencyConfig, error)
	DeleteFunctionProvisionedConcurrencyConfig(ctx context.Context, functionName, qualifier string) error
	ListFunctionProvisionedConcurrencyConfigs(
		ctx context.Context, functionName string,
	) ([]sdrv.ProvisionedConcurrencyConfig, error)
}

// provisionedConcurrencyResponse is the AWS Put/GetProvisionedConcurrencyConfig
// response shape (no FunctionArn — that only appears on the List item shape).
type provisionedConcurrencyResponse struct {
	RequestedProvisionedConcurrentExecutions int    `json:"RequestedProvisionedConcurrentExecutions,omitempty"`
	AvailableProvisionedConcurrentExecutions int    `json:"AvailableProvisionedConcurrentExecutions,omitempty"`
	AllocatedProvisionedConcurrentExecutions int    `json:"AllocatedProvisionedConcurrentExecutions,omitempty"`
	Status                                   string `json:"Status,omitempty"`
	StatusReason                             string `json:"StatusReason,omitempty"`
	LastModified                             string `json:"LastModified,omitempty"`
}

// provisionedConcurrencyListItem is the ListProvisionedConcurrencyConfigs item
// shape: the same fields as provisionedConcurrencyResponse plus FunctionArn.
type provisionedConcurrencyListItem struct {
	provisionedConcurrencyResponse
	FunctionArn string `json:"FunctionArn,omitempty"`
}

// listProvisionedConcurrencyConfigsResponse is the
// ListProvisionedConcurrencyConfigs envelope.
type listProvisionedConcurrencyConfigsResponse struct {
	ProvisionedConcurrencyConfigs []provisionedConcurrencyListItem `json:"ProvisionedConcurrencyConfigs"`
	NextMarker                    string                           `json:"NextMarker,omitempty"`
}

func toProvisionedConcurrencyResponse(c *sdrv.ProvisionedConcurrencyConfig) provisionedConcurrencyResponse {
	return provisionedConcurrencyResponse{
		RequestedProvisionedConcurrentExecutions: c.RequestedProvisionedConcurrentExecutions,
		AvailableProvisionedConcurrentExecutions: c.AvailableProvisionedConcurrentExecutions,
		AllocatedProvisionedConcurrentExecutions: c.AllocatedProvisionedConcurrentExecutions,
		Status:                                   c.Status,
		StatusReason:                             c.StatusReason,
		LastModified:                             c.LastModified,
	}
}

func toProvisionedConcurrencyListItem(c *sdrv.ProvisionedConcurrencyConfig) provisionedConcurrencyListItem {
	return provisionedConcurrencyListItem{
		provisionedConcurrencyResponse: toProvisionedConcurrencyResponse(c),
		FunctionArn:                    c.FunctionArn,
	}
}

// isProvisionedConcurrencyPath reports whether path is the
// .../{name}/provisioned-concurrency sub-resource (2019-09-30).
func isProvisionedConcurrencyPath(path string) bool {
	return strings.HasPrefix(path, concurrencyReadPrefix) && strings.HasSuffix(path, provisionedConcurrencySuffix)
}

// provisionedConcurrencyFunctionName extracts the function name from a
// provisioned-concurrency path, or false when the path shape doesn't match
// (e.g. a nested extra segment).
func provisionedConcurrencyFunctionName(path string) (string, bool) {
	if !isProvisionedConcurrencyPath(path) {
		return "", false
	}

	rest := strings.TrimPrefix(strings.TrimPrefix(path, concurrencyReadPrefix), "/")
	name := strings.TrimSuffix(rest, provisionedConcurrencySuffix)

	if name == "" || strings.Contains(name, "/") {
		return "", false
	}

	return name, true
}

// serveProvisionedConcurrency dispatches the provisioned-concurrency
// sub-resource: PUT=Put, DELETE=Delete, GET=Get (or List when the List=ALL
// query parameter is present, matching real Lambda's routing on this shared
// path).
func (h *Handler) serveProvisionedConcurrency(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.fn.(provisionedConcurrencyManager)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "provisioned concurrency not supported")
		return
	}

	name, ok := provisionedConcurrencyFunctionName(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda path")
		return
	}

	if r.Method == http.MethodGet && r.URL.Query().Get(provisionedConcurrencyListParam) == provisionedConcurrencyListParamAll {
		listProvisionedConcurrencyConfigs(w, r, mgr, name)
		return
	}

	qualifier := r.URL.Query().Get("Qualifier")

	switch r.Method {
	case http.MethodPut:
		putProvisionedConcurrency(w, r, mgr, name, qualifier)
	case http.MethodGet:
		h.getProvisionedConcurrency(w, r, mgr, name, qualifier)
	case http.MethodDelete:
		h.deleteProvisionedConcurrency(w, r, mgr, name, qualifier)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

// putProvisionedConcurrency handles PUT: it always creates/replaces the
// config, so the function's existence (or a $LATEST/reserved-concurrency
// violation) surfaces as the provider's own error.
func putProvisionedConcurrency(
	w http.ResponseWriter, r *http.Request, mgr provisionedConcurrencyManager, name, qualifier string,
) {
	var req struct {
		ProvisionedConcurrentExecutions int `json:"ProvisionedConcurrentExecutions"`
	}

	if !decodeJSON(w, r, &req) {
		return
	}

	cfg, err := mgr.PutFunctionProvisionedConcurrencyConfig(r.Context(), sdrv.ProvisionedConcurrencyConfig{
		FunctionName:                             name,
		Qualifier:                                qualifier,
		RequestedProvisionedConcurrentExecutions: req.ProvisionedConcurrentExecutions,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusAccepted, toProvisionedConcurrencyResponse(cfg))
}

// getProvisionedConcurrency checks the function exists first so a subsequent
// NotFound from the provider is unambiguously "no config for this qualifier"
// (ProvisionedConcurrencyConfigNotFoundException) rather than "function
// missing" (ResourceNotFoundException) — real Lambda distinguishes the two.
func (h *Handler) getProvisionedConcurrency(
	w http.ResponseWriter, r *http.Request, mgr provisionedConcurrencyManager, name, qualifier string,
) {
	if _, err := h.fn.GetFunction(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}

	cfg, err := mgr.GetFunctionProvisionedConcurrencyConfig(r.Context(), name, qualifier)
	if err != nil {
		writeProvisionedConcurrencyNotFound(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toProvisionedConcurrencyResponse(cfg))
}

// deleteProvisionedConcurrency applies the same function-exists-first check as
// getProvisionedConcurrency so a Delete's NotFound is reported as the specific
// ProvisionedConcurrencyConfigNotFoundException.
func (h *Handler) deleteProvisionedConcurrency(
	w http.ResponseWriter, r *http.Request, mgr provisionedConcurrencyManager, name, qualifier string,
) {
	if _, err := h.fn.GetFunction(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}

	if err := mgr.DeleteFunctionProvisionedConcurrencyConfig(r.Context(), name, qualifier); err != nil {
		writeProvisionedConcurrencyNotFound(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeProvisionedConcurrencyNotFound emits the 404
// ProvisionedConcurrencyConfigNotFoundException real Lambda returns when a
// function exists but has no provisioned-concurrency config for the requested
// qualifier — distinct from the generic ResourceNotFoundException a missing
// function reports.
func writeProvisionedConcurrencyNotFound(w http.ResponseWriter, err error) {
	writeError(w, http.StatusNotFound, "ProvisionedConcurrencyConfigNotFoundException", cerrors.Message(err))
}

// listProvisionedConcurrencyConfigs renders ListProvisionedConcurrencyConfigs,
// sorting by qualifier so Marker offsets stay stable across paginated calls
// (the driver returns configs in map-iteration order, which is unstable).
func listProvisionedConcurrencyConfigs(
	w http.ResponseWriter, r *http.Request, mgr provisionedConcurrencyManager, name string,
) {
	configs, err := mgr.ListFunctionProvisionedConcurrencyConfigs(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	sort.Slice(configs, func(i, j int) bool { return configs[i].Qualifier < configs[j].Qualifier })

	start, end, nextMarker, _ := pageWindow(len(configs), r.URL.Query())

	items := make([]provisionedConcurrencyListItem, 0, end-start)
	for i := start; i < end; i++ {
		items = append(items, toProvisionedConcurrencyListItem(&configs[i]))
	}

	writeJSON(w, http.StatusOK, listProvisionedConcurrencyConfigsResponse{
		ProvisionedConcurrencyConfigs: items,
		NextMarker:                    nextMarker,
	})
}
