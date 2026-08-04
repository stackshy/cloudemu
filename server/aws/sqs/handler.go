// Package sqs implements the AWS SQS JSON-RPC protocol as a server.Handler.
// Modern aws-sdk-go-v2 SQS uses AwsJson1_0 with X-Amz-Target headers (since
// SQS migrated off the legacy Query protocol in 2023).
//
// Coverage: queue lifecycle, the synchronous send/receive/delete loop,
// queue attributes (GetQueueAttributes exposes the QueueArn that event-source
// mappings, DLQ wiring, and S3->SQS notifications depend on), and PurgeQueue.
// Batch ops and ChangeMessageVisibility remain deferred — the portable
// messagequeue.MessageQueue driver supports them.
package sqs

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

const targetPrefix = "AmazonSQS."

// Handler serves SQS JSON-RPC requests against a messagequeue.MessageQueue
// driver.
type Handler struct {
	mq mqdriver.MessageQueue
}

// New returns an SQS handler backed by mq.
func New(mq mqdriver.MessageQueue) *Handler {
	return &Handler{mq: mq}
}

// Matches identifies SQS requests by their X-Amz-Target header. SQS shares
// the same content-type as DynamoDB (application/x-amz-json-1.0) so the
// header prefix is the only reliable discriminator.
func (*Handler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
}

// ServeHTTP dispatches SQS operations based on X-Amz-Target.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)

	switch op {
	case "CreateQueue":
		h.createQueue(w, r)
	case "GetQueueUrl":
		h.getQueueURL(w, r)
	case "ListQueues":
		h.listQueues(w, r)
	case "DeleteQueue":
		h.deleteQueue(w, r)
	case "SendMessage":
		h.sendMessage(w, r)
	case "ReceiveMessage":
		h.receiveMessage(w, r)
	case "DeleteMessage":
		h.deleteMessage(w, r)
	case "GetQueueAttributes":
		h.getQueueAttributes(w, r)
	case "SetQueueAttributes":
		h.setQueueAttributes(w, r)
	case "PurgeQueue":
		h.purgeQueue(w, r)
	case "TagQueue":
		h.tagQueue(w, r)
	case "UntagQueue":
		h.untagQueue(w, r)
	case "ListQueueTags":
		h.listQueueTags(w, r)
	default:
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnknownOperationException", "unknown operation: "+op)
	}
}

func (h *Handler) createQueue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueName  string            `json:"QueueName"`
		Attributes map[string]string `json:"Attributes"`
		Tags       map[string]string `json:"tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cfg := mqdriver.QueueConfig{
		Name: req.QueueName,
		FIFO: req.Attributes["FifoQueue"] == "true" || strings.HasSuffix(req.QueueName, ".fifo"),
		Tags: req.Tags,
	}

	info, err := h.mq.CreateQueue(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"QueueUrl": info.URL})
}

func (h *Handler) getQueueURL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueName string `json:"QueueName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	queues, err := h.mq.ListQueues(r.Context(), "")
	if err != nil {
		writeErr(w, err)
		return
	}

	for i := range queues {
		if queues[i].Name == req.QueueName {
			wire.WriteJSON(w, map[string]any{"QueueUrl": queues[i].URL})
			return
		}
	}

	wire.WriteJSONError(w, http.StatusBadRequest,
		"QueueDoesNotExist", "queue not found: "+req.QueueName)
}

func (h *Handler) listQueues(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueNamePrefix string `json:"QueueNamePrefix"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	queues, err := h.mq.ListQueues(r.Context(), req.QueueNamePrefix)
	if err != nil {
		writeErr(w, err)
		return
	}

	urls := make([]string, 0, len(queues))
	for i := range queues {
		urls = append(urls, queues[i].URL)
	}

	wire.WriteJSON(w, map[string]any{"QueueUrls": urls})
}

func (h *Handler) deleteQueue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueURL string `json:"QueueUrl"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.mq.DeleteQueue(r.Context(), req.QueueURL); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueURL          string         `json:"QueueUrl"`
		MessageBody       string         `json:"MessageBody"`
		DelaySeconds      int            `json:"DelaySeconds"`
		GroupID           string         `json:"MessageGroupId"`
		DeduplicationID   string         `json:"MessageDeduplicationId"`
		MessageAttributes map[string]any `json:"MessageAttributes"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	out, err := h.mq.SendMessage(r.Context(), mqdriver.SendMessageInput{
		QueueURL:        req.QueueURL,
		Body:            req.MessageBody,
		DelaySeconds:    req.DelaySeconds,
		GroupID:         req.GroupID,
		DeduplicationID: req.DeduplicationID,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"MessageId": out.MessageID})
}

func (h *Handler) receiveMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueURL            string `json:"QueueUrl"`
		MaxNumberOfMessages int    `json:"MaxNumberOfMessages"`
		WaitTimeSeconds     int    `json:"WaitTimeSeconds"`
		VisibilityTimeout   int    `json:"VisibilityTimeout"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if req.MaxNumberOfMessages == 0 {
		req.MaxNumberOfMessages = 1
	}

	msgs, err := h.mq.ReceiveMessages(r.Context(), mqdriver.ReceiveMessageInput{
		QueueURL:          req.QueueURL,
		MaxMessages:       req.MaxNumberOfMessages,
		WaitTimeSeconds:   req.WaitTimeSeconds,
		VisibilityTimeout: req.VisibilityTimeout,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]map[string]any, 0, len(msgs))
	for i := range msgs {
		out = append(out, map[string]any{
			"MessageId":     msgs[i].MessageID,
			"ReceiptHandle": msgs[i].ReceiptHandle,
			"Body":          msgs[i].Body,
		})
	}

	wire.WriteJSON(w, map[string]any{"Messages": out})
}

func (h *Handler) deleteMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueURL      string `json:"QueueUrl"`
		ReceiptHandle string `json:"ReceiptHandle"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.mq.DeleteMessage(r.Context(), req.QueueURL, req.ReceiptHandle); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

// queueTagger is the AWS-specific SQS tagging surface. It's not part of the
// portable MessageQueue driver, so the handler type-asserts for it.
type queueTagger interface {
	TagQueue(ctx context.Context, queueURL string, tags map[string]string) error
	UntagQueue(ctx context.Context, queueURL string, keys []string) error
	ListQueueTags(ctx context.Context, queueURL string) (map[string]string, error)
}

func (h *Handler) tagQueue(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.mq.(queueTagger)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		QueueURL string            `json:"QueueUrl"`
		Tags     map[string]string `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := tagger.TagQueue(r.Context(), req.QueueURL, req.Tags); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) untagQueue(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.mq.(queueTagger)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		QueueURL string   `json:"QueueUrl"`
		TagKeys  []string `json:"TagKeys"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := tagger.UntagQueue(r.Context(), req.QueueURL, req.TagKeys); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) listQueueTags(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.mq.(queueTagger)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	var req struct {
		QueueURL string `json:"QueueUrl"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tags, err := tagger.ListQueueTags(r.Context(), req.QueueURL)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"Tags": tags})
}

// numericAttrKeys are the SetQueueAttributes attributes the provider applies.
var numericAttrKeys = []string{
	"DelaySeconds", "VisibilityTimeout", "MaximumMessageSize",
	"MessageRetentionPeriod", "ReceiveMessageWaitTimeSeconds",
}

func (h *Handler) getQueueAttributes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueURL       string   `json:"QueueUrl"`
		AttributeNames []string `json:"AttributeNames"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	attrs, err := h.mq.GetQueueAttributes(r.Context(), req.QueueURL)
	if err != nil {
		writeErr(w, err)
		return
	}

	info, err := h.mq.GetQueueInfo(r.Context(), req.QueueURL)
	if err != nil {
		writeErr(w, err)
		return
	}

	all := map[string]string{
		"QueueArn":                              info.ARN,
		"ApproximateNumberOfMessages":           strconv.Itoa(attrs.ApproximateMessageCount),
		"ApproximateNumberOfMessagesNotVisible": strconv.Itoa(attrs.ApproximateNotVisibleCount),
		"VisibilityTimeout":                     strconv.Itoa(attrs.VisibilityTimeout),
		"DelaySeconds":                          strconv.Itoa(attrs.DelaySeconds),
		"MaximumMessageSize":                    strconv.Itoa(attrs.MaximumMessageSize),
		"MessageRetentionPeriod":                strconv.Itoa(attrs.MessageRetentionPeriod),
		"CreatedTimestamp":                      strconv.FormatInt(attrs.CreatedAt.Unix(), 10),
		"LastModifiedTimestamp":                 strconv.FormatInt(attrs.LastModifiedAt.Unix(), 10),
		"FifoQueue":                             strconv.FormatBool(attrs.FifoQueue),
	}
	if attrs.RedrivePolicy != "" {
		all["RedrivePolicy"] = attrs.RedrivePolicy
	}

	wire.WriteJSON(w, map[string]any{"Attributes": selectAttributes(all, req.AttributeNames)})
}

// selectAttributes returns the requested subset, or all when the caller asks
// for "All" or names nothing (real SQS semantics).
func selectAttributes(all map[string]string, names []string) map[string]string {
	if len(names) == 0 {
		return all
	}

	for _, n := range names {
		if n == "All" {
			return all
		}
	}

	out := make(map[string]string, len(names))
	for _, n := range names {
		if v, ok := all[n]; ok {
			out[n] = v
		}
	}

	return out
}

func (h *Handler) setQueueAttributes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueURL   string            `json:"QueueUrl"`
		Attributes map[string]string `json:"Attributes"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	attrs := make(map[string]int, len(numericAttrKeys))
	for _, k := range numericAttrKeys {
		if v, ok := req.Attributes[k]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				attrs[k] = n
			}
		}
	}

	if err := h.mq.SetQueueAttributes(r.Context(), req.QueueURL, attrs); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) purgeQueue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueURL string `json:"QueueUrl"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.mq.PurgeQueue(r.Context(), req.QueueURL); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

// writeErr maps CloudEmu canonical errors to SQS-shaped HTTP error responses.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "QueueDoesNotExist", err.Error())
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "QueueNameExists", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterValue", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}
