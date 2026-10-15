package sns

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

// topicRef says where an operation names the topic it is authorized on.
type topicRef int

const (
	refNoTopic         topicRef = iota // account-level, Resource "*"
	refTopicName                       // Name (CreateTopic)
	refTopicARN                        // TopicArn
	refPublishTarget                   // TopicArn, else TargetArn
	refResourceARN                     // ResourceArn (tagging)
	refSubscriptionARN                 // SubscriptionArn: the subscription's topic
)

// topicName is the shape of an SNS topic name.
var topicName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}(\.fifo)?$`)

// subscriptionARNFields is the field count of a subscription ARN
// (arn:aws:sns:region:account:topic:id), whose topic is field 5.
const (
	subscriptionARNFields = 7
	subscriptionTopicIdx  = 5
)

// IAMChecks names the IAM action of a request from the form Action that
// ServeHTTP dispatches on, and the topic it runs on. The topic is the one the
// handler resolves (the last field of TopicArn, TargetArn or ResourceArn, or
// the topic field of a SubscriptionArn), in the server's account and region.
// PublishBatch is authorized as sns:Publish, the action AWS checks for it. A
// request that names no well-formed topic is evaluated on an unknown
// resource. An Action the handler does not know is authorized as such and
// then answered with InvalidAction, so nothing runs.
func (h *Handler) IAMChecks(r *http.Request, s awsauthz.Scope) ([]awsauthz.Check, bool) {
	checks, ok := awsauthz.QueryChecks(r, h.IAMService())
	if !ok {
		return nil, false
	}

	op := r.Form.Get("Action")

	ref, known := snsActions[op]
	if !known {
		return checks, true
	}

	checks[0].Action = h.IAMService() + ":" + strings.TrimSuffix(op, "Batch")
	checks[0].Resource = topicResource(r, ref, s)

	return checks, true
}

// topicResource is the topic ARN an operation runs on, "*" for an operation
// on no topic, or "" when the request names none.
func topicResource(r *http.Request, ref topicRef, s awsauthz.Scope) string {
	var name string

	switch ref {
	case refNoTopic:
		return "*"
	case refTopicName:
		name = r.Form.Get("Name")
	case refTopicARN:
		name = topicNameFromARN(r.Form.Get("TopicArn"))
	case refPublishTarget:
		name = topicNameFromARN(publishTarget(r))
	case refResourceARN:
		name = topicNameFromARN(r.Form.Get("ResourceArn"))
	case refSubscriptionARN:
		name = subscriptionTopic(r.Form.Get("SubscriptionArn"))
	}

	if !topicName.MatchString(name) {
		return ""
	}

	return s.ARN("sns", name)
}

// publishTarget is the ARN Publish and PublishBatch address, read as the
// handler reads it: TopicArn, else TargetArn.
func publishTarget(r *http.Request) string {
	if arn := r.Form.Get("TopicArn"); arn != "" {
		return arn
	}

	return r.Form.Get("TargetArn")
}

// subscriptionTopic is the topic field of a subscription ARN, or "".
func subscriptionTopic(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) != subscriptionARNFields {
		return ""
	}

	return parts[subscriptionTopicIdx]
}
