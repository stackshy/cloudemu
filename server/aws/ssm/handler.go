// Package ssm implements the AWS Systems Manager (SSM) Parameter Store
// JSON-RPC protocol as a server.Handler. Point the real aws-sdk-go-v2 SSM
// client at a Server registered with this handler and Parameter Store
// operations work against an in-memory parameterstore driver.
//
// SSM uses the AWS JSON 1.1 wire shape (POST + JSON body, dispatched on the
// X-Amz-Target header "AmazonSSM.<Operation>"), the same family as DynamoDB,
// SQS, EventBridge, CloudWatch Logs, ECR, SageMaker, and Secrets Manager. The
// "AmazonSSM." target prefix is disjoint from all of those, so registration
// order relative to them is unconstrained.
package ssm

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	ssmdriver "github.com/stackshy/cloudemu/parameterstore/driver"
	"github.com/stackshy/cloudemu/server/wire"
)

const targetPrefix = "AmazonSSM."

// Handler serves Parameter Store JSON-RPC requests against a ParameterStore driver.
type Handler struct {
	store ssmdriver.ParameterStore
}

// New returns a Parameter Store handler backed by s.
func New(s ssmdriver.ParameterStore) *Handler {
	return &Handler{store: s}
}

// Matches returns true for SSM-shaped requests, identified by an X-Amz-Target
// header of "AmazonSSM.<Operation>".
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches Parameter Store operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	switch op {
	case "PutParameter":
		h.putParameter(w, r)
	case "GetParameter":
		h.getParameter(w, r)
	case "GetParameters":
		h.getParameters(w, r)
	case "GetParametersByPath":
		h.getParametersByPath(w, r)
	case "DeleteParameter":
		h.deleteParameter(w, r)
	case "DeleteParameters":
		h.deleteParameters(w, r)
	case "DescribeParameters":
		h.describeParameters(w, r)
	case "GetParameterHistory":
		h.getParameterHistory(w, r)
	case "LabelParameterVersion":
		h.labelParameterVersion(w, r)
	default:
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnknownOperationException", "unknown SSM operation: "+op)
	}
}

// writeErr maps canonical cloudemu errors to SSM JSON error responses. SSM
// returns errors as HTTP 400 with a "__type" body the SDK maps to a typed
// exception.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ParameterNotFound", err.Error())
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ParameterAlreadyExists", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", err.Error())
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		wire.WriteJSONError(w, http.StatusBadRequest, "ParameterLimitExceeded", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServerError", err.Error())
	}
}
