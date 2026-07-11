// Package sns implements the AWS SNS query-protocol as a server.Handler. Point
// the real aws-sdk-go-v2 SNS client at a Server registered with this handler
// and topic/subscription/publish operations work against an in-memory
// notification driver.
//
// SNS shares the AWS query wire shape with EC2, RDS, Redshift, IAM, and
// ElastiCache (POST + form-encoded body, XML response). To keep dispatch
// unambiguous, this handler's Matches predicate parses the form body once and
// only claims requests whose Action is one of the known SNS operations. The
// EC2 handler is the catch-all for all other query-protocol actions, so this
// handler MUST register before EC2.
//
// Coverage (query protocol):
//
//	CreateTopic                 — Notification.CreateTopic
//	DeleteTopic                 — Notification.DeleteTopic
//	GetTopicAttributes          — Notification.GetTopic
//	ListTopics                  — Notification.ListTopics
//	Subscribe                   — Notification.Subscribe
//	Unsubscribe                 — Notification.Unsubscribe
//	ListSubscriptions           — Notification.ListSubscriptions across all topics
//	ListSubscriptionsByTopic    — Notification.ListSubscriptions for one topic
//	Publish                     — Notification.Publish
package sns

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
	notifdriver "github.com/stackshy/cloudemu/notification/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// Namespace is the XML namespace for AWS SNS responses.
const Namespace = "http://sns.amazonaws.com/doc/2010-03-31/"

const (
	formContentType  = "application/x-www-form-urlencoded"
	maxFormBodyBytes = 1 << 20
)

// snsActions is the set of Action values this handler recognizes. Matches uses
// it to decide whether to claim a request. Disjoint from RDS / Redshift / IAM /
// EC2 / ElastiCache action sets.
var snsActions = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"CreateTopic":              {},
	"DeleteTopic":              {},
	"GetTopicAttributes":       {},
	"ListTopics":               {},
	"Subscribe":                {},
	"Unsubscribe":              {},
	"ListSubscriptions":        {},
	"ListSubscriptionsByTopic": {},
	"Publish":                  {},
}

// Handler serves SNS query-protocol requests against a notification driver.
type Handler struct {
	notif notifdriver.Notification
}

// New returns an SNS handler backed by n.
func New(n notifdriver.Notification) *Handler {
	return &Handler{notif: n}
}

// Matches returns true if the request looks like an AWS SNS query-protocol call
// (POST + form-encoded body whose Action is one of the known SNS operations).
// Calling ParseForm here caches the parsed form on the request so ServeHTTP can
// use it without re-reading the body.
func (*Handler) Matches(r *http.Request) bool {
	if r.Header.Get("X-Amz-Target") != "" {
		return false
	}

	if r.Method != http.MethodPost {
		return false
	}

	if !strings.HasPrefix(r.Header.Get("Content-Type"), formContentType) {
		return false
	}

	r.Body = http.MaxBytesReader(nil, r.Body, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		return false
	}

	_, ok := snsActions[r.Form.Get("Action")]

	return ok
}

// ServeHTTP dispatches on Action. The form has already been parsed by Matches.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action := r.Form.Get("Action")

	switch action {
	case "CreateTopic":
		h.createTopic(w, r)
	case "DeleteTopic":
		h.deleteTopic(w, r)
	case "GetTopicAttributes":
		h.getTopicAttributes(w, r)
	case "ListTopics":
		h.listTopics(w, r)
	case "Subscribe":
		h.subscribe(w, r)
	case "Unsubscribe":
		h.unsubscribe(w, r)
	case "ListSubscriptions":
		h.listSubscriptions(w, r)
	case "ListSubscriptionsByTopic":
		h.listSubscriptionsByTopic(w, r)
	case "Publish":
		h.publish(w, r)
	default:
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidAction", "unknown SNS action: "+action)
	}
}

// writeErr maps cloudemu errors to SNS XML error responses. SNS uses a small
// set of error codes; the SDK maps NotFound → NotFoundException and
// InvalidParameter → InvalidParameterException.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		awsquery.WriteXMLError(w, http.StatusNotFound, "NotFound", err.Error())
	case cerrors.IsInvalidArgument(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameter", err.Error())
	case cerrors.IsAlreadyExists(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameter", err.Error())
	default:
		awsquery.WriteXMLError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}

// topicNameFromARN extracts the SNS topic name (the last colon-delimited
// segment) from a topic ARN. The notification driver keys topics by name, so
// every ARN-addressed operation resolves the name first. A value without any
// colon is returned unchanged so a bare name still works.
func topicNameFromARN(arn string) string {
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		return arn[i+1:]
	}

	return arn
}
