// Package sqs implements the AWS SQS JSON-RPC protocol as a server.Handler.
// Modern aws-sdk-go-v2 SQS uses AwsJson1_0 with X-Amz-Target headers (since
// SQS migrated off the legacy Query protocol in 2023).
//
// Coverage: queue lifecycle, the synchronous send/receive/delete loop, batch
// send/delete, ChangeMessageVisibility, queue attributes (GetQueueAttributes
// exposes the QueueArn that event-source mappings, DLQ wiring, and S3->SQS
// notifications depend on), and PurgeQueue.
package sqs

import (
	"context"
	"net/http"
	"sort"
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
//
//nolint:gocyclo // flat dispatch: one branch per SQS operation
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
	case "SendMessageBatch":
		h.sendMessageBatch(w, r)
	case "DeleteMessageBatch":
		h.deleteMessageBatch(w, r)
	case "ChangeMessageVisibility":
		h.changeMessageVisibility(w, r)
	case "ChangeMessageVisibilityBatch":
		h.changeMessageVisibilityBatch(w, r)
	case "ListDeadLetterSourceQueues":
		h.listDeadLetterSourceQueues(w, r)
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
	case "AddPermission":
		h.addPermission(w, r)
	case "RemovePermission":
		h.removePermission(w, r)
	case "StartMessageMoveTask":
		h.startMessageMoveTask(w, r)
	case "CancelMessageMoveTask":
		h.cancelMessageMoveTask(w, r)
	case "ListMessageMoveTasks":
		h.listMessageMoveTasks(w, r)
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

	if !validateQueueAttributeRanges(w, req.Attributes) {
		return
	}

	cfg := mqdriver.QueueConfig{
		Name:                          req.QueueName,
		FIFO:                          req.Attributes["FifoQueue"] == attrTrue || strings.HasSuffix(req.QueueName, ".fifo"),
		Tags:                          req.Tags,
		DelaySeconds:                  atoiAttr(req.Attributes, "DelaySeconds"),
		VisibilityTimeout:             atoiAttr(req.Attributes, "VisibilityTimeout"),
		VisibilityTimeoutSet:          attrPresent(req.Attributes, "VisibilityTimeout"),
		MaxMessageSize:                atoiAttr(req.Attributes, "MaximumMessageSize"),
		MessageRetention:              atoiAttr(req.Attributes, "MessageRetentionPeriod"),
		ReceiveMessageWaitTimeSeconds: atoiAttr(req.Attributes, "ReceiveMessageWaitTimeSeconds"),
		ContentBasedDeduplication:     req.Attributes["ContentBasedDeduplication"] == attrTrue,
		RedrivePolicy:                 req.Attributes["RedrivePolicy"],
		RedriveAllowPolicy:            req.Attributes["RedriveAllowPolicy"],
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
		MaxResults      int    `json:"MaxResults"`
		NextToken       string `json:"NextToken"`
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

	sort.Strings(urls)

	page, next := paginateURLs(urls, req.MaxResults, req.NextToken)

	resp := map[string]any{"QueueUrls": page}
	if next != "" {
		resp["NextToken"] = next
	}

	wire.WriteJSON(w, resp)
}

// paginateURLs applies MaxResults/NextToken paging over a stable, sorted URL
// slice. The token is the 1-based start offset encoded as a string.
func paginateURLs(urls []string, maxResults int, token string) (page []string, next string) {
	start := 0

	if token != "" {
		if n, err := strconv.Atoi(token); err == nil && n >= 0 {
			start = n
		}
	}

	if start > len(urls) {
		start = len(urls)
	}

	end := len(urls)
	if maxResults > 0 && start+maxResults < end {
		end = start + maxResults
	}

	if end < len(urls) {
		next = strconv.Itoa(end)
	}

	return urls[start:end], next
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
		QueueURL          string                          `json:"QueueUrl"`
		MessageBody       string                          `json:"MessageBody"`
		DelaySeconds      int                             `json:"DelaySeconds"`
		GroupID           string                          `json:"MessageGroupId"`
		DeduplicationID   string                          `json:"MessageDeduplicationId"`
		MessageAttributes map[string]wireMessageAttribute `json:"MessageAttributes"`
		SystemAttributes  map[string]wireMessageAttribute `json:"MessageSystemAttributes"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if reason := validateSystemAttributes(req.SystemAttributes); reason != "" {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterValue", reason)
		return
	}

	msgAttrs := toDriverMessageAttributes(req.MessageAttributes)
	sysAttrs := toDriverMessageAttributes(req.SystemAttributes)

	out, err := h.mq.SendMessage(r.Context(), mqdriver.SendMessageInput{
		QueueURL:          req.QueueURL,
		Body:              req.MessageBody,
		DelaySeconds:      req.DelaySeconds,
		GroupID:           req.GroupID,
		DeduplicationID:   req.DeduplicationID,
		MessageAttributes: msgAttrs,
		SystemAttributes:  sysAttrs,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := map[string]any{
		"MessageId":        out.MessageID,
		"MD5OfMessageBody": md5OfBody(req.MessageBody),
	}
	if md5Attrs := md5OfMessageAttributes(msgAttrs); md5Attrs != "" {
		resp["MD5OfMessageAttributes"] = md5Attrs
	}

	if md5Sys := md5OfMessageAttributes(sysAttrs); md5Sys != "" {
		resp["MD5OfMessageSystemAttributes"] = md5Sys
	}

	if out.SequenceNumber != "" {
		resp["SequenceNumber"] = out.SequenceNumber
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) receiveMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueURL              string   `json:"QueueUrl"`
		MaxNumberOfMessages   int      `json:"MaxNumberOfMessages"`
		WaitTimeSeconds       int      `json:"WaitTimeSeconds"`
		VisibilityTimeout     int      `json:"VisibilityTimeout"`
		AttributeNames        []string `json:"AttributeNames"`
		MessageSystemAttrs    []string `json:"MessageSystemAttributeNames"`
		MessageAttributeNames []string `json:"MessageAttributeNames"`
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

	sysNames := append(append([]string{}, req.AttributeNames...), req.MessageSystemAttrs...)

	out := make([]map[string]any, 0, len(msgs))
	for i := range msgs {
		out = append(out, buildReceiveEntry(&msgs[i], sysNames, req.MessageAttributeNames))
	}

	wire.WriteJSON(w, map[string]any{"Messages": out})
}

// buildReceiveEntry shapes one received message into the SQS wire response,
// applying the requested system-attribute and message-attribute filters and
// emitting the MD5 checksums real clients validate.
func buildReceiveEntry(msg *mqdriver.Message, sysNames, msgAttrNames []string) map[string]any {
	entry := map[string]any{
		"MessageId":     msg.MessageID,
		"ReceiptHandle": msg.ReceiptHandle,
		"Body":          msg.Body,
		"MD5OfBody":     md5OfBody(msg.Body),
	}

	if sys := selectSystemAttributes(msg.SystemAttributes, sysNames); len(sys) > 0 {
		entry["Attributes"] = sys
	}

	if attrs := fromDriverMessageAttributes(msg.MessageAttributes, msgAttrNames); len(attrs) > 0 {
		entry["MessageAttributes"] = attrs
		entry["MD5OfMessageAttributes"] = md5OfMessageAttributes(msg.MessageAttributes)
	}

	return entry
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
		writeReceiptErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) changeMessageVisibility(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueURL          string `json:"QueueUrl"`
		ReceiptHandle     string `json:"ReceiptHandle"`
		VisibilityTimeout int    `json:"VisibilityTimeout"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.mq.ChangeVisibility(r.Context(), req.QueueURL, req.ReceiptHandle, req.VisibilityTimeout); err != nil {
		writeReceiptErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

// writeReceiptErr maps a ChangeMessageVisibility failure: an unknown receipt
// handle on an existing queue surfaces as ReceiptHandleIsInvalid, while a
// missing queue keeps the standard QueueDoesNotExist mapping.
func writeReceiptErr(w http.ResponseWriter, err error) {
	if cerrors.IsFailedPrecondition(err) {
		wire.WriteJSONError(w, http.StatusBadRequest, "ReceiptHandleIsInvalid", err.Error())
		return
	}

	writeErr(w, err)
}

func (h *Handler) changeMessageVisibilityBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueURL string `json:"QueueUrl"`
		Entries  []struct {
			ID                string `json:"Id"`
			ReceiptHandle     string `json:"ReceiptHandle"`
			VisibilityTimeout int    `json:"VisibilityTimeout"`
		} `json:"Entries"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ids := make([]string, len(req.Entries))
	for i := range req.Entries {
		ids[i] = req.Entries[i].ID
	}

	if !validateBatchEntryIDs(w, ids) {
		return
	}

	successful := make([]map[string]any, 0, len(req.Entries))
	failed := make([]map[string]any, 0)

	for i := range req.Entries {
		err := h.mq.ChangeVisibility(r.Context(), req.QueueURL, req.Entries[i].ReceiptHandle, req.Entries[i].VisibilityTimeout)
		if err != nil {
			failed = append(failed, map[string]any{
				"Id": req.Entries[i].ID, "Code": "ReceiptHandleIsInvalid",
				"Message": err.Error(), "SenderFault": true,
			})

			continue
		}

		successful = append(successful, map[string]any{"Id": req.Entries[i].ID})
	}

	wire.WriteJSON(w, map[string]any{"Successful": successful, "Failed": failed})
}

func (h *Handler) listDeadLetterSourceQueues(w http.ResponseWriter, r *http.Request) {
	lister, ok := h.mq.(dlqSourceLister)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "ListDeadLetterSourceQueues not supported"))
		return
	}

	var req struct {
		QueueURL   string `json:"QueueUrl"`
		MaxResults int    `json:"MaxResults"`
		NextToken  string `json:"NextToken"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	sources, err := lister.ListDeadLetterSourceQueues(r.Context(), req.QueueURL)
	if err != nil {
		writeErr(w, err)
		return
	}

	if sources == nil {
		sources = []string{}
	}

	sort.Strings(sources)

	page, next := paginateURLs(sources, req.MaxResults, req.NextToken)

	resp := map[string]any{"queueUrls": page}
	if next != "" {
		resp["NextToken"] = next
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) sendMessageBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueURL string `json:"QueueUrl"`
		Entries  []struct {
			ID                     string                          `json:"Id"`
			MessageBody            string                          `json:"MessageBody"`
			DelaySeconds           int                             `json:"DelaySeconds"`
			MessageGroupID         string                          `json:"MessageGroupId"`
			MessageDeduplicationID string                          `json:"MessageDeduplicationId"`
			MessageAttributes      map[string]wireMessageAttribute `json:"MessageAttributes"`
			SystemAttributes       map[string]wireMessageAttribute `json:"MessageSystemAttributes"`
		} `json:"Entries"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ids := make([]string, len(req.Entries))
	for i := range req.Entries {
		ids[i] = req.Entries[i].ID
	}

	if !validateBatchEntryIDs(w, ids) {
		return
	}

	for i := range req.Entries {
		if reason := validateSystemAttributes(req.Entries[i].SystemAttributes); reason != "" {
			wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterValue", reason)
			return
		}
	}

	bodyByID := make(map[string]string, len(req.Entries))
	attrsByID := make(map[string]map[string]mqdriver.MessageAttributeValue, len(req.Entries))
	sysAttrsByID := make(map[string]map[string]mqdriver.MessageAttributeValue, len(req.Entries))

	entries := make([]mqdriver.BatchSendEntry, 0, len(req.Entries))

	for i := range req.Entries {
		msgAttrs := toDriverMessageAttributes(req.Entries[i].MessageAttributes)
		sysAttrs := toDriverMessageAttributes(req.Entries[i].SystemAttributes)
		bodyByID[req.Entries[i].ID] = req.Entries[i].MessageBody
		attrsByID[req.Entries[i].ID] = msgAttrs
		sysAttrsByID[req.Entries[i].ID] = sysAttrs
		entries = append(entries, mqdriver.BatchSendEntry{
			ID:                req.Entries[i].ID,
			Body:              req.Entries[i].MessageBody,
			DelaySeconds:      req.Entries[i].DelaySeconds,
			GroupID:           req.Entries[i].MessageGroupID,
			DeduplicationID:   req.Entries[i].MessageDeduplicationID,
			MessageAttributes: msgAttrs,
			SystemAttributes:  sysAttrs,
		})
	}

	res, err := h.mq.SendMessageBatch(r.Context(), req.QueueURL, entries)
	if err != nil {
		writeErr(w, err)
		return
	}

	successful := buildSendBatchSuccess(res.Successful, bodyByID, attrsByID, sysAttrsByID)

	wire.WriteJSON(w, map[string]any{"Successful": successful, "Failed": batchFailed(res.Failed)})
}

// buildSendBatchSuccess shapes the successful SendMessageBatch entries, attaching
// the body/attribute MD5 checksums real clients validate, keyed back to each
// entry by its request Id.
func buildSendBatchSuccess(
	entries []mqdriver.BatchSendResultEntry,
	bodyByID map[string]string,
	attrsByID, sysAttrsByID map[string]map[string]mqdriver.MessageAttributeValue,
) []map[string]any {
	successful := make([]map[string]any, 0, len(entries))

	for i := range entries {
		entry := map[string]any{
			"Id":               entries[i].ID,
			"MessageId":        entries[i].MessageID,
			"MD5OfMessageBody": md5OfBody(bodyByID[entries[i].ID]),
		}
		if md5Attrs := md5OfMessageAttributes(attrsByID[entries[i].ID]); md5Attrs != "" {
			entry["MD5OfMessageAttributes"] = md5Attrs
		}

		if md5Sys := md5OfMessageAttributes(sysAttrsByID[entries[i].ID]); md5Sys != "" {
			entry["MD5OfMessageSystemAttributes"] = md5Sys
		}

		if entries[i].SequenceNumber != "" {
			entry["SequenceNumber"] = entries[i].SequenceNumber
		}

		successful = append(successful, entry)
	}

	return successful
}

func (h *Handler) deleteMessageBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QueueURL string `json:"QueueUrl"`
		Entries  []struct {
			ID            string `json:"Id"`
			ReceiptHandle string `json:"ReceiptHandle"`
		} `json:"Entries"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	ids := make([]string, len(req.Entries))
	for i := range req.Entries {
		ids[i] = req.Entries[i].ID
	}

	if !validateBatchEntryIDs(w, ids) {
		return
	}

	entries := make([]mqdriver.BatchDeleteEntry, 0, len(req.Entries))
	for i := range req.Entries {
		entries = append(entries, mqdriver.BatchDeleteEntry{
			ID:            req.Entries[i].ID,
			ReceiptHandle: req.Entries[i].ReceiptHandle,
		})
	}

	res, err := h.mq.DeleteMessageBatch(r.Context(), req.QueueURL, entries)
	if err != nil {
		writeErr(w, err)
		return
	}

	successful := make([]map[string]any, 0, len(res.Successful))
	for i := range res.Successful {
		successful = append(successful, map[string]any{"Id": res.Successful[i]})
	}

	wire.WriteJSON(w, map[string]any{"Successful": successful, "Failed": batchFailed(res.Failed)})
}

// selectSystemAttributes returns the requested system attributes. Unlike
// GetQueueAttributes, an empty request returns none (SQS ReceiveMessage
// semantics: system Attributes appear only when explicitly requested).
func selectSystemAttributes(all map[string]string, names []string) map[string]string {
	want := attributeSelector(names)

	out := make(map[string]string, len(all))

	for k, v := range all {
		if want(k) {
			out[k] = v
		}
	}

	return out
}

// batchFailed shapes driver batch failures into the SQS wire response form.
func batchFailed(failed []mqdriver.BatchSendFailEntry) []map[string]any {
	out := make([]map[string]any, 0, len(failed))
	for i := range failed {
		out = append(out, map[string]any{
			"Id":          failed[i].ID,
			"Code":        failed[i].Code,
			"Message":     failed[i].Message,
			"SenderFault": true,
		})
	}

	return out
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

//nolint:dupl // uniform optional-interface handler shape; distinct SQS operation.
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

// attrConfigurator is the AWS-specific richer attribute surface (RedrivePolicy,
// ContentBasedDeduplication, Policy, KmsMasterKeyId) that the numeric-only
// portable driver cannot express. The handler type-asserts for it.
type attrConfigurator interface {
	SetQueueAttributesRaw(ctx context.Context, queueURL string, attrs map[string]string) error
}

// dlqSourceLister exposes ListDeadLetterSourceQueues (AWS-specific).
type dlqSourceLister interface {
	ListDeadLetterSourceQueues(ctx context.Context, dlqURL string) ([]string, error)
}

// queuePermissioner exposes the SQS AddPermission/RemovePermission access-policy
// surface (AWS-specific). The handler type-asserts for it.
type queuePermissioner interface {
	AddPermission(ctx context.Context, queueURL, label string, accountIDs, actions []string) error
	RemovePermission(ctx context.Context, queueURL, label string) error
}

// messageMover exposes the SQS dead-letter-queue redrive surface
// (StartMessageMoveTask/CancelMessageMoveTask/ListMessageMoveTasks). It is
// AWS-specific and not part of the portable MessageQueue driver, so the handler
// type-asserts for it.
type messageMover interface {
	StartMessageMoveTask(ctx context.Context, sourceARN, destARN string, maxRate int) (string, error)
	CancelMessageMoveTask(ctx context.Context, taskHandle string) (int64, error)
	ListMessageMoveTasks(ctx context.Context, sourceARN string, maxResults int) ([]mqdriver.MessageMoveTask, error)
}

func (h *Handler) startMessageMoveTask(w http.ResponseWriter, r *http.Request) {
	mover, ok := h.mq.(messageMover)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "StartMessageMoveTask not supported"))
		return
	}

	var req struct {
		SourceArn                    string `json:"SourceArn"`
		DestinationArn               string `json:"DestinationArn"`
		MaxNumberOfMessagesPerSecond int    `json:"MaxNumberOfMessagesPerSecond"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	handle, err := mover.StartMessageMoveTask(r.Context(), req.SourceArn, req.DestinationArn, req.MaxNumberOfMessagesPerSecond)
	if err != nil {
		writeMoveTaskErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"TaskHandle": handle})
}

//nolint:dupl // uniform optional-interface handler shape; distinct SQS operation.
func (h *Handler) cancelMessageMoveTask(w http.ResponseWriter, r *http.Request) {
	mover, ok := h.mq.(messageMover)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "CancelMessageMoveTask not supported"))
		return
	}

	var req struct {
		TaskHandle string `json:"TaskHandle"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	moved, err := mover.CancelMessageMoveTask(r.Context(), req.TaskHandle)
	if err != nil {
		writeMoveTaskErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"ApproximateNumberOfMessagesMoved": moved})
}

func (h *Handler) listMessageMoveTasks(w http.ResponseWriter, r *http.Request) {
	mover, ok := h.mq.(messageMover)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "ListMessageMoveTasks not supported"))
		return
	}

	var req struct {
		SourceArn  string `json:"SourceArn"`
		MaxResults int    `json:"MaxResults"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tasks, err := mover.ListMessageMoveTasks(r.Context(), req.SourceArn, req.MaxResults)
	if err != nil {
		writeMoveTaskErr(w, err)
		return
	}

	results := make([]map[string]any, 0, len(tasks))
	for i := range tasks {
		results = append(results, moveTaskResult(&tasks[i]))
	}

	wire.WriteJSON(w, map[string]any{"Results": results})
}

// moveTaskResult shapes one move task into its SQS ListMessageMoveTasks wire
// entry, omitting the optional fields real SQS leaves out when unset.
func moveTaskResult(t *mqdriver.MessageMoveTask) map[string]any {
	entry := map[string]any{
		"ApproximateNumberOfMessagesMoved":  t.ApproxMessagesMoved,
		"ApproximateNumberOfMessagesToMove": t.ApproxMessagesToMove,
		"SourceArn":                         t.SourceARN,
		"Status":                            t.Status,
		"StartedTimestamp":                  t.StartedAt.UnixMilli(),
	}

	if t.TaskHandle != "" {
		entry["TaskHandle"] = t.TaskHandle
	}

	if t.DestinationARN != "" {
		entry["DestinationArn"] = t.DestinationARN
	}

	if t.MaxNumberOfMessagesPerSecond > 0 {
		entry["MaxNumberOfMessagesPerSecond"] = t.MaxNumberOfMessagesPerSecond
	}

	if t.FailureReason != "" {
		entry["FailureReason"] = t.FailureReason
	}

	return entry
}

// writeMoveTaskErr maps canonical errors to the SQS message-move error codes.
func writeMoveTaskErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", err.Error())
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "UnsupportedOperation", err.Error())
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterValue", err.Error())
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}

func (h *Handler) addPermission(w http.ResponseWriter, r *http.Request) {
	perm, ok := h.mq.(queuePermissioner)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "AddPermission not supported"))
		return
	}

	var req struct {
		QueueURL      string   `json:"QueueUrl"`
		Label         string   `json:"Label"`
		AWSAccountIDs []string `json:"AWSAccountIds"`
		Actions       []string `json:"Actions"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := perm.AddPermission(r.Context(), req.QueueURL, req.Label, req.AWSAccountIDs, req.Actions); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

func (h *Handler) removePermission(w http.ResponseWriter, r *http.Request) {
	perm, ok := h.mq.(queuePermissioner)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "RemovePermission not supported"))
		return
	}

	var req struct {
		QueueURL string `json:"QueueUrl"`
		Label    string `json:"Label"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := perm.RemovePermission(r.Context(), req.QueueURL, req.Label); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{})
}

// attrPresent reports whether an attribute was supplied, distinguishing an
// explicit "0" from an omitted value for defaults resolved in the provider.
func attrPresent(attrs map[string]string, key string) bool {
	_, ok := attrs[key]

	return ok
}

// atoiAttr parses a string attribute as an int, returning 0 when absent or invalid.
func atoiAttr(attrs map[string]string, key string) int {
	if v, ok := attrs[key]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}

	return 0
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
		"ApproximateNumberOfMessagesDelayed":    strconv.Itoa(attrs.ApproximateDelayedCount),
		"VisibilityTimeout":                     strconv.Itoa(attrs.VisibilityTimeout),
		"DelaySeconds":                          strconv.Itoa(attrs.DelaySeconds),
		"MaximumMessageSize":                    strconv.Itoa(attrs.MaximumMessageSize),
		"MessageRetentionPeriod":                strconv.Itoa(attrs.MessageRetentionPeriod),
		"ReceiveMessageWaitTimeSeconds":         strconv.Itoa(attrs.ReceiveMessageWaitTimeSeconds),
		"CreatedTimestamp":                      strconv.FormatInt(attrs.CreatedAt.Unix(), 10),
		"LastModifiedTimestamp":                 strconv.FormatInt(attrs.LastModifiedAt.Unix(), 10),
		"SqsManagedSseEnabled":                  "false",
	}

	// SQS returns FifoQueue/ContentBasedDeduplication only for FIFO queues.
	if attrs.FifoQueue {
		all["FifoQueue"] = attrTrue
		all["ContentBasedDeduplication"] = strconv.FormatBool(attrs.ContentBasedDeduplication)
	}

	if attrs.RedrivePolicy != "" {
		all["RedrivePolicy"] = attrs.RedrivePolicy
	}

	if attrs.RedriveAllowPolicy != "" {
		all["RedriveAllowPolicy"] = attrs.RedriveAllowPolicy
	}

	if attrs.Policy != "" {
		all["Policy"] = attrs.Policy
	}

	if attrs.KmsMasterKeyID != "" {
		all["KmsMasterKeyId"] = attrs.KmsMasterKeyID
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
		if n == attrAll {
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

	if !validateQueueAttributeRanges(w, req.Attributes) {
		return
	}

	// Prefer the richer AWS-specific setter so RedrivePolicy/ContentBasedDeduplication/
	// Policy/KmsMasterKeyId are applied (DLQ redrive wiring depends on this).
	if cfg, ok := h.mq.(attrConfigurator); ok {
		if err := cfg.SetQueueAttributesRaw(r.Context(), req.QueueURL, req.Attributes); err != nil {
			writeErr(w, err)
			return
		}

		wire.WriteJSON(w, map[string]any{})

		return
	}

	numericAttrKeys := []string{
		"DelaySeconds", "VisibilityTimeout", "MaximumMessageSize",
		"MessageRetentionPeriod", "ReceiveMessageWaitTimeSeconds",
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
