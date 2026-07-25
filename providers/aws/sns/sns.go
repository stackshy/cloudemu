// Package sns provides an in-memory mock implementation of AWS Simple Notification Service.
package sns

import (
	"context"
	"maps"
	"sync"

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
	mu            sync.RWMutex
}

// Mock is an in-memory mock implementation of the AWS SNS service.
type Mock struct {
	topics     *memstore.Store[*topicData]
	opts       *config.Options
	monitoring mondriver.Monitoring
}

// SetMonitoring sets the monitoring backend for auto-metric generation.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
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

	result := td.info
	result.SubscriptionCount = td.subscriptions.Len()

	return &result, nil
}

// ListTopics lists all SNS topics visible under the given scope filter.
func (m *Mock) ListTopics(_ context.Context, filter scope.Scope) ([]driver.TopicInfo, error) {
	all := m.topics.SortedValues()

	topics := make([]driver.TopicInfo, 0, len(all))

	for _, td := range all {
		if !td.info.Scope.Matches(filter) {
			continue
		}

		info := td.info
		info.SubscriptionCount = td.subscriptions.Len()
		topics = append(topics, info)
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
	if !cfg.Scope.IsZero() {
		td.info.Scope = cfg.Scope
	}

	m.topics.Set(cfg.Name, td)

	result := td.info
	result.SubscriptionCount = td.subscriptions.Len()

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

	if cfg.Endpoint == "" {
		return nil, errors.New(errors.InvalidArgument, "endpoint is required")
	}

	subID := idgen.GenerateID("sub-")
	arn := idgen.AWSARN("sns", m.opts.Region, m.opts.AccountID, "subscription/"+subID)

	sub := driver.SubscriptionInfo{
		ID:       arn,
		TopicID:  cfg.TopicID,
		Protocol: cfg.Protocol,
		Endpoint: cfg.Endpoint,
		Status:   "confirmed",
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
func (m *Mock) Publish(_ context.Context, input driver.PublishInput) (*driver.PublishOutput, error) {
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

	dims := map[string]string{"TopicName": input.TopicID}
	m.emitMetric("NumberOfMessagesPublished", 1, "Count", dims)
	m.emitMetric("PublishSize", float64(len(input.Message)), "Bytes", dims)

	return &driver.PublishOutput{MessageID: msgID}, nil
}
