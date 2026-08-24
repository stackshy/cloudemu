// Package sns provides an in-memory mock implementation of AWS Simple Notification Service.
package sns

import (
	"context"
	"encoding/json"
	"maps"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
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
type SQSDeliverer interface {
	DeliverExternal(ctx context.Context, queueARN, body string) error
}

// Mock is an in-memory mock implementation of the AWS SNS service.
type Mock struct {
	topics     *memstore.Store[*topicData]
	opts       *config.Options
	monitoring mondriver.Monitoring
	sqs        SQSDeliverer
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// SetSQSDeliverer wires the SQS backend so publishes fan out to SQS subscriptions.
func (m *Mock) SetSQSDeliverer(d SQSDeliverer) {
	m.sqs = d
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
func (m *Mock) CreateTopic(_ context.Context, cfg driver.TopicConfig) (*driver.TopicInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "topic name is required")
	}

	if m.topics.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "topic %q already exists", cfg.Name)
	}

	arn := idgen.AWSARN("sns", m.opts.Region, m.opts.AccountID, cfg.Name)

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	info := driver.TopicInfo{
		ID:                idgen.GenerateID("topic-"),
		Name:              cfg.Name,
		Scope:             cfg.Scope,
		ResourceID:        arn,
		DisplayName:       cfg.DisplayName,
		SubscriptionCount: 0,
		Tags:              tags,
	}

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

	if !cfg.Scope.IsZero() {
		td.info.Scope = cfg.Scope
	}

	m.topics.Set(cfg.Name, td)

	result := td.withCounts()

	return &result, nil
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

	subID := idgen.GenerateID("sub-")
	arn := idgen.AWSARN("sns", m.opts.Region, m.opts.AccountID, cfg.TopicID+":"+subID)

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
func (m *Mock) Publish(ctx context.Context, input driver.PublishInput) (*driver.PublishOutput, error) {
	td, ok := m.topics.Get(input.TopicID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", input.TopicID)
	}

	if input.Message == "" {
		return nil, errors.New(errors.InvalidArgument, "message is required")
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

	m.fanOutToSQS(ctx, td, msgID, input)

	dims := map[string]string{"TopicName": input.TopicID}
	m.emitMetric("NumberOfMessagesPublished", 1, "Count", dims)
	m.emitMetric("PublishSize", float64(len(input.Message)), "Bytes", dims)

	return &driver.PublishOutput{MessageID: msgID}, nil
}

// fanOutToSQS delivers a published message to every SQS-protocol subscription
// on the topic, wrapping it in the SNS notification envelope real SNS uses.
func (m *Mock) fanOutToSQS(ctx context.Context, td *topicData, msgID string, input driver.PublishInput) {
	if m.sqs == nil {
		return
	}

	for _, sub := range td.subscriptions.All() {
		if sub.Protocol != "sqs" || sub.Endpoint == "" {
			continue
		}

		// A filter policy gates delivery: the message reaches the subscriber
		// only when its attributes (or body) satisfy the policy.
		if !subscriptionAccepts(&sub, &input) {
			continue
		}

		// With raw message delivery enabled, SNS strips its metadata and sends
		// the published message body as-is instead of the Notification envelope.
		if rawDeliveryEnabled(&sub) {
			_ = m.sqs.DeliverExternal(ctx, sub.Endpoint, input.Message)

			continue
		}

		envelope, err := m.notificationEnvelope(td, msgID, input, sub.ID)
		if err != nil {
			continue
		}

		_ = m.sqs.DeliverExternal(ctx, sub.Endpoint, envelope)
	}
}

// notificationEnvelope builds the SNS Notification JSON that wraps a published
// message for a non-raw SQS subscription.
func (m *Mock) notificationEnvelope(td *topicData, msgID string, input driver.PublishInput, subARN string) (string, error) {
	env := map[string]any{
		"Type":             "Notification",
		"MessageId":        msgID,
		"TopicArn":         td.info.ResourceID,
		"Message":          input.Message,
		"Timestamp":        m.opts.Clock.Now().UTC().Format(time.RFC3339),
		"SignatureVersion": "1",
		"Signature":        "Q2xvdWRFbXVFeGFtcGxlU2lnbmF0dXJl",
		"SigningCertURL": "https://sns." + m.opts.Region +
			".amazonaws.com/SimpleNotificationService-cloudemu.pem",
		"UnsubscribeURL": "https://sns." + m.opts.Region +
			".amazonaws.com/?Action=Unsubscribe&SubscriptionArn=" + subARN,
	}

	// Subject is optional: real SNS omits the field entirely when the message was
	// published without one, so consumers presence-check rather than see "".
	if input.Subject != "" {
		env["Subject"] = input.Subject
	}

	// Real SNS carries publish MessageAttributes into the SQS envelope as
	// {name: {"Type": "String", "Value": v}}; preserve them end-to-end.
	if len(input.Attributes) > 0 {
		attrs := make(map[string]any, len(input.Attributes))
		for k, v := range input.Attributes {
			attrs[k] = map[string]string{"Type": "String", "Value": v}
		}

		env["MessageAttributes"] = attrs
	}

	body, err := json.Marshal(env)
	if err != nil {
		return "", err
	}

	return string(body), nil
}
