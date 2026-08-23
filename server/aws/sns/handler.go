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
//	ConfirmSubscription         — snsExtras.ConfirmSubscription
//	GetSubscriptionAttributes   — snsExtras.GetSubscription
//	SetSubscriptionAttributes   — snsExtras.SetSubscriptionAttribute
//	ListSubscriptions           — Notification.ListSubscriptions across all topics
//	ListSubscriptionsByTopic    — Notification.ListSubscriptions for one topic
//	Publish                     — Notification.Publish
//	PublishBatch                — Notification.Publish per entry
//	AddPermission               — snsExtras.AddTopicPermission
//	RemovePermission            — snsExtras.RemoveTopicPermission
package sns

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// Namespace is the XML namespace for AWS SNS responses.
const Namespace = "http://sns.amazonaws.com/doc/2010-03-31/"

const (
	formContentType  = "application/x-www-form-urlencoded"
	maxFormBodyBytes = 1 << 20
)

// SNS attribute-value literals and the pending subscription state.
const (
	statusPending = "pending"
	attrTrue      = "true"
	attrFalse     = "false"
)

// snsActions is the set of Action values this handler recognizes. Matches uses
// it to decide whether to claim a request. Disjoint from RDS / Redshift / IAM /
// EC2 / ElastiCache action sets.
var snsActions = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"CreateTopic":               {},
	"DeleteTopic":               {},
	"GetTopicAttributes":        {},
	"SetTopicAttributes":        {},
	"ListTopics":                {},
	"Subscribe":                 {},
	"Unsubscribe":               {},
	"ConfirmSubscription":       {},
	"GetSubscriptionAttributes": {},
	"SetSubscriptionAttributes": {},
	"ListSubscriptions":         {},
	"ListSubscriptionsByTopic":  {},
	"Publish":                   {},
	"PublishBatch":              {},
	"AddPermission":             {},
	"RemovePermission":          {},
	"TagResource":               {},
	"UntagResource":             {},
	actionListTagsForResource:   {},
}

// actionListTagsForResource is the generic tag-read verb SNS shares with other
// query-protocol services; it's scope-gated in Matches.
const actionListTagsForResource = "ListTagsForResource"

// topicTagger is the AWS-specific topic-tagging surface. It's not part of the
// portable Notification driver (Azure Notification Hubs and GCP FCM also
// implement it), so the handler type-asserts for it.
//
// ListTagsForResource collides with RDS in the shared query protocol (RDS
// registers first), so both handlers claim it only for their own SigV4
// credential scope — see Matches.
type topicTagger interface {
	TagTopic(ctx context.Context, topicName string, tags map[string]string) error
	UntagTopic(ctx context.Context, topicName string, keys []string) error
}

// snsExtras is the AWS-only surface (subscription attributes, confirmation, and
// topic access-policy permissions) that isn't part of the portable Notification
// driver — Azure Notification Hubs and GCP FCM don't model it. The AWS SNS mock
// implements it; handlers type-assert for it and return NotSupported otherwise.
type snsExtras interface {
	GetSubscription(ctx context.Context, subscriptionARN string) (*notifdriver.SubscriptionInfo, error)
	SetSubscriptionAttribute(ctx context.Context, subscriptionARN, name, value string) error
	ConfirmSubscription(ctx context.Context, topicName, token string) (*notifdriver.SubscriptionInfo, error)
	AddTopicPermission(ctx context.Context, topicName, label string, accountIDs, actions []string) error
	RemoveTopicPermission(ctx context.Context, topicName, label string) error
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

	action := r.Form.Get("Action")

	_, ok := snsActions[action]
	if !ok {
		return false
	}

	// ListTagsForResource is a generic tag verb SNS shares with RDS on the same
	// query wire (RDS registers first). Claim it only when the SigV4 credential
	// scope names "sns"; otherwise let it fall through.
	if action == actionListTagsForResource {
		return awsquery.CredentialScopeService(r.Header.Get("Authorization")) == "sns"
	}

	return true
}

// ServeHTTP dispatches on Action. The form has already been parsed by Matches.
//
//nolint:gocyclo // flat dispatch switch over the SNS action set
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action := r.Form.Get("Action")

	switch action {
	case "CreateTopic":
		h.createTopic(w, r)
	case "DeleteTopic":
		h.deleteTopic(w, r)
	case "GetTopicAttributes":
		h.getTopicAttributes(w, r)
	case "SetTopicAttributes":
		h.setTopicAttributes(w, r)
	case "ListTopics":
		h.listTopics(w, r)
	case "Subscribe":
		h.subscribe(w, r)
	case "Unsubscribe":
		h.unsubscribe(w, r)
	case "ConfirmSubscription":
		h.confirmSubscription(w, r)
	case "GetSubscriptionAttributes":
		h.getSubscriptionAttributes(w, r)
	case "SetSubscriptionAttributes":
		h.setSubscriptionAttributes(w, r)
	case "ListSubscriptions":
		h.listSubscriptions(w, r)
	case "ListSubscriptionsByTopic":
		h.listSubscriptionsByTopic(w, r)
	case "Publish":
		h.publish(w, r)
	case "PublishBatch":
		h.publishBatch(w, r)
	case "AddPermission":
		h.addPermission(w, r)
	case "RemovePermission":
		h.removePermission(w, r)
	case "TagResource":
		h.tagResource(w, r)
	case "UntagResource":
		h.untagResource(w, r)
	case actionListTagsForResource:
		h.listTagsForResource(w, r)
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

// accountFromARN pulls the account id (field 4) out of an ARN
// arn:aws:sns:region:account:name, or "" when the shape doesn't match.
func accountFromARN(arn string) string {
	const accountField = 4

	parts := strings.Split(arn, ":")
	if len(parts) > accountField {
		return parts[accountField]
	}

	return ""
}

// defaultEffectiveDeliveryPolicy is the delivery policy SNS reports for a topic
// that hasn't overridden the account defaults.
const defaultEffectiveDeliveryPolicy = `{"http":{"defaultHealthyRetryPolicy":{"minDelayTarget":20,` +
	`"maxDelayTarget":20,"numRetries":3,"numMaxDelayRetries":0,"numNoDelayRetries":0,` +
	`"numMinDelayRetries":0,"backoffFunction":"linear"},"disableSubscriptionOverrides":false}}`

// defaultTopicPolicy mirrors the access policy real SNS attaches to a new topic,
// so a client that reads Policy back gets valid JSON.
func defaultTopicPolicy(arn, owner string) string {
	return `{"Version":"2008-10-17","Id":"__default_policy_ID","Statement":[{` +
		`"Sid":"__default_statement_ID","Effect":"Allow","Principal":{"AWS":"*"},` +
		`"Action":["SNS:GetTopicAttributes","SNS:SetTopicAttributes","SNS:AddPermission",` +
		`"SNS:RemovePermission","SNS:DeleteTopic","SNS:Subscribe","SNS:ListSubscriptionsByTopic",` +
		`"SNS:Publish","SNS:Receive"],"Resource":"` + arn + `",` +
		`"Condition":{"StringEquals":{"AWS:SourceOwner":"` + owner + `"}}}]}`
}
