// Package cloudwatchlogs implements the AWS CloudWatch Logs JSON-RPC protocol
// as a server.Handler. Point the real aws-sdk-go-v2 CloudWatch Logs client at a
// Server registered with this handler and log-group, log-stream, and log-event
// operations work against the shared logging driver.
//
// CloudWatch Logs uses the AWS JSON 1.1 wire shape (POST + JSON body,
// dispatched on the X-Amz-Target header), the same family as DynamoDB, SQS, and
// Secrets Manager. Its target prefix is "Logs_20140328." — disjoint from every
// other JSON-RPC AWS handler, so registration order relative to them is
// unconstrained.
//
// Coverage (Logs_20140328):
//
//	CreateLogGroup      DescribeLogGroups   DeleteLogGroup
//	CreateLogStream     DescribeLogStreams  DeleteLogStream
//	PutLogEvents        GetLogEvents        FilterLogEvents
package cloudwatchlogs

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// targetPrefix roots every CloudWatch Logs X-Amz-Target value. The API version
// segment (20140328) is fixed.
const targetPrefix = "Logs_20140328."

// Handler serves CloudWatch Logs JSON-RPC requests against a logging driver.
type Handler struct {
	logs logdriver.Logging
}

// New returns a CloudWatch Logs handler backed by l.
func New(l logdriver.Logging) *Handler {
	return &Handler{logs: l}
}

// Matches returns true for CloudWatch Logs-shaped requests, identified by an
// X-Amz-Target header of "Logs_20140328.<Operation>". The prefix is disjoint
// from DynamoDB (DynamoDB_20120810.), SQS (AmazonSQS.), Secrets Manager
// (secretsmanager.), and SageMaker (SageMaker.), so no shadowing occurs.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches CloudWatch Logs operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	switch op {
	case "CreateLogGroup":
		h.createLogGroup(w, r)
	case "DescribeLogGroups":
		h.describeLogGroups(w, r)
	case "DeleteLogGroup":
		h.deleteLogGroup(w, r)
	case "CreateLogStream":
		h.createLogStream(w, r)
	case "DescribeLogStreams":
		h.describeLogStreams(w, r)
	case "DeleteLogStream":
		h.deleteLogStream(w, r)
	case "PutLogEvents":
		h.putLogEvents(w, r)
	case "GetLogEvents":
		h.getLogEvents(w, r)
	case "FilterLogEvents":
		h.filterLogEvents(w, r)
	default:
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnknownOperationException", "unknown CloudWatch Logs operation: "+op)
	}
}

// writeErr maps canonical cloudemu errors to CloudWatch Logs JSON error
// responses. Like the other AWS JSON 1.1 services, errors are HTTP 400 with a
// "__type" body the SDK maps to a typed exception.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", err.Error())
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceAlreadyExistsException", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", err.Error())
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidOperationException", err.Error())
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		wire.WriteJSONError(w, http.StatusBadRequest, "LimitExceededException", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "ServiceUnavailableException", err.Error())
	}
}
