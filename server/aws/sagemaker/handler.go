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
// shadow the catch-all S3 handler registered alongside — register this handler
// before S3.
package sagemaker

import (
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/sagemaker/driver"
	"github.com/stackshy/cloudemu/server/wire"
)

const targetPrefix = "SageMaker."

// Handler serves SageMaker control-plane and runtime requests.
type Handler struct {
	svc driver.Service
}

// New returns a SageMaker handler backed by svc.
func New(svc driver.Service) *Handler {
	return &Handler{svc: svc}
}

// Matches claims awsJson1_1 control-plane requests (by X-Amz-Target) and the
// runtime invocation REST paths.
func (*Handler) Matches(r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix) {
		return true
	}

	return isRuntimePath(r.URL.Path)
}

func isRuntimePath(p string) bool {
	return strings.HasPrefix(p, "/endpoints/") &&
		(strings.HasSuffix(p, "/invocations") || strings.HasSuffix(p, "/async-invocations"))
}

// ServeHTTP dispatches by X-Amz-Target for the control plane, falling back to
// the runtime REST path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isRuntimePath(r.URL.Path) {
		h.serveRuntime(w, r)

		return
	}

	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if h.routeModels(w, r, op) || h.routeEndpoints(w, r, op) ||
		h.routeJobs(w, r, op) || h.routeTags(w, r, op) {
		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException", "unknown operation: "+op)
}

func (h *Handler) routeModels(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateModel":
		h.createModel(w, r)
	case "DescribeModel":
		h.describeModel(w, r)
	case "ListModels":
		h.listModels(w, r)
	case "DeleteModel":
		h.deleteModel(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeEndpoints(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateEndpointConfig":
		h.createEndpointConfig(w, r)
	case "DescribeEndpointConfig":
		h.describeEndpointConfig(w, r)
	case "DeleteEndpointConfig":
		h.deleteEndpointConfig(w, r)
	case "CreateEndpoint":
		h.createEndpoint(w, r)
	case "DescribeEndpoint":
		h.describeEndpoint(w, r)
	case "ListEndpoints":
		h.listEndpoints(w, r)
	case "DeleteEndpoint":
		h.deleteEndpoint(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeJobs(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateTrainingJob":
		h.createTrainingJob(w, r)
	case "DescribeTrainingJob":
		h.describeTrainingJob(w, r)
	case "ListTrainingJobs":
		h.listTrainingJobs(w, r)
	case "StopTrainingJob":
		h.stopTrainingJob(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeTags(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "AddTags":
		h.addTags(w, r)
	case "ListTags":
		h.listTags(w, r)
	case "DeleteTags":
		h.deleteTags(w, r)
	default:
		return false
	}

	return true
}

// epoch converts a stored RFC3339 timestamp to Unix seconds for awsJson1_1
// timestamp serialization (the SDK's default format is unixTimestamp).
func epoch(iso string) float64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return 0
	}

	return float64(t.Unix())
}

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
