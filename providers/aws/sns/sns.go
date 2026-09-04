// Package sns provides an in-memory mock implementation of AWS Simple Notification Service.
package sns

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// Compile-time check that Mock implements driver.Notification.
var _ driver.Notification = (*Mock)(nil)

// Subscription lifecycle states.
const (
	statusConfirmed = "confirmed"
	statusPending   = "pending"
)

// SNS message-structure and message-attribute constants.
const (
	messageStructureJSON  = "json"
	defaultProtocolKey    = "default"
	protocolSQS           = "sqs"
	protocolLambda        = "lambda"
	messageAttrTypeString = "String"
)

// fifoSuffix is the mandatory topic-name suffix for a FIFO SNS topic.
const fifoSuffix = ".fifo"

// SNS delivery-outcome metric names.
const (
	metricNotificationsDelivered = "NumberOfNotificationsDelivered"
	metricNotificationsFailed    = "NumberOfNotificationsFailed"
)

type publishedMessage struct {
	ID         string
	TopicID    string
	Subject    string
	Message    string
	Attributes map[string]string
}

type topicData struct {
	info          driver.TopicInfo
	subscriptions *memstore.Store[driver.SubscriptionInfo]
	messages      []publishedMessage
	deleted       int // monotonic count of unsubscribed endpoints
	mu            sync.RWMutex
}

// confirmationProtocols is the set of SNS protocols whose subscriptions start
// out pending until ConfirmSubscription is called. sqs/lambda/sms/application/
// firehose auto-confirm.
var confirmationProtocols = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"http":       {},
	"https":      {},
	"email":      {},
	"email-json": {},
}

func requiresConfirmation(protocol string) bool {
	_, ok := confirmationProtocols[protocol]
	return ok
}

// validProtocols is the set of delivery protocols SNS Subscribe accepts. Any
// other value is rejected with InvalidParameter, matching real SNS.
var validProtocols = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"http":        {},
	"https":       {},
	"email":       {},
	"email-json":  {},
	"sms":         {},
	"sqs":         {},
	"application": {},
	"lambda":      {},
	"firehose":    {},
}

func isValidProtocol(protocol string) bool {
	_, ok := validProtocols[protocol]
	return ok
}

// SQSDeliverer delivers an SNS notification into an SQS queue identified by
// its ARN. The SQS mock satisfies this, enabling real SNS -> SQS fan-out.
// DeliverExternalFIFO carries the FIFO MessageGroupId / MessageDeduplicationId so
// a FIFO topic fanning out to a FIFO SQS queue (which requires a group id) is not
// silently rejected; group/dedup are empty for standard delivery.
type SQSDeliverer interface {
	DeliverExternal(ctx context.Context, queueARN, body string) error
	DeliverExternalFIFO(ctx context.Context, queueARN, body, groupID, dedupID string) error
}

// LambdaInvoker asynchronously invokes a Lambda function by ARN with an SNS event
// payload. The Lambda mock satisfies this, enabling real SNS -> Lambda fan-out.
type LambdaInvoker interface {
	InvokeExternal(ctx context.Context, functionARN string, payload []byte) error
}

// Mock is an in-memory mock implementation of the AWS SNS service.
type Mock struct {
	topics     *memstore.Store[*topicData]
	opts       *config.Options
	monitoring mondriver.Monitoring
	sqs        SQSDeliverer
	lambda     LambdaInvoker
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// SetSQSDeliverer wires the SQS backend so publishes fan out to SQS subscriptions.
func (m *Mock) SetSQSDeliverer(d SQSDeliverer) {
	m.sqs = d
}

// SetLambdaInvoker wires the Lambda backend so publishes fan out to
// lambda-protocol subscriptions.
func (m *Mock) SetLambdaInvoker(l LambdaInvoker) {
	m.lambda = l
}

func (m *Mock) emitMetric(metricName string, value float64, unit string, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: "AWS/SNS", MetricName: metricName, Value: value, Unit: unit,
		Dimensions: dims, Timestamp: m.opts.Clock.Now(),
	}})
}

// New creates a new SNS mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		topics: memstore.New[*topicData](),
		opts:   opts,
	}
}

// CreateTopic creates a new SNS topic.
// arnRegion returns the region field of an SNS ARN
// (arn:aws:sns:<region>:<account>:<name>), or fallback when the ARN is
// malformed. A topic's stored ARN is the source of truth for its region, so a
// subscription ARN and the notification-envelope URLs are derived from it rather
// than the configured default.
func arnRegion(arn, fallback string) string {
	const regionField, minFields = 3, 6

	parts := strings.Split(arn, ":")
	if len(parts) < minFields || parts[regionField] == "" {
		return fallback
	}

	return parts[regionField]
}

func (m *Mock) CreateTopic(ctx context.Context, cfg driver.TopicConfig) (*driver.TopicInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "topic name is required")
	}

	if cfg.FifoTopic && !strings.HasSuffix(cfg.Name, fifoSuffix) {
		return nil, errors.New(errors.InvalidArgument,
			"Invalid parameter: Name Reason: FIFO Topic Names must end with .fifo")
	}

	if m.topics.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "topic %q already exists", cfg.Name)
	}

	arn := idgen.AWSARN("sns", regionctx.RegionOr(ctx, m.opts.Region), m.opts.AccountID, cfg.Name)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	info := driver.TopicInfo{
		ID:                        idgen.GenerateID("topic-"),
		Name:                      cfg.Name,
		Scope:                     cfg.Scope,
		ResourceID:                arn,
		DisplayName:               cfg.DisplayName,
		Policy:                    cfg.Policy,
		DeliveryPolicy:            cfg.DeliveryPolicy,
		KmsMasterKeyID:            cfg.KmsMasterKeyID,
		FifoTopic:                 cfg.FifoTopic,
		ContentBasedDeduplication: cfg.ContentBasedDeduplication,
		SubscriptionCount:         0,
		Tags:                      tags,
	}

	applyCreateFeedbackAttrs(&info, &cfg)

	td := &topicData{
		info:          info,
		subscriptions: memstore.New[driver.SubscriptionInfo](),
	}

	m.topics.Set(cfg.Name, td)

	result := info

	return &result, nil
}

// DeleteTopic deletes an SNS topic by name.
func (m *Mock) DeleteTopic(_ context.Context, id string) error {
	if !m.topics.Delete(id) {
		return errors.Newf(errors.NotFound, "topic %q not found", id)
	}

	return nil
}

// GetTopic retrieves information about an SNS topic.
func (m *Mock) GetTopic(_ context.Context, id string) (*driver.TopicInfo, error) {
	td, ok := m.topics.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", id)
	}

	result := td.withCounts()

	return &result, nil
}

// withCounts returns a copy of the topic info with the confirmed/pending/deleted
// subscription counters populated from live subscription state.
func (td *topicData) withCounts() driver.TopicInfo {
	info := td.info

	var confirmed, pending int

	for _, s := range td.subscriptions.All() {
		if s.Status == statusPending {
			pending++
		} else {
			confirmed++
		}
	}

	info.SubscriptionsConfirmed = confirmed
	info.SubscriptionsPending = pending

	td.mu.RLock()
	info.SubscriptionsDeleted = td.deleted
	td.mu.RUnlock()

	info.SubscriptionCount = confirmed + pending

	return info
}

// ListTopics lists all SNS topics visible under the given scope filter.
func (m *Mock) ListTopics(_ context.Context, filter scope.Scope) ([]driver.TopicInfo, error) {
	all := m.topics.SortedValues()

	topics := make([]driver.TopicInfo, 0, len(all))

	for _, td := range all {
		if !td.info.Scope.Matches(filter) {
			continue
		}

		topics = append(topics, td.withCounts())
	}

	return topics, nil
}

// UpdateTopic replaces the mutable fields of an existing topic — ARM
// CreateOrUpdate-on-existing semantics (display name and tags come from the
// request; identity is preserved).
func (m *Mock) UpdateTopic(_ context.Context, cfg driver.TopicConfig) (*driver.TopicInfo, error) {
	td, ok := m.topics.Get(cfg.Name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", cfg.Name)
	}

	if cfg.DisplayName != "" {
		td.info.DisplayName = cfg.DisplayName
	}

	if cfg.Tags != nil {
		td.info.Tags = maps.Clone(cfg.Tags)
	}

	if cfg.Policy != "" {
		td.info.Policy = cfg.Policy
	}

	if cfg.DeliveryPolicy != "" {
		td.info.DeliveryPolicy = cfg.DeliveryPolicy
	}

	if cfg.KmsMasterKeyID != "" {
		td.info.KmsMasterKeyID = cfg.KmsMasterKeyID
	}

	if cfg.ContentBasedDeduplicationSet {
		if !td.info.FifoTopic {
			return nil, errors.New(errors.InvalidArgument,
				"Invalid parameter: Attributes Reason: ContentBasedDeduplication attribute "+
					"is not applicable for standard topics")
		}

		td.info.ContentBasedDeduplication = cfg.ContentBasedDeduplication
	}

	applyUpdateFeedbackAttrs(&td.info, &cfg)

	if !cfg.Scope.IsZero() {
		td.info.Scope = cfg.Scope
	}

	m.topics.Set(cfg.Name, td)

	result := td.withCounts()

	return &result, nil
}

// applyCreateFeedbackAttrs copies SignatureVersion, TracingConfig,
// ArchivePolicy, and the per-protocol delivery-status feedback attributes from
// a CreateTopic config onto the new topic's info, unconditionally (a create
// always applies the field exactly as given, including its zero value).
func applyCreateFeedbackAttrs(info *driver.TopicInfo, cfg *driver.TopicConfig) {
	info.SignatureVersion = cfg.SignatureVersion
	info.TracingConfig = cfg.TracingConfig
	info.ArchivePolicy = cfg.ArchivePolicy
	info.ApplicationSuccessFeedbackRoleArn = cfg.ApplicationSuccessFeedbackRoleArn
	info.ApplicationFailureFeedbackRoleArn = cfg.ApplicationFailureFeedbackRoleArn
	info.ApplicationSuccessFeedbackSampleRate = cfg.ApplicationSuccessFeedbackSampleRate
	info.HTTPSuccessFeedbackRoleArn = cfg.HTTPSuccessFeedbackRoleArn
	info.HTTPFailureFeedbackRoleArn = cfg.HTTPFailureFeedbackRoleArn
	info.HTTPSuccessFeedbackSampleRate = cfg.HTTPSuccessFeedbackSampleRate
	info.LambdaSuccessFeedbackRoleArn = cfg.LambdaSuccessFeedbackRoleArn
	info.LambdaFailureFeedbackRoleArn = cfg.LambdaFailureFeedbackRoleArn
	info.LambdaSuccessFeedbackSampleRate = cfg.LambdaSuccessFeedbackSampleRate
	info.SQSSuccessFeedbackRoleArn = cfg.SQSSuccessFeedbackRoleArn
	info.SQSFailureFeedbackRoleArn = cfg.SQSFailureFeedbackRoleArn
	info.SQSSuccessFeedbackSampleRate = cfg.SQSSuccessFeedbackSampleRate
	info.FirehoseSuccessFeedbackRoleArn = cfg.FirehoseSuccessFeedbackRoleArn
	info.FirehoseFailureFeedbackRoleArn = cfg.FirehoseFailureFeedbackRoleArn
	info.FirehoseSuccessFeedbackSampleRate = cfg.FirehoseSuccessFeedbackSampleRate
}

// setIfNonEmpty assigns src to *dst only when src is non-empty, mirroring the
// no-clear-via-empty-string convention UpdateTopic already applies to
// DeliveryPolicy/KmsMasterKeyID: SetTopicAttributes sets one attribute per
// call, so an update config only ever carries an opinion on the attribute(s)
// the caller actually named.
func setIfNonEmpty(dst *string, src string) {
	if src != "" {
		*dst = src
	}
}

// applyUpdateFeedbackAttrs merges SignatureVersion, TracingConfig,
// ArchivePolicy, and the per-protocol delivery-status feedback attributes from
// an UpdateTopic (SetTopicAttributes) config onto an existing topic's info,
// leaving any attribute the caller didn't name (empty in cfg) unchanged.
func applyUpdateFeedbackAttrs(info *driver.TopicInfo, cfg *driver.TopicConfig) {
	setIfNonEmpty(&info.SignatureVersion, cfg.SignatureVersion)
	setIfNonEmpty(&info.TracingConfig, cfg.TracingConfig)
	setIfNonEmpty(&info.ArchivePolicy, cfg.ArchivePolicy)
	setIfNonEmpty(&info.ApplicationSuccessFeedbackRoleArn, cfg.ApplicationSuccessFeedbackRoleArn)
	setIfNonEmpty(&info.ApplicationFailureFeedbackRoleArn, cfg.ApplicationFailureFeedbackRoleArn)
	setIfNonEmpty(&info.ApplicationSuccessFeedbackSampleRate, cfg.ApplicationSuccessFeedbackSampleRate)
	setIfNonEmpty(&info.HTTPSuccessFeedbackRoleArn, cfg.HTTPSuccessFeedbackRoleArn)
	setIfNonEmpty(&info.HTTPFailureFeedbackRoleArn, cfg.HTTPFailureFeedbackRoleArn)
	setIfNonEmpty(&info.HTTPSuccessFeedbackSampleRate, cfg.HTTPSuccessFeedbackSampleRate)
	setIfNonEmpty(&info.LambdaSuccessFeedbackRoleArn, cfg.LambdaSuccessFeedbackRoleArn)
	setIfNonEmpty(&info.LambdaFailureFeedbackRoleArn, cfg.LambdaFailureFeedbackRoleArn)
	setIfNonEmpty(&info.LambdaSuccessFeedbackSampleRate, cfg.LambdaSuccessFeedbackSampleRate)
	setIfNonEmpty(&info.SQSSuccessFeedbackRoleArn, cfg.SQSSuccessFeedbackRoleArn)
	setIfNonEmpty(&info.SQSFailureFeedbackRoleArn, cfg.SQSFailureFeedbackRoleArn)
	setIfNonEmpty(&info.SQSSuccessFeedbackSampleRate, cfg.SQSSuccessFeedbackSampleRate)
	setIfNonEmpty(&info.FirehoseSuccessFeedbackRoleArn, cfg.FirehoseSuccessFeedbackRoleArn)
	setIfNonEmpty(&info.FirehoseFailureFeedbackRoleArn, cfg.FirehoseFailureFeedbackRoleArn)
	setIfNonEmpty(&info.FirehoseSuccessFeedbackSampleRate, cfg.FirehoseSuccessFeedbackSampleRate)
}

// Subscribe creates a subscription to an SNS topic.
func (m *Mock) Subscribe(_ context.Context, cfg driver.SubscriptionConfig) (*driver.SubscriptionInfo, error) {
	td, ok := m.topics.Get(cfg.TopicID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", cfg.TopicID)
	}

	if cfg.Protocol == "" {
		return nil, errors.New(errors.InvalidArgument, "protocol is required")
	}

	if !isValidProtocol(cfg.Protocol) {
		return nil, errors.Newf(errors.InvalidArgument,
			"Invalid parameter: Protocol Reason: %s protocol is not supported", cfg.Protocol)
	}

	if cfg.Endpoint == "" {
		return nil, errors.New(errors.InvalidArgument, "endpoint is required")
	}

	// Subscribe is idempotent on (TopicArn, Protocol, Endpoint): real SNS
	// returns the existing subscription (pending or confirmed) instead of
	// creating a duplicate. td.mu serializes the scan-then-create so two
	// concurrent identical Subscribe calls can't both observe "not found" and
	// each create their own subscription.
	td.mu.Lock()
	defer td.mu.Unlock()

	for _, s := range td.subscriptions.All() {
		if s.Protocol == cfg.Protocol && s.Endpoint == cfg.Endpoint {
			result := s

			return &result, nil
		}
	}

	subID := idgen.GenerateID("sub-")
	arn := idgen.AWSARN("sns", arnRegion(td.info.ResourceID, m.opts.Region), m.opts.AccountID, cfg.TopicID+":"+subID)

	attrs := make(map[string]string, len(cfg.Attributes))
	for k, v := range cfg.Attributes {
		attrs[k] = v
	}

	sub := driver.SubscriptionInfo{
		ID:         arn,
		TopicID:    cfg.TopicID,
		Protocol:   cfg.Protocol,
		Endpoint:   cfg.Endpoint,
		Status:     statusConfirmed,
		Attributes: attrs,
	}

	if requiresConfirmation(cfg.Protocol) {
		sub.Status = statusPending
		sub.ConfirmationToken = idgen.GenerateID("")
	}

	td.subscriptions.Set(arn, sub)

	result := sub

	return &result, nil
}

// Unsubscribe removes a subscription from an SNS topic.
func (m *Mock) Unsubscribe(_ context.Context, subscriptionID string) error {
	all := m.topics.All()

	for _, td := range all {
		if td.subscriptions.Delete(subscriptionID) {
			td.mu.Lock()
			td.deleted++
			td.mu.Unlock()

			return nil
		}
	}

	return errors.Newf(errors.NotFound, "subscription %q not found", subscriptionID)
}

// ListSubscriptions lists all subscriptions for an SNS topic.
func (m *Mock) ListSubscriptions(_ context.Context, topicID string) ([]driver.SubscriptionInfo, error) {
	td, ok := m.topics.Get(topicID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", topicID)
	}

	all := td.subscriptions.SortedValues()

	subs := make([]driver.SubscriptionInfo, 0, len(all))
	for _, s := range all {
		subs = append(subs, s)
	}

	return subs, nil
}

// Publish publishes a message to an SNS topic.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) Publish(ctx context.Context, input driver.PublishInput) (*driver.PublishOutput, error) {
	td, ok := m.topics.Get(input.TopicID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", input.TopicID)
	}

	if input.Message == "" {
		return nil, errors.New(errors.InvalidArgument, "message is required")
	}

	if err := validateFIFOPublish(&td.info, &input); err != nil {
		return nil, err
	}

	if err := validateMessageStructure(&input); err != nil {
		return nil, err
	}

	msgID := idgen.GenerateID("msg-")

	attrs := make(map[string]string, len(input.Attributes))
	for k, v := range input.Attributes {
		attrs[k] = v
	}

	td.mu.Lock()
	td.messages = append(td.messages, publishedMessage{
		ID:         msgID,
		TopicID:    input.TopicID,
		Subject:    input.Subject,
		Message:    input.Message,
		Attributes: attrs,
	})
	td.mu.Unlock()

	m.fanOutToSQS(ctx, td, msgID, &input)
	m.fanOutToLambda(ctx, td, msgID, &input)

	dims := map[string]string{"TopicName": input.TopicID}
	m.emitMetric("NumberOfMessagesPublished", 1, "Count", dims)
	m.emitMetric("PublishSize", float64(len(input.Message)), "Bytes", dims)

	return &driver.PublishOutput{MessageID: msgID}, nil
}

// PublishExternal publishes a raw message to the topic identified by its ARN,
// fanning it out to the topic's subscriptions. It backs cross-service event
// delivery (e.g. S3 -> SNS notifications). An unknown topic is a no-op so a
// stale target never fails the caller.
func (m *Mock) PublishExternal(ctx context.Context, topicARN, message string) error {
	name := arnResource(topicARN)
	if _, ok := m.topics.Get(name); !ok {
		return nil
	}

	_, err := m.Publish(ctx, driver.PublishInput{TopicID: name, Message: message})

	return err
}

// arnResource returns the resource segment (after the last colon) of an ARN,
// e.g. the topic name of arn:aws:sns:us-east-1:000000000000:my-topic.
func arnResource(arn string) string {
	if i := strings.LastIndexByte(arn, ':'); i >= 0 {
		return arn[i+1:]
	}

	return arn
}

// validateFIFOPublish enforces the two FIFO-topic Publish requirements real SNS
// rejects when missing: every message to a FIFO topic must carry a
// MessageGroupId, and it must carry a MessageDeduplicationId unless the topic
// has ContentBasedDeduplication enabled (in which case SNS derives one from the
// message body). Standard topics accept MessageGroupId optionally (forwarded to
// SQS standard subscriptions for fair-queue routing) and never require dedup ids,
// so this only applies to FIFO topics.
func validateFIFOPublish(info *driver.TopicInfo, input *driver.PublishInput) error {
	if !info.FifoTopic {
		return nil
	}

	if input.MessageGroupID == "" {
		return errors.New(errors.InvalidArgument,
			"Invalid parameter: The MessageGroupId parameter is required for FIFO topics")
	}

	if input.MessageDeduplicationID == "" && !info.ContentBasedDeduplication {
		return errors.New(errors.InvalidArgument,
			"Invalid parameter: The topic should either have ContentBasedDeduplication set, "+
				"or the Publish request should provide a MessageDeduplicationId")
	}

	return nil
}

// validateMessageStructure enforces the SNS rules for a MessageStructure=json
// publish: only "json" is accepted, the message must parse as a JSON object of
// string values, and it must carry a "default" entry (used for any protocol
// without a specific key). An empty structure leaves the message unchanged.
func validateMessageStructure(input *driver.PublishInput) error {
	if input.MessageStructure == "" {
		return nil
	}

	if input.MessageStructure != messageStructureJSON {
		return errors.New(errors.InvalidArgument, "Invalid parameter: Invalid message structure. Must be json")
	}

	parsed, ok := parseStructuredMessage(input.Message)
	if !ok {
		return errors.New(errors.InvalidArgument,
			"Invalid parameter: Message Structure - JSON message body failed to parse")
	}

	if _, ok := parsed[defaultProtocolKey]; !ok {
		return errors.New(errors.InvalidArgument,
			"Invalid parameter: Message Structure - No default entry in JSON message body")
	}

	return nil
}

// parseStructuredMessage decodes a MessageStructure=json body into its
// per-protocol string map. It reports false when the body is not a JSON object
// of strings.
func parseStructuredMessage(message string) (map[string]string, bool) {
	var parsed map[string]string
	if err := json.Unmarshal([]byte(message), &parsed); err != nil {
		return nil, false
	}

	return parsed, true
}

// resolveProtocolMessage returns the body a subscriber of the given protocol
// receives. For a MessageStructure=json publish it selects the protocol-specific
// entry, falling back to "default"; otherwise the raw message is returned as-is.
func resolveProtocolMessage(input *driver.PublishInput, protocol string) string {
	if input.MessageStructure != messageStructureJSON {
		return input.Message
	}

	parsed, ok := parseStructuredMessage(input.Message)
	if !ok {
		return input.Message
	}

	if v, present := parsed[protocol]; present {
		return v
	}

	return parsed[defaultProtocolKey]
}

// fanOutToSQS delivers a published message to every SQS-protocol subscription
// on the topic, wrapping it in the SNS notification envelope real SNS uses.
func (m *Mock) fanOutToSQS(ctx context.Context, td *topicData, msgID string, input *driver.PublishInput) {
	if m.sqs == nil {
		return
	}

	message := resolveProtocolMessage(input, protocolSQS)

	for _, sub := range td.subscriptions.All() {
		if sub.Protocol != protocolSQS || sub.Endpoint == "" {
			continue
		}

		// A filter policy gates delivery: the message reaches the subscriber
		// only when its attributes (or body) satisfy the policy.
		if !subscriptionAccepts(&sub, input) {
			continue
		}

		// With raw message delivery enabled, SNS strips its metadata and sends
		// the published message body as-is instead of the Notification envelope.
		body := message

		if !rawDeliveryEnabled(&sub) {
			envelope, err := m.notificationEnvelope(td, msgID, input, message, sub.ID)
			if err != nil {
				continue
			}

			body = envelope
		}

		m.deliverToSQS(ctx, sub.Endpoint, body, input)
	}
}

// fanOutToLambda invokes every lambda-protocol subscription on the topic,
// wrapping the published message in the SNS -> Lambda Records event real AWS
// delivers. A nil invoker skips delivery gracefully.
func (m *Mock) fanOutToLambda(ctx context.Context, td *topicData, msgID string, input *driver.PublishInput) {
	if m.lambda == nil {
		return
	}

	message := resolveProtocolMessage(input, protocolLambda)

	for _, sub := range td.subscriptions.All() {
		if sub.Protocol != protocolLambda || sub.Endpoint == "" {
			continue
		}

		// A filter policy gates delivery exactly as on the SQS path.
		if !subscriptionAccepts(&sub, input) {
			continue
		}

		event := map[string]any{
			"Records": []any{
				map[string]any{
					"EventSource":          "aws:sns",
					"EventVersion":         "1.0",
					"EventSubscriptionArn": sub.ID,
					"Sns":                  m.notificationEnvelopeMap(td, msgID, input, message, sub.ID),
				},
			},
		}

		payload, err := json.Marshal(event)
		if err != nil {
			continue
		}

		m.deliverToLambda(ctx, sub.Endpoint, payload, input)
	}
}

// deliverToLambda invokes one lambda-protocol subscription, recording an SNS
// delivery/failure metric so a broken target is observable rather than a silent
// drop (mirrors deliverToSQS).
func (m *Mock) deliverToLambda(ctx context.Context, functionARN string, payload []byte, input *driver.PublishInput) {
	err := m.lambda.InvokeExternal(ctx, functionARN, payload)

	metric := metricNotificationsDelivered
	if err != nil {
		metric = metricNotificationsFailed
	}

	m.emitMetric(metric, 1, "Count", map[string]string{"TopicName": input.TopicID})
}

// deliverToSQS fans one message out to a single SQS subscription, carrying the
// publish's FIFO group/dedup ids so a FIFO queue accepts it. A delivery failure
// is recorded as an SNS metric rather than swallowed, so a real misconfiguration
// (e.g. a FIFO target without a group id) is observable instead of a silent drop.
func (m *Mock) deliverToSQS(ctx context.Context, queueARN, body string, input *driver.PublishInput) {
	err := m.sqs.DeliverExternalFIFO(ctx, queueARN, body, input.MessageGroupID, input.MessageDeduplicationID)

	metric := metricNotificationsDelivered
	if err != nil {
		metric = metricNotificationsFailed
	}

	m.emitMetric(metric, 1, "Count", map[string]string{"TopicName": input.TopicID})
}

// notificationEnvelope builds the SNS Notification JSON that wraps a published
// message for a non-raw SQS subscription.
func (m *Mock) notificationEnvelope(
	td *topicData, msgID string, input *driver.PublishInput, message, subARN string,
) (string, error) {
	body, err := json.Marshal(m.notificationEnvelopeMap(td, msgID, input, message, subARN))
	if err != nil {
		return "", err
	}

	return string(body), nil
}

// notificationEnvelopeMap builds the SNS Notification object (Type=Notification,
// MessageId, TopicArn, Message, Timestamp, optional Subject/MessageAttributes)
// shared by the SQS envelope and the Sns field of the Lambda Records event.
func (m *Mock) notificationEnvelopeMap(
	td *topicData, msgID string, input *driver.PublishInput, message, subARN string,
) map[string]any {
	env := map[string]any{
		"Type":             "Notification",
		"MessageId":        msgID,
		"TopicArn":         td.info.ResourceID,
		"Message":          message,
		"Timestamp":        m.opts.Clock.Now().UTC().Format(time.RFC3339),
		"SignatureVersion": "1",
		"Signature":        "Q2xvdWRFbXVFeGFtcGxlU2lnbmF0dXJl",
		"SigningCertURL": "https://sns." + arnRegion(td.info.ResourceID, m.opts.Region) +
			".amazonaws.com/SimpleNotificationService-cloudemu.pem",
		"UnsubscribeURL": "https://sns." + arnRegion(td.info.ResourceID, m.opts.Region) +
			".amazonaws.com/?Action=Unsubscribe&SubscriptionArn=" + subARN,
	}

	// Subject is optional: real SNS omits the field entirely when the message was
	// published without one, so consumers presence-check rather than see "".
	if input.Subject != "" {
		env["Subject"] = input.Subject
	}

	// Real SNS carries publish MessageAttributes into the SQS envelope as
	// {name: {"Type": <DataType>, "Value": v}}; preserve the DataType end-to-end.
	if len(input.AttributeEntries) > 0 || len(input.Attributes) > 0 {
		env["MessageAttributes"] = envelopeAttributes(input)
	}

	return env
}

// envelopeAttributes renders the publish's message attributes into the
// {name: {"Type": DataType, "Value": v}} shape SNS puts on the SQS envelope. It
// prefers the typed AttributeEntries (preserving Number/Binary), falling back to
// the flat string attributes as "String" for non-SNS publishes.
func envelopeAttributes(input *driver.PublishInput) map[string]any {
	if len(input.AttributeEntries) > 0 {
		out := make(map[string]any, len(input.AttributeEntries))
		for k, e := range input.AttributeEntries {
			out[k] = map[string]string{"Type": defaultDataType(e.DataType), "Value": e.Value}
		}

		return out
	}

	out := make(map[string]any, len(input.Attributes))
	for k, v := range input.Attributes {
		out[k] = map[string]string{"Type": messageAttrTypeString, "Value": v}
	}

	return out
}

// defaultDataType returns dt, or "String" when dt is empty.
func defaultDataType(dt string) string {
	if dt == "" {
		return messageAttrTypeString
	}

	return dt
}
