package lambda

import (
	"context"
	"net/http"
	"strings"
	"time"

	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// eventInvokeConfigPrefix is the Lambda asynchronous-invocation-config API
// version prefix (PutFunctionEventInvokeConfig et al), a function sub-resource
// (.../{name}/event-invoke-config) versioned under 2019-09-25 — its own prefix,
// so it needs a Matches clause.
const eventInvokeConfigPrefix = "/2019-09-25/functions"

// eventInvokeConfigSuffix / listSuffix are the sub-resource path segments the
// event-invoke-config routes hang off {name}.
const (
	eventInvokeConfigSuffix = "event-invoke-config"
	eventInvokeListSegment  = "list"
)

// eventInvokeConfigManager is the AWS-specific asynchronous-invoke-config
// surface (retries, max event age, OnSuccess/OnFailure destinations). It has no
// Azure/GCP equivalent, so the handler type-asserts for it rather than adding it
// to the portable Serverless driver, mirroring functionURLManager.
type eventInvokeConfigManager interface {
	PutFunctionEventInvokeConfig(ctx context.Context, cfg sdrv.EventInvokeConfig) (*sdrv.EventInvokeConfig, error)
	UpdateFunctionEventInvokeConfig(ctx context.Context, cfg sdrv.EventInvokeConfig) (*sdrv.EventInvokeConfig, error)
	GetFunctionEventInvokeConfig(ctx context.Context, functionName, qualifier string) (*sdrv.EventInvokeConfig, error)
	DeleteFunctionEventInvokeConfig(ctx context.Context, functionName, qualifier string) error
	ListFunctionEventInvokeConfigs(ctx context.Context, functionName string) ([]sdrv.EventInvokeConfig, error)
}

// destinationJSON is the AWS OnSuccess/OnFailure Destination shape.
type destinationJSON struct {
	Destination string `json:"Destination"`
}

// destinationConfigJSON is the AWS DestinationConfig shape shared by the request
// and response.
type destinationConfigJSON struct {
	OnSuccess *destinationJSON `json:"OnSuccess,omitempty"`
	OnFailure *destinationJSON `json:"OnFailure,omitempty"`
}

// eventInvokeConfigRequest is the body of Put/UpdateFunctionEventInvokeConfig.
type eventInvokeConfigRequest struct {
	MaximumRetryAttempts     *int                   `json:"MaximumRetryAttempts"`
	MaximumEventAgeInSeconds *int                   `json:"MaximumEventAgeInSeconds"`
	DestinationConfig        *destinationConfigJSON `json:"DestinationConfig"`
}

// eventInvokeConfigResponse is the AWS FunctionEventInvokeConfig shape.
// LastModified is emitted as epoch seconds (the rest-json timestamp default the
// SDK decodes into *time.Time).
type eventInvokeConfigResponse struct {
	FunctionArn              string                 `json:"FunctionArn"`
	LastModified             float64                `json:"LastModified"`
	MaximumRetryAttempts     *int                   `json:"MaximumRetryAttempts,omitempty"`
	MaximumEventAgeInSeconds *int                   `json:"MaximumEventAgeInSeconds,omitempty"`
	DestinationConfig        *destinationConfigJSON `json:"DestinationConfig,omitempty"`
}

// listEventInvokeConfigsResponse is the ListFunctionEventInvokeConfigs envelope.
type listEventInvokeConfigsResponse struct {
	FunctionEventInvokeConfigs []eventInvokeConfigResponse `json:"FunctionEventInvokeConfigs"`
}

func toDestinationDriver(d *destinationJSON) *sdrv.Destination {
	if d == nil {
		return nil
	}

	return &sdrv.Destination{Destination: d.Destination}
}

func toDestinationConfigDriver(dc *destinationConfigJSON) *sdrv.DestinationConfig {
	if dc == nil {
		return nil
	}

	return &sdrv.DestinationConfig{
		OnSuccess: toDestinationDriver(dc.OnSuccess),
		OnFailure: toDestinationDriver(dc.OnFailure),
	}
}

func toDestinationJSON(d *sdrv.Destination) *destinationJSON {
	if d == nil {
		return nil
	}

	return &destinationJSON{Destination: d.Destination}
}

func toDestinationConfigJSON(dc *sdrv.DestinationConfig) *destinationConfigJSON {
	if dc == nil {
		return nil
	}

	return &destinationConfigJSON{
		OnSuccess: toDestinationJSON(dc.OnSuccess),
		OnFailure: toDestinationJSON(dc.OnFailure),
	}
}

func toEventInvokeConfigResponse(c *sdrv.EventInvokeConfig) eventInvokeConfigResponse {
	return eventInvokeConfigResponse{
		FunctionArn:              c.FunctionArn,
		LastModified:             epochSeconds(c.LastModified),
		MaximumRetryAttempts:     c.MaximumRetryAttempts,
		MaximumEventAgeInSeconds: c.MaximumEventAgeInSeconds,
		DestinationConfig:        toDestinationConfigJSON(c.DestinationConfig),
	}
}

// epochSeconds converts the driver's RFC3339 LastModified string to epoch
// seconds for the wire; a blank/unparsable value falls back to 0.
func epochSeconds(rfc3339 string) float64 {
	t, err := time.Parse("2006-01-02T15:04:05Z", rfc3339)
	if err != nil {
		return 0
	}

	return float64(t.Unix())
}

// serveEventInvokeConfig dispatches the Lambda async-invoke-config API under
// /2019-09-25/functions/{name}/event-invoke-config (PUT=put, POST=update,
// GET=get, DELETE=delete) and .../event-invoke-config/list (GET=list). The
// Qualifier query parameter scopes the config to a version or alias.
func (h *Handler) serveEventInvokeConfig(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.fn.(eventInvokeConfigManager)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "event invoke config not supported")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, eventInvokeConfigPrefix)
	rest = strings.TrimPrefix(rest, "/")
	parts := strings.Split(rest, "/")

	const (
		itemParts = 2 // {name}/event-invoke-config
		listParts = 3 // {name}/event-invoke-config/list
	)

	if len(parts) < itemParts || parts[0] == "" || parts[1] != eventInvokeConfigSuffix {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda path")
		return
	}

	name := parts[0]

	if len(parts) == listParts && parts[2] == eventInvokeListSegment {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
			return
		}

		listEventInvokeConfigs(w, r, mgr, name)

		return
	}

	if len(parts) != itemParts {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda path")
		return
	}

	serveEventInvokeConfigItem(w, r, mgr, name)
}

// serveEventInvokeConfigItem handles PUT/POST/GET/DELETE on
// .../{name}/event-invoke-config.
func serveEventInvokeConfigItem(w http.ResponseWriter, r *http.Request, mgr eventInvokeConfigManager, name string) {
	qualifier := r.URL.Query().Get("Qualifier")

	switch r.Method {
	case http.MethodPut:
		writeEventInvokeConfig(w, r, mgr.PutFunctionEventInvokeConfig, name, qualifier)
	case http.MethodPost:
		writeEventInvokeConfig(w, r, mgr.UpdateFunctionEventInvokeConfig, name, qualifier)
	case http.MethodGet:
		c, err := mgr.GetFunctionEventInvokeConfig(r.Context(), name, qualifier)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, toEventInvokeConfigResponse(c))
	case http.MethodDelete:
		if err := mgr.DeleteFunctionEventInvokeConfig(r.Context(), name, qualifier); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

// writeEventInvokeConfig decodes a put/update body and writes the result. The op
// (put vs update) is supplied as fn so both share the decode/encode path.
func writeEventInvokeConfig(
	w http.ResponseWriter, r *http.Request,
	fn func(context.Context, sdrv.EventInvokeConfig) (*sdrv.EventInvokeConfig, error),
	name, qualifier string,
) {
	var req eventInvokeConfigRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	c, err := fn(r.Context(), sdrv.EventInvokeConfig{
		FunctionName:             name,
		Qualifier:                qualifier,
		MaximumRetryAttempts:     req.MaximumRetryAttempts,
		MaximumEventAgeInSeconds: req.MaximumEventAgeInSeconds,
		DestinationConfig:        toDestinationConfigDriver(req.DestinationConfig),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toEventInvokeConfigResponse(c))
}

// listEventInvokeConfigs renders ListFunctionEventInvokeConfigs. The GET-only
// method guard sits in the dispatcher (serveEventInvokeConfig).
func listEventInvokeConfigs(w http.ResponseWriter, r *http.Request, mgr eventInvokeConfigManager, name string) {
	configs, err := mgr.ListFunctionEventInvokeConfigs(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	items := make([]eventInvokeConfigResponse, 0, len(configs))
	for i := range configs {
		items = append(items, toEventInvokeConfigResponse(&configs[i]))
	}

	writeJSON(w, http.StatusOK, listEventInvokeConfigsResponse{FunctionEventInvokeConfigs: items})
}
