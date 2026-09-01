// Package emr implements the Amazon EMR (Elastic MapReduce) AWS JSON 1.1
// protocol as a server.Handler. Point the real aws-sdk-go-v2/service/emr client
// (or the `aws emr` CLI) at a Server registered with this handler and the
// cluster/step lifecycle runs against an in-memory backend.
//
// EMR uses the AWS JSON 1.1 wire shape (POST + JSON body dispatched on the
// X-Amz-Target header, prefix "ElasticMapReduce."). Cluster and step state live
// only in the wire server — no portable driver interface represents them — so
// the handler owns a self-contained thread-safe store rather than a three-layer
// provider driver. Clusters are created WAITING by RunJobFlow and moved to
// TERMINATED by TerminateJobFlows; steps execute instantly and land COMPLETED.
package emr

import (
	"context"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

const targetPrefix = "ElasticMapReduce."

// Handler serves EMR JSON-RPC requests backed by an in-memory cluster store.
type Handler struct {
	store  *store
	routes map[string]http.HandlerFunc
}

// New returns an EMR handler. accountID and region shape generated cluster ARNs;
// clock (nil = real clock) drives the lifecycle timeline for deterministic tests.
func New(accountID, region string, clock config.Clock) *Handler {
	h := &Handler{store: newStore(accountID, region, clock)}
	h.routes = map[string]http.HandlerFunc{
		"RunJobFlow":           h.runJobFlow,
		"DescribeCluster":      h.describeCluster,
		"ListClusters":         h.listClusters,
		"TerminateJobFlows":    h.terminateJobFlows,
		"AddJobFlowSteps":      h.addJobFlowSteps,
		"ListSteps":            h.listSteps,
		"DescribeStep":         h.describeStep,
		"CancelSteps":          h.cancelSteps,
		"AddInstanceGroups":    h.addInstanceGroups,
		"ModifyInstanceGroups": h.modifyInstanceGroups,
		"ListInstanceGroups":   h.listInstanceGroups,
		"ListInstances":        h.listInstances,
		"ListBootstrapActions": h.listBootstrapActions,
	}

	return h
}

// Matches returns true for EMR-shaped requests (X-Amz-Target of
// "ElasticMapReduce.<Operation>"). The prefix is disjoint from every other AWS
// JSON-RPC service, so registration order is unconstrained.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches EMR operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported EMR operation: "+r.Header.Get("X-Amz-Target"))
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

// writeErr maps a store/validation error to the closest EMR JSON error type.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsInvalidArgument(err), cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidRequestException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalFailure", msg)
	}
}
