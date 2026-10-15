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

// moveTaskActions are the further actions a message move task operation
// needs on its source (dead-letter) queue, from "Configuring queue
// permissions for dead-letter queue redrive" in the SQS Developer Guide.
//
//nolint:gochecknoglobals // static lookup table
var moveTaskActions = map[string][]string{
	"StartMessageMoveTask":  {"ReceiveMessage", "DeleteMessage", "GetQueueAttributes"},
	"CancelMessageMoveTask": {"ReceiveMessage", "DeleteMessage", "GetQueueAttributes"},
	"ListMessageMoveTasks":  {"GetQueueAttributes"},
}

// queueRequest is the part of a request body IAMChecks reads.
type queueRequest struct {
	QueueURL       string `json:"QueueUrl"`
	QueueName      string `json:"QueueName"`
	SourceArn      string `json:"SourceArn"`
	DestinationArn string `json:"DestinationArn"`
	TaskHandle     string `json:"TaskHandle"`
}

// IAMChecks names the IAM checks of a request from the X-Amz-Target
// ServeHTTP dispatches on. The queue ARN is built in the server's account and
// region from the queue name the request carries; a request that names no
// well-formed queue is evaluated on an unknown resource. A message move task
// operation also needs the redrive actions on its source queue, and
// StartMessageMoveTask needs sqs:SendMessage on the destination: on
// DestinationArn, or on an unknown resource when it is empty and the
// messages go back to their original source queues. An operation the
// handler does not serve returns ok=false, and ServeHTTP answers it with
// UnknownOperationException.
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

	var req queueRequest

	decoded := awsauthz.JSONBody(r, &req)

	queue, dest := "", ""
	if decoded {
		queue = queueARN(s, requestQueue(ref, &req))
		dest = queueARN(s, queueFromARN(req.DestinationArn))
	}

	checks := awsauthz.Single(action, queue)
	for _, extra := range moveTaskActions[op] {
		checks = append(checks, awsauthz.Check{Action: iamService + ":" + extra, Resource: queue})
	}

	if op == "StartMessageMoveTask" {
		checks = append(checks, awsauthz.Check{Action: iamService + ":SendMessage", Resource: dest})
	}

	return checks, true
}

// requestQueue is the queue name a request names in the field ref points at.
func requestQueue(ref queueRef, req *queueRequest) string {
	switch ref {
	case refQueueURL:
		return lastField(req.QueueURL, "/")
	case refQueueName:
		return req.QueueName
	case refSourceArn:
		return queueFromARN(req.SourceArn)
	case refTaskHandle:
		return queueFromARN(taskSource(req.TaskHandle))
	case refNoResource:
	}

	return ""
}

// queueARN is the ARN of queue name in the server's account and region, or
// "" (unknown) when name is not a queue name.
func queueARN(s awsauthz.Scope, name string) string {
	if !queueName.MatchString(name) {
		return ""
	}

	return s.ARN(iamService, name)
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
