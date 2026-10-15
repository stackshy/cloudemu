package sagemaker

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

// route is which of the handler's three surfaces serves a request.
type route int

const (
	routeControl route = iota
	routeRuntime
	routeFeatureStore
)

// classify picks the surface the way ServeHTTP dispatches: the runtime and
// feature-store paths win over X-Amz-Target, so a control-plane target sent
// to a runtime path still runs the runtime operation.
func classify(r *http.Request) route {
	switch {
	case isRuntimePath(r.URL.Path):
		return routeRuntime
	case isFeatureStorePath(r.URL.Path):
		return routeFeatureStore
	default:
		return routeControl
	}
}

// featureStoreActions maps a feature-store record method to its IAM action.
//
//nolint:gochecknoglobals // static lookup table
var featureStoreActions = map[string]string{
	http.MethodPut:    "PutRecord",
	http.MethodGet:    "GetRecord",
	http.MethodDelete: "DeleteRecord",
}

// IAMChecks names the IAM action of a request from the surface classify
// picks, which is the one ServeHTTP runs. A method the runtime or feature
// store does not serve returns ok=false; the handler answers it with 405.
func (h *Handler) IAMChecks(r *http.Request, _ awsauthz.Scope) ([]awsauthz.Check, bool) {
	var op string

	switch classify(r) {
	case routeRuntime:
		if r.Method != http.MethodPost {
			return nil, false
		}

		op = "InvokeEndpoint"
		if strings.HasSuffix(r.URL.Path, "/async-invocations") {
			op = "InvokeEndpointAsync"
		}
	case routeFeatureStore:
		op = featureStoreActions[r.Method]
	case routeControl:
		op = strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
	}

	if op == "" {
		return nil, false
	}

	return awsauthz.Single(h.IAMService()+":"+op, ""), true
}
