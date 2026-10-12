// Package sagemaker implements the Amazon SageMaker awsJson1_1 control-plane
// API and the sagemaker-runtime restJson1 InvokeEndpoint data-plane API as a
// server.Handler. Point the real aws-sdk-go-v2/service/sagemaker and
// .../sagemakerruntime clients at a Server registered with this handler and
// the jobs, model/endpoint inference stack and tagging surface work end-to-end
// against an in-memory SageMaker driver.
//
// The control plane is dispatched by the X-Amz-Target header
// ("SageMaker.<Operation>"); the runtime is dispatched by the REST path
// /endpoints/{name}/invocations. Matches is scoped to those so it does not
// shadow the catch-all S3 handler registered alongside. Register this handler
// before S3.
package sagemaker

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

const targetPrefix = "SageMaker."

// Handler serves SageMaker control-plane and runtime requests.
type Handler struct {
	svc driver.Service
	ops map[string]http.HandlerFunc
}

// New returns a SageMaker handler backed by svc.
func New(svc driver.Service) *Handler {
	h := &Handler{svc: svc}
	h.ops = h.routes()

	return h
}

// Matches claims awsJson1_1 control-plane requests (by X-Amz-Target), the
// runtime invocation REST paths, and the feature-store online-runtime paths.
func (*Handler) Matches(r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix) {
		return true
	}

	return isRuntimePath(r.URL.Path) || isFeatureStorePath(r.URL.Path)
}

func isRuntimePath(p string) bool {
	return strings.HasPrefix(p, "/endpoints/") &&
		(strings.HasSuffix(p, "/invocations") || strings.HasSuffix(p, "/async-invocations"))
}

func isFeatureStorePath(p string) bool {
	return strings.HasPrefix(p, "/FeatureGroup/")
}

// ServeHTTP dispatches by X-Amz-Target for the control plane, falling back to
// the runtime and feature-store REST paths.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch classify(r) {
	case routeRuntime:
		h.serveRuntime(w, r)

		return
	case routeFeatureStore:
		h.serveFeatureStoreRuntime(w, r)

		return
	case routeControl:
	}

	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if handle, ok := h.ops[op]; ok {
		if validationNotFoundOps[op] {
			w = notFoundAsValidation{w}
		}

		handle(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException", "unknown operation: "+op)
}

// decodeName1 decodes a request body and returns the string value of the
// single named field (e.g. "TrainingJobName"). Used by the many Describe/Stop
// operations whose request is just one identifier.
func decodeName1(w http.ResponseWriter, r *http.Request, field string) (string, bool) {
	var m map[string]any
	if !wire.DecodeJSON(w, r, &m) {
		return "", false
	}

	name, _ := m[field].(string)

	return name, true
}

// stopByName decodes the named identifier and invokes a Stop/Delete-style
// driver call that returns only an error, writing an empty success body.
func stopByName(w http.ResponseWriter, r *http.Request, field string, fn func(context.Context, string) error) {
	name, ok := decodeName1(w, r, field)
	if !ok {
		return
	}

	if err := fn(r.Context(), name); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}

// writeSummaries projects items into the awsJson1_1 list shape
// {"<key>": [ {...}, ... ]} via per-item project. It collapses the otherwise
// identical make/loop/WriteJSON boilerplate shared by every List* handler.
func writeSummaries[T any](w http.ResponseWriter, key string, page driver.ListPage[T], project func(*T) map[string]any) {
	out := make([]map[string]any, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, project(&page.Items[i]))
	}

	resp := map[string]any{key: out}
	if page.NextToken != "" {
		resp["NextToken"] = page.NextToken
	}

	wire.WriteJSON(w, resp)
}

// epoch converts a stored RFC3339 timestamp to Unix seconds for awsJson1_1
// timestamp serialization (the SDK's default format is unixTimestamp).
func epoch(iso string) float64 {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return 0
	}

	return float64(t.UnixMilli()) / millisPerSecond
}

const millisPerSecond = 1000

func toTags(in []wireTag) []driver.Tag {
	out := make([]driver.Tag, 0, len(in))
	for _, t := range in {
		out = append(out, driver.Tag{Key: t.Key, Value: t.Value})
	}

	return out
}

func fromTags(in []driver.Tag) []wireTag {
	out := make([]wireTag, 0, len(in))
	for _, t := range in {
		out = append(out, wireTag{Key: t.Key, Value: t.Value})
	}

	return out
}

// wireTag is the JSON shape of a SageMaker tag.
type wireTag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// IAMService returns the IAM service prefix of the operations this handler
// serves.
func (*Handler) IAMService() string { return "sagemaker" }
