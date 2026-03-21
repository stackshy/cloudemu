// Package notificationhubs provides an in-memory mock implementation of Azure Notification Hubs.
package notificationhubs

import (
	"context"
	"fmt"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/internal/memstore"
	"github.com/stackshy/cloudemu/notification/driver"
)

// Compile-time check that Mock implements driver.Notification.
var _ driver.Notification = (*Mock)(nil)

type topicData struct {
	info          driver.TopicInfo
	subscriptions *memstore.Store[driver.SubscriptionInfo]
}

// Mock is an in-memory mock implementation of Azure Notification Hubs.
type Mock struct {
	topics *memstore.Store[*topicData]
	opts   *config.Options
}

// New creates a new Notification Hubs mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		topics: memstore.New[*topicData](),
		opts:   opts,
	}
}

// CreateTopic creates a new notification hub.
func (m *Mock) CreateTopic(_ context.Context, cfg driver.TopicConfig) (*driver.TopicInfo, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "topic name is required")
	}

	resourceID := idgen.AzureID(m.opts.AccountID, "rg-default", "Microsoft.NotificationHubs", "namespaces/default/notificationHubs", cfg.Name)

	if m.topics.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "topic %q already exists", cfg.Name)
	}

	tags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	info := driver.TopicInfo{
		ID:                idgen.GenerateID("topic-"),
		Name:              cfg.Name,
		ARN:               resourceID,
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

// DeleteTopic deletes a notification hub by name.
func (m *Mock) DeleteTopic(_ context.Context, id string) error {
	if !m.topics.Delete(id) {
		return errors.Newf(errors.NotFound, "topic %q not found", id)
	}

	return nil
}

// GetTopic retrieves information about a notification hub.
func (m *Mock) GetTopic(_ context.Context, id string) (*driver.TopicInfo, error) {
	td, ok := m.topics.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", id)
	}

	result := td.info
	result.SubscriptionCount = td.subscriptions.Len()

	return &result, nil
}

// ListTopics lists all notification hubs.
func (m *Mock) ListTopics(_ context.Context) ([]driver.TopicInfo, error) {
	all := m.topics.All()

	topics := make([]driver.TopicInfo, 0, len(all))

	for _, td := range all {
		info := td.info
		info.SubscriptionCount = td.subscriptions.Len()
		topics = append(topics, info)
	}

	return topics, nil
}

// Subscribe creates a subscription to a notification hub.
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
	resourceID := fmt.Sprintf("%s/subscriptions/%s", td.info.ARN, subID)

	sub := driver.SubscriptionInfo{
		ID:       resourceID,
		TopicID:  cfg.TopicID,
		Protocol: cfg.Protocol,
		Endpoint: cfg.Endpoint,
		Status:   "confirmed",
	}

	td.subscriptions.Set(resourceID, sub)

	result := sub

	return &result, nil
}

// Unsubscribe removes a subscription.
func (m *Mock) Unsubscribe(_ context.Context, subscriptionID string) error {
	all := m.topics.All()

	for _, td := range all {
		if td.subscriptions.Delete(subscriptionID) {
			return nil
		}
	}

	return errors.Newf(errors.NotFound, "subscription %q not found", subscriptionID)
}

// ListSubscriptions lists all subscriptions for a notification hub.
func (m *Mock) ListSubscriptions(_ context.Context, topicID string) ([]driver.SubscriptionInfo, error) {
	td, ok := m.topics.Get(topicID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", topicID)
	}

	all := td.subscriptions.All()

	subs := make([]driver.SubscriptionInfo, 0, len(all))
	for _, s := range all {
		subs = append(subs, s)
	}

	return subs, nil
}

// Publish publishes a message to a notification hub.
func (m *Mock) Publish(_ context.Context, input driver.PublishInput) (*driver.PublishOutput, error) {
	if _, ok := m.topics.Get(input.TopicID); !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", input.TopicID)
	}

	if input.Message == "" {
		return nil, errors.New(errors.InvalidArgument, "message is required")
	}

	msgID := idgen.GenerateID("msg-")

	return &driver.PublishOutput{MessageID: msgID}, nil
}
