// Package cloudwatchlogs implements the AWS CloudWatch Logs JSON-RPC protocol
// as a server.Handler. Point the real aws-sdk-go-v2 CloudWatch Logs client at a
// Server registered with this handler and log-group, log-stream, and log-event
// operations work against the shared logging driver.
//
// CloudWatch Logs uses the AWS JSON 1.1 wire shape (POST + JSON body,
// dispatched on the X-Amz-Target header), the same family as DynamoDB, SQS, and
// Secrets Manager. Its target prefix "Logs_20140328." is disjoint from every
// other JSON-RPC AWS handler, so registration order relative to them is
// unconstrained.
//
// Coverage (Logs_20140328):
//
//	CreateLogGroup      DescribeLogGroups   DeleteLogGroup
//	CreateLogStream     DescribeLogStreams  DeleteLogStream
//	PutLogEvents        GetLogEvents        FilterLogEvents
//	PutMetricFilter     DescribeMetricFilters DeleteMetricFilter
//	PutSubscriptionFilter DescribeSubscriptionFilters DeleteSubscriptionFilter
//	PutRetentionPolicy  DeleteRetentionPolicy ListLogGroups
//	AssociateKmsKey     DisassociateKmsKey  TestMetricFilter
//	StartQuery GetQueryResults StopQuery DescribeQueries GetLogRecord GetLogGroupFields
//	CreateExportTask    DescribeExportTasks CancelExportTask
//	PutDataProtectionPolicy GetDataProtectionPolicy DeleteDataProtectionPolicy
//	PutAccountPolicy    DescribeAccountPolicies DeleteAccountPolicy
//	StartLiveTail (an event stream; aws-sdk-go-v2 sends it to a stream- host prefix
//	and botocore to streaming-, and dispatch is by X-Amz-Target, so either
//	prefixed host needs no extra route)
//	TagResource / UntagResource / ListTagsForResource (and the legacy *LogGroup forms)
//
// CloudWatch Logs-only operations go through the optional driver.AWSLogging
// interface, which the AWS backend implements.
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

// ServeHTTP dispatches CloudWatch Logs operations based on X-Amz-Target. Core
// log-group / log-stream / log-event operations are handled here; retention,
// metric-filter, and tagging operations route through dispatchManagement to
// keep either switch within the complexity budget.
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
		h.dispatchManagement(w, r, op)
	}
}

// dispatchManagement handles the metric-filter and subscription-filter
// operations, delegating the log-group settings, tagging, and the
// unknown-operation error to dispatchGroupSettings.
func (h *Handler) dispatchManagement(w http.ResponseWriter, r *http.Request, op string) {
	switch op {
	case "PutMetricFilter":
		h.putMetricFilter(w, r)
	case "DescribeMetricFilters":
		h.describeMetricFilters(w, r)
	case "DeleteMetricFilter":
		h.deleteMetricFilter(w, r)
	case "TestMetricFilter":
		h.testMetricFilter(w, r)
	case "PutSubscriptionFilter":
		h.putSubscriptionFilter(w, r)
	case "DescribeSubscriptionFilters":
		h.describeSubscriptionFilters(w, r)
	case "DeleteSubscriptionFilter":
		h.deleteSubscriptionFilter(w, r)
	default:
		h.dispatchGroupSettings(w, r, op)
	}
}

// dispatchGroupSettings handles retention, ListLogGroups, and KMS key
// association, delegating tagging to dispatchTags.
func (h *Handler) dispatchGroupSettings(w http.ResponseWriter, r *http.Request, op string) {
	switch op {
	case "PutRetentionPolicy":
		h.putRetentionPolicy(w, r)
	case "DeleteRetentionPolicy":
		h.deleteRetentionPolicy(w, r)
	case "ListLogGroups":
		h.listLogGroups(w, r)
	case "AssociateKmsKey":
		h.associateKmsKey(w, r)
	case "DisassociateKmsKey":
		h.disassociateKmsKey(w, r)
	default:
		h.dispatchInsights(w, r, op)
	}
}

// dispatchInsights handles the Logs Insights operations, delegating export
// tasks and tagging to dispatchExport.
func (h *Handler) dispatchInsights(w http.ResponseWriter, r *http.Request, op string) {
	switch op {
	case "StartQuery":
		h.startQuery(w, r)
	case "GetQueryResults":
		h.getQueryResults(w, r)
	case "StopQuery":
		h.stopQuery(w, r)
	case "DescribeQueries":
		h.describeQueries(w, r)
	case "GetLogRecord":
		h.getLogRecord(w, r)
	case "GetLogGroupFields":
		h.getLogGroupFields(w, r)
	default:
		h.dispatchExport(w, r, op)
	}
}

// dispatchTags handles the tagging operations, reporting the unknown-operation
// error for anything unmatched. Split out of dispatchManagement to keep each
// switch within the cyclomatic-complexity budget.
func (h *Handler) dispatchTags(w http.ResponseWriter, r *http.Request, op string) {
	switch op {
	case "TagResource", "TagLogGroup":
		h.tagLogGroup(w, r)
	case "UntagResource", "UntagLogGroup":
		h.untagLogGroup(w, r)
	case "ListTagsForResource", "ListTagsLogGroup":
		h.listTagsForResource(w, r)
	default:
		writeUnknownOperation(w, op)
	}
}

func writeUnknownOperation(w http.ResponseWriter, op string) {
	wire.WriteJSONError(w, http.StatusBadRequest,
		"UnknownOperationException", "unknown CloudWatch Logs operation: "+op)
}

// awsLogs returns the CloudWatch Logs-only driver surface. When the backend
// does not implement it, the operation is answered as unknown, which is what
// a backend without the operation would do.
func (h *Handler) awsLogs(w http.ResponseWriter, op string) (logdriver.AWSLogging, bool) {
	ext, ok := h.logs.(logdriver.AWSLogging)
	if !ok {
		writeUnknownOperation(w, op)
	}

	return ext, ok
}

// writeErr maps canonical cloudemu errors to CloudWatch Logs JSON error
// responses. Like the other AWS JSON 1.1 services, errors are HTTP 400 with a
// "__type" body the SDK maps to a typed exception.
func writeErr(w http.ResponseWriter, err error) {
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceAlreadyExistsException", msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException", msg)
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidOperationException", msg)
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		wire.WriteJSONError(w, http.StatusBadRequest, "LimitExceededException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "ServiceUnavailableException", msg)
	}
}

// IAMService returns the IAM service prefix of the operations this handler
// serves.
func (*Handler) IAMService() string { return "logs" }
