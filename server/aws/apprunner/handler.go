// Package apprunner implements the AWS App Runner JSON 1.0 protocol as a
// server.Handler. Point the real aws-sdk-go-v2/service/apprunner client (or the
// `aws apprunner` CLI) at a Server registered with this handler and service,
// auto-scaling-configuration, and connection operations run against an in-memory
// App Runner driver.
//
// App Runner uses the AWS JSON 1.0 wire shape (POST + JSON body dispatched on
// the X-Amz-Target header, prefix "AppRunner.").
package apprunner

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	ardriver "github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

const targetPrefix = "AppRunner."

// Handler serves App Runner JSON-RPC requests against an AppRunner driver.
type Handler struct {
	ar     ardriver.AppRunner
	routes map[string]http.HandlerFunc
}

// New returns an App Runner handler backed by d.
func New(d ardriver.AppRunner) *Handler {
	h := &Handler{ar: d}
	h.routes = map[string]http.HandlerFunc{
		// Service.
		"CreateService":   h.createService,
		"DescribeService": h.describeService,
		"DeleteService":   h.deleteService,
		"UpdateService":   h.updateService,
		"ListServices":    h.listServices,
		"PauseService":    h.pauseService,
		"ResumeService":   h.resumeService,
		"StartDeployment": h.startDeployment,
		// AutoScalingConfiguration.
		"CreateAutoScalingConfiguration":          h.createASC,
		"DescribeAutoScalingConfiguration":        h.describeASC,
		"DeleteAutoScalingConfiguration":          h.deleteASC,
		"ListAutoScalingConfigurations":           h.listASC,
		"UpdateDefaultAutoScalingConfiguration":   h.updateDefaultASC,
		"ListServicesForAutoScalingConfiguration": h.listServicesForASC,
		// Connection.
		"CreateConnection": h.createConnection,
		"DeleteConnection": h.deleteConnection,
		"ListConnections":  h.listConnections,
		// ObservabilityConfiguration.
		"CreateObservabilityConfiguration":   h.createObs,
		"DescribeObservabilityConfiguration": h.describeObs,
		"DeleteObservabilityConfiguration":   h.deleteObs,
		"ListObservabilityConfigurations":    h.listObs,
		// VpcConnector.
		"CreateVpcConnector":   h.createVpcConnector,
		"DescribeVpcConnector": h.describeVpcConnector,
		"DeleteVpcConnector":   h.deleteVpcConnector,
		"ListVpcConnectors":    h.listVpcConnectors,
		// VpcIngressConnection.
		"CreateVpcIngressConnection":   h.createVpcIngress,
		"DescribeVpcIngressConnection": h.describeVpcIngress,
		"DeleteVpcIngressConnection":   h.deleteVpcIngress,
		"ListVpcIngressConnections":    h.listVpcIngress,
		"UpdateVpcIngressConnection":   h.updateVpcIngress,
		// CustomDomain.
		"AssociateCustomDomain":    h.associateCustomDomain,
		"DisassociateCustomDomain": h.disassociateCustomDomain,
		"DescribeCustomDomains":    h.describeCustomDomains,
		// Operations.
		"ListOperations": h.listOperations,
		// Tags.
		"ListTagsForResource": h.listTags,
		"TagResource":         h.tagResource,
		"UntagResource":       h.untagResource,
	}

	return h
}

// Matches returns true for App Runner-shaped requests (X-Amz-Target of
// "AppRunner.<Operation>"). Because App Runner is X-Amz-Target dispatched, its
// predicate is disjoint from S3 (which rejects any X-Amz-Target) and from every
// other JSON-RPC service (distinct target prefix), so registration order is free.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches App Runner operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported App Runner operation: "+r.Header.Get("X-Amz-Target"))
}

// dispatch decodes a JSON request of type Req, invokes call, and writes the
// returned value as JSON (or maps the error).
func dispatch[Req any](
	h *Handler, w http.ResponseWriter, r *http.Request,
	call func(*Handler, context.Context, *Req) (any, error),
) {
	var req Req
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	out, err := call(h, r.Context(), &req)
	if err != nil {
		writeErr(w, err)

		return
	}

	wire.WriteJSON(w, out)
}

// writeErr maps a driver error to the closest App Runner JSON error type. Errors
// tagged with a specific App Runner exception (via driver.APIError) take
// precedence so distinct exceptions like ResourceNotFoundException /
// InvalidStateException surface as themselves.
func writeErr(w http.ResponseWriter, err error) {
	var apiErr *ardriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, statusFor(apiErr.Exception), apiErr.Exception, err.Error())

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, ardriver.ExResourceNotFound, err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, ardriver.ExInvalidRequest, err.Error())
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, ardriver.ExInvalidState, err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, ardriver.ExInternalServiceError, err.Error())
	}
}

// statusFor maps an App Runner exception to its HTTP status. App Runner's
// modeled client exceptions are 400; the internal-service exception is 500.
func statusFor(exception string) int {
	if exception == ardriver.ExInternalServiceError {
		return http.StatusInternalServerError
	}

	return http.StatusBadRequest
}
