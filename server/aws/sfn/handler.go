// Package sfn implements the AWS Step Functions JSON 1.0 protocol as a
// server.Handler. Point the real aws-sdk-go-v2/service/sfn client (or the
// `aws stepfunctions` CLI) at a Server registered with this handler and
// state-machine, execution, activity, version/alias, and tag operations run
// against an in-memory Step Functions driver.
//
// Step Functions uses the AWS JSON 1.0 wire shape (POST + JSON body dispatched
// on the X-Amz-Target header, prefix "AWSStepFunctions.").
package sfn

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	sfndriver "github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

const targetPrefix = "AWSStepFunctions."

// Handler serves Step Functions JSON-RPC requests against an SFN driver.
type Handler struct {
	sfn    sfndriver.SFN
	routes map[string]http.HandlerFunc
}

// New returns a Step Functions handler backed by d.
func New(d sfndriver.SFN) *Handler {
	h := &Handler{sfn: d}
	h.routes = map[string]http.HandlerFunc{
		"CreateStateMachine":               h.createStateMachine,
		"DescribeStateMachine":             h.describeStateMachine,
		"UpdateStateMachine":               h.updateStateMachine,
		"DeleteStateMachine":               h.deleteStateMachine,
		"ListStateMachines":                h.listStateMachines,
		"StartExecution":                   h.startExecution,
		"StartSyncExecution":               h.startSyncExecution,
		"DescribeExecution":                h.describeExecution,
		"StopExecution":                    h.stopExecution,
		"ListExecutions":                   h.listExecutions,
		"GetExecutionHistory":              h.getExecutionHistory,
		"DescribeStateMachineForExecution": h.describeStateMachineForExecution,
		"PublishStateMachineVersion":       h.publishStateMachineVersion,
		"ListStateMachineVersions":         h.listStateMachineVersions,
		"DeleteStateMachineVersion":        h.deleteStateMachineVersion,
		"CreateStateMachineAlias":          h.createStateMachineAlias,
		"DescribeStateMachineAlias":        h.describeStateMachineAlias,
		"UpdateStateMachineAlias":          h.updateStateMachineAlias,
		"DeleteStateMachineAlias":          h.deleteStateMachineAlias,
		"ListStateMachineAliases":          h.listStateMachineAliases,
		"CreateActivity":                   h.createActivity,
		"DescribeActivity":                 h.describeActivity,
		"DeleteActivity":                   h.deleteActivity,
		"ListActivities":                   h.listActivities,
		"GetActivityTask":                  h.getActivityTask,
		"SendTaskSuccess":                  h.sendTaskSuccess,
		"SendTaskFailure":                  h.sendTaskFailure,
		"SendTaskHeartbeat":                h.sendTaskHeartbeat,
		"TagResource":                      h.tagResource,
		"UntagResource":                    h.untagResource,
		"ListTagsForResource":              h.listTagsForResource,
	}

	return h
}

// Matches returns true for Step Functions-shaped requests (X-Amz-Target of
// "AWSStepFunctions.<Operation>").
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches Step Functions operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	if fn, ok := h.routes[op]; ok {
		fn(w, r)

		return
	}

	wire.WriteJSONError(w, http.StatusBadRequest, "UnknownOperationException",
		"unsupported Step Functions operation: "+r.Header.Get("X-Amz-Target"))
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

// writeErr maps a driver error to the closest Step Functions JSON error type.
// Errors tagged with a specific SFN exception (via driver.APIError) take
// precedence so distinct exceptions like StateMachineDoesNotExist /
// ExecutionAlreadyExists / InvalidArn surface as themselves.
func writeErr(w http.ResponseWriter, err error) {
	var apiErr *sfndriver.APIError
	if errors.As(err, &apiErr) {
		wire.WriteJSONError(w, http.StatusBadRequest, apiErr.Exception, err.Error())

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ConflictException", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", err.Error())
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ConflictException", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
