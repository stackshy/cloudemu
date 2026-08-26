// Package eventbridge implements the AWS EventBridge JSON-RPC protocol as a
// server.Handler. Point the real aws-sdk-go-v2 EventBridge client at a Server
// registered with this handler and event-bus, rule, target, and event
// operations work against an in-memory eventbus driver.
//
// EventBridge uses the AWS JSON 1.1 wire shape (POST + JSON body, dispatched on
// the X-Amz-Target header), the same family as DynamoDB, SQS, and Secrets
// Manager. Its target prefix ("AWSEvents.") is disjoint from every other JSON
// handler, so registration order among them is unconstrained.
package eventbridge

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

const targetPrefix = "AWSEvents."

// Handler serves EventBridge JSON-RPC requests against an EventBus driver.
type Handler struct {
	bus       ebdriver.EventBus
	accountID string
	region    string
}

// New returns an EventBridge handler backed by b. accountID and region are used
// to synthesize well-formed rule ARNs.
func New(b ebdriver.EventBus, accountID, region string) *Handler {
	return &Handler{bus: b, accountID: accountID, region: region}
}

// Matches returns true for EventBridge-shaped requests, identified by an
// X-Amz-Target header of "AWSEvents.<Operation>".
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches EventBridge operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	switch op {
	case "CreateEventBus":
		h.createEventBus(w, r)
	case "DescribeEventBus":
		h.describeEventBus(w, r)
	case "ListEventBuses":
		h.listEventBuses(w, r)
	case "DeleteEventBus":
		h.deleteEventBus(w, r)
	case "PutRule":
		h.putRule(w, r)
	case "DescribeRule":
		h.describeRule(w, r)
	case "ListRules":
		h.listRules(w, r)
	case "DeleteRule":
		h.deleteRule(w, r)
	case "EnableRule":
		h.enableRule(w, r)
	case "DisableRule":
		h.disableRule(w, r)
	case "PutTargets":
		h.putTargets(w, r)
	case "RemoveTargets":
		h.removeTargets(w, r)
	case "ListTargetsByRule":
		h.listTargetsByRule(w, r)
	case "ListRuleNamesByTarget":
		h.listRuleNamesByTarget(w, r)
	case "PutPermission":
		h.putPermission(w, r)
	case "RemovePermission":
		h.removePermission(w, r)
	case "PutEvents":
		h.putEvents(w, r)
	case "TestEventPattern":
		h.testEventPattern(w, r)
	case "TagResource":
		h.tagResource(w, r)
	case "UntagResource":
		h.untagResource(w, r)
	case "ListTagsForResource":
		h.listTagsForResource(w, r)
	default:
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnknownOperationException", "unknown EventBridge operation: "+op)
	}
}

// writeErr maps canonical cloudemu errors to EventBridge JSON error responses.
// EventBridge returns errors as HTTP 400 with a "__type" body the SDK maps to a
// typed exception.
func writeErr(w http.ResponseWriter, err error) {
	// Message strips the internal "<Code>: " prefix Error() prepends, so the wire
	// message carries only the human text — real AWS never leaks a taxonomy name,
	// and the deliberately-verbatim DeleteRule wording must arrive intact.
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", msg)
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceAlreadyExistsException", msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", msg)
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidStateException", msg)
	case cerrors.GetCode(err) == cerrors.ResourceExhausted:
		wire.WriteJSONError(w, http.StatusBadRequest, "LimitExceededException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalException", msg)
	}
}
