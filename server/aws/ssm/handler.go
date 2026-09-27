// Package ssm implements the AWS Systems Manager (SSM) JSON-RPC protocol as a
// server.Handler: Parameter Store, Run Command, Documents and tagging. Point
// the real aws-sdk-go-v2 SSM client at a Server registered with this handler
// and those operations work against the in-memory drivers.
//
// SSM uses the AWS JSON 1.1 wire shape (POST + JSON body, dispatched on the
// X-Amz-Target header "AmazonSSM.<Operation>"), the same family as DynamoDB,
// SQS, EventBridge, CloudWatch Logs, ECR, SageMaker, and Secrets Manager. The
// "AmazonSSM." target prefix is disjoint from all of those, so registration
// order relative to them is unconstrained.
package ssm

import (
	stderrors "errors"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	ssmdriver "github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

const targetPrefix = "AmazonSSM."

// handlerFunc serves one SSM operation.
type handlerFunc func(h *Handler, w http.ResponseWriter, r *http.Request)

// Handler serves SSM JSON-RPC requests. Parameter Store runs against the
// portable ParameterStore driver. Run Command and Documents are optional
// capabilities the driver may also implement.
type Handler struct {
	store ssmdriver.ParameterStore
	ops   map[string]handlerFunc
}

// New returns an SSM handler backed by s.
func New(s ssmdriver.ParameterStore) *Handler {
	h := &Handler{store: s, ops: map[string]handlerFunc{}}

	for _, family := range []map[string]handlerFunc{parameterOps(), runCommandOps(), tagOps(), documentOps()} {
		for op, fn := range family {
			h.ops[op] = fn
		}
	}

	return h
}

// parameterOps is the Parameter Store family, service settings included.
func parameterOps() map[string]handlerFunc {
	return map[string]handlerFunc{
		"PutParameter":          (*Handler).putParameter,
		"GetParameter":          (*Handler).getParameter,
		"GetParameters":         (*Handler).getParameters,
		"GetParametersByPath":   (*Handler).getParametersByPath,
		"DeleteParameter":       (*Handler).deleteParameter,
		"DeleteParameters":      (*Handler).deleteParameters,
		"DescribeParameters":    (*Handler).describeParameters,
		"GetParameterHistory":   (*Handler).getParameterHistory,
		"LabelParameterVersion": (*Handler).labelParameterVersion,
		"GetServiceSetting":     (*Handler).getServiceSetting,
		"UpdateServiceSetting":  (*Handler).updateServiceSetting,
		"ResetServiceSetting":   (*Handler).resetServiceSetting,
	}
}

func runCommandOps() map[string]handlerFunc {
	return map[string]handlerFunc{
		"SendCommand":          (*Handler).sendCommand,
		"GetCommandInvocation": (*Handler).getCommandInvocation,
	}
}

// Matches returns true for SSM-shaped requests, identified by an X-Amz-Target
// header of "AmazonSSM.<Operation>".
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches SSM operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	fn, ok := h.ops[op]
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnknownOperationException", "unknown SSM operation: "+op)

		return
	}

	fn(h, w, r)
}

// writeErr maps canonical cloudemu errors to SSM JSON error responses. SSM
// returns errors as HTTP 400 with a "__type" body the SDK maps to a typed
// exception.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	// A provider error may name its exact SSM exception (InvalidDocument,
	// InvalidInstanceId, ...). The code mapping below only fits Parameter Store.
	var ex interface {
		SSMException() (string, int)
	}

	if stderrors.As(err, &ex) {
		name, status := ex.SSMException()
		wire.WriteJSONError(w, status, name, msg)

		return
	}

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ParameterNotFound", msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ParameterAlreadyExists", msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", msg)
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		wire.WriteJSONError(w, http.StatusBadRequest, "ParameterLimitExceeded", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServerError", msg)
	}
}
