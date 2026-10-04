package sqs

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

// queueRef says where an operation names its queue.
type queueRef int

const (
	refQueueURL   queueRef = iota // QueueUrl
	refQueueName                  // QueueName (CreateQueue, GetQueueUrl)
	refSourceArn                  // SourceArn (message move tasks)
	refTaskHandle                 // TaskHandle, which carries the source ARN
	refNoResource                 // account-level, Resource "*"
)

// queueOps maps every operation ServeHTTP dispatches to where it names its
// queue, as listed under "Actions defined by Amazon SQS". The IAM action is
// the operation name, except that a batch operation has no action of its
// own: AWS authorizes SendMessageBatch as sqs:SendMessage, and so on.
//
//nolint:gochecknoglobals,goconst // static lookup table of the operation names the dispatch switch lists
var queueOps = map[string]queueRef{
	"CreateQueue":                  refQueueName,
	"GetQueueUrl":                  refQueueName,
	"ListQueues":                   refNoResource,
	"DeleteQueue":                  refQueueURL,
	"SendMessage":                  refQueueURL,
	"SendMessageBatch":             refQueueURL,
	"ReceiveMessage":               refQueueURL,
	"DeleteMessage":                refQueueURL,
	"DeleteMessageBatch":           refQueueURL,
	"ChangeMessageVisibility":      refQueueURL,
	"ChangeMessageVisibilityBatch": refQueueURL,
	"ListDeadLetterSourceQueues":   refQueueURL,
	"GetQueueAttributes":           refQueueURL,
	"SetQueueAttributes":           refQueueURL,
	"PurgeQueue":                   refQueueURL,
	"TagQueue":                     refQueueURL,
	"UntagQueue":                   refQueueURL,
	"ListQueueTags":                refQueueURL,
	"AddPermission":                refQueueURL,
	"RemovePermission":             refQueueURL,
	"StartMessageMoveTask":         refSourceArn,
	"CancelMessageMoveTask":        refTaskHandle,
	"ListMessageMoveTasks":         refSourceArn,
}

// arnFields is the field count of an SQS queue ARN
// (arn:partition:sqs:region:account:name).
const arnFields = 6

// queueName is the shape of an SQS queue name.
var queueName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}(\.fifo)?$`)

// IAMChecks names the IAM action and queue of a request from the
// X-Amz-Target ServeHTTP dispatches on. The queue ARN is built in the
// server's account and region from the queue name the request carries; a
// request that names no well-formed queue is evaluated on an unknown
// resource. An operation the handler does not serve returns ok=false, and
// ServeHTTP answers it with UnknownOperationException.
func (*Handler) IAMChecks(r *http.Request, s awsauthz.Scope) ([]awsauthz.Check, bool) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	ref, ok := queueOps[op]
	if !ok {
		return nil, false
	}

	action := iamService + ":" + strings.TrimSuffix(op, "Batch")
	if ref == refNoResource {
		return awsauthz.Single(action, "*"), true
	}

	var req struct {
		QueueURL   string `json:"QueueUrl"`
		QueueName  string `json:"QueueName"`
		SourceArn  string `json:"SourceArn"`
		TaskHandle string `json:"TaskHandle"`
	}

	if !awsauthz.JSONBody(r, &req) {
		return awsauthz.Single(action, ""), true
	}

	var name string

	switch ref {
	case refQueueURL:
		name = lastField(req.QueueURL, "/")
	case refQueueName:
		name = req.QueueName
	case refSourceArn:
		name = queueFromARN(req.SourceArn)
	case refTaskHandle:
		name = queueFromARN(taskSource(req.TaskHandle))
	case refNoResource:
	}

	if !queueName.MatchString(name) {
		return awsauthz.Single(action, ""), true
	}

	return awsauthz.Single(action, s.ARN(iamService, name)), true
}

// lastField is the part of s after the last sep.
func lastField(s, sep string) string {
	return s[strings.LastIndex(s, sep)+1:]
}

// queueFromARN is the queue name of an SQS queue ARN, or "".
func queueFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) != arnFields || parts[0] != "arn" || parts[2] != iamService {
		return ""
	}

	return parts[arnFields-1]
}

// taskSource is the source queue ARN a message move task handle carries
// (base64 JSON, as the provider issues it), or "".
func taskSource(handle string) string {
	raw, err := base64.StdEncoding.DecodeString(handle)
	if err != nil {
		return ""
	}

	var h struct {
		SourceArn string `json:"sourceArn"`
	}

	if json.Unmarshal(raw, &h) != nil {
		return ""
	}

	return h.SourceArn
}
