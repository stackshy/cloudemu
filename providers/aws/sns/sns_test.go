package sns

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/notification/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	return New(opts)
}

func createTestTopic(t *testing.T, m *Mock, name string) *driver.TopicInfo {
	t.Helper()

	info, err := m.CreateTopic(context.Background(), driver.TopicConfig{Name: name})
	require.NoError(t, err)

	return info
}

func TestCreateTopic(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.TopicConfig
		setup     func(*Mock)
		expectErr bool
	}{
		{
			name: "basic topic",
			cfg:  driver.TopicConfig{Name: "my-topic"},
		},
		{
			name: "with display name",
			cfg:  driver.TopicConfig{Name: "alerts", DisplayName: "Alert Notifications"},
		},
		{
			name: "with tags",
			cfg: driver.TopicConfig{
				Name: "tagged-topic",
				Tags: map[string]string{"env": "prod", "team": "platform"},
			},
		},
		{
			name:      "empty name",
			cfg:       driver.TopicConfig{},
			expectErr: true,
		},
		{
			name: "duplicate topic",
			cfg:  driver.TopicConfig{Name: "dup"},
			setup: func(m *Mock) {
				_, _ = m.CreateTopic(context.Background(), driver.TopicConfig{Name: "dup"})
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			if tc.setup != nil {
				tc.setup(m)
			}

			info, err := m.CreateTopic(context.Background(), tc.cfg)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			assert.NotEmpty(t, info.ID)
			assert.NotEmpty(t, info.ResourceID)
			assert.Equal(t, tc.cfg.Name, info.Name)
			assert.Equal(t, tc.cfg.DisplayName, info.DisplayName)
			assert.Equal(t, 0, info.SubscriptionCount)
		})
	}
}

func TestCreateTopicWithTags(t *testing.T) {
	m := newTestMock()
	tags := map[string]string{"env": "staging", "service": "notifications"}

	info, err := m.CreateTopic(context.Background(), driver.TopicConfig{
		Name: "tagged",
		Tags: tags,
	})
	require.NoError(t, err)

	assert.Equal(t, "staging", info.Tags["env"])
	assert.Equal(t, "notifications", info.Tags["service"])

	// Verify tags are copied and not shared.
	tags["env"] = "production"
	assert.Equal(t, "staging", info.Tags["env"])
}

func TestDeleteTopic(t *testing.T) {
	tests := []struct {
		name      string
		topicID   string
		setup     func(*Mock) string
		expectErr bool
	}{
		{
			name: "success",
			setup: func(m *Mock) string {
				info, _ := m.CreateTopic(context.Background(), driver.TopicConfig{Name: "del"})
				return info.Name
			},
		},
		{
			name:      "not found",
			topicID:   "arn:aws:sns:us-east-1:123456789012:nonexistent",
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			id := tc.topicID

			if tc.setup != nil {
				id = tc.setup(m)
			}

			err := m.DeleteTopic(context.Background(), id)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestDeleteTopicRemovesFromList(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	info := createTestTopic(t, m, "to-delete")

	err := m.DeleteTopic(ctx, info.Name)
	require.NoError(t, err)

	topics, err := m.ListTopics(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(topics))
}

func TestGetTopic(t *testing.T) {
	tests := []struct {
		name      string
		topicID   string
		setup     func(*Mock) string
		expectErr bool
	}{
		{
			name: "success",
			setup: func(m *Mock) string {
				info, _ := m.CreateTopic(context.Background(), driver.TopicConfig{
					Name:        "get-me",
					DisplayName: "Get Me",
				})
				return info.Name
			},
		},
		{
			name:      "not found",
			topicID:   "arn:aws:sns:us-east-1:123456789012:nope",
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			id := tc.topicID

			if tc.setup != nil {
				id = tc.setup(m)
			}

			info, err := m.GetTopic(context.Background(), id)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			assert.Equal(t, "get-me", info.Name)
			assert.Equal(t, "Get Me", info.DisplayName)
		})
	}
}

func TestGetTopicSubscriptionCount(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	topic := createTestTopic(t, m, "sub-count")

	info, err := m.GetTopic(ctx, topic.Name)
	require.NoError(t, err)
	assert.Equal(t, 0, info.SubscriptionCount)

	_, err = m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID:  topic.Name,
		Protocol: "email",
		Endpoint: "a@b.com",
	})
	require.NoError(t, err)

	_, err = m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID:  topic.Name,
		Protocol: "sms",
		Endpoint: "+1234567890",
	})
	require.NoError(t, err)

	info, err = m.GetTopic(ctx, topic.Name)
	require.NoError(t, err)
	assert.Equal(t, 2, info.SubscriptionCount)
}

func TestListTopics(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	topics, err := m.ListTopics(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(topics))

	createTestTopic(t, m, "topic-1")
	createTestTopic(t, m, "topic-2")
	createTestTopic(t, m, "topic-3")

	topics, err = m.ListTopics(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, len(topics))
}

func TestSubscribe(t *testing.T) {
	tests := []struct {
		name      string
		cfg       driver.SubscriptionConfig
		setup     func(*Mock) driver.SubscriptionConfig
		expectErr bool
	}{
		{
			name: "email subscription",
			setup: func(m *Mock) driver.SubscriptionConfig {
				info := createTopicHelper(m, "sub-topic")
				return driver.SubscriptionConfig{
					TopicID: info.Name, Protocol: "email", Endpoint: "user@example.com",
				}
			},
		},
		{
			name: "sms subscription",
			setup: func(m *Mock) driver.SubscriptionConfig {
				info := createTopicHelper(m, "sms-topic")
				return driver.SubscriptionConfig{
					TopicID: info.Name, Protocol: "sms", Endpoint: "+1234567890",
				}
			},
		},
		{
			name: "nonexistent topic",
			cfg: driver.SubscriptionConfig{
				TopicID: "arn:aws:sns:us-east-1:123456789012:nope", Protocol: "email", Endpoint: "a@b.com",
			},
			expectErr: true,
		},
		{
			name: "empty protocol",
			setup: func(m *Mock) driver.SubscriptionConfig {
				info := createTopicHelper(m, "no-proto")
				return driver.SubscriptionConfig{
					TopicID: info.Name, Protocol: "", Endpoint: "a@b.com",
				}
			},
			expectErr: true,
		},
		{
			name: "empty endpoint",
			setup: func(m *Mock) driver.SubscriptionConfig {
				info := createTopicHelper(m, "no-endpoint")
				return driver.SubscriptionConfig{
					TopicID: info.Name, Protocol: "email", Endpoint: "",
				}
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			cfg := tc.cfg

			if tc.setup != nil {
				cfg = tc.setup(m)
			}

			sub, err := m.Subscribe(context.Background(), cfg)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			assert.NotEmpty(t, sub.ID)
			assert.Equal(t, cfg.Protocol, sub.Protocol)
			assert.Equal(t, cfg.Endpoint, sub.Endpoint)
			assert.Equal(t, "confirmed", sub.Status)
		})
	}
}

func createTopicHelper(m *Mock, name string) *driver.TopicInfo {
	info, _ := m.CreateTopic(context.Background(), driver.TopicConfig{Name: name})
	return info
}

func TestMultipleSubscriptionsSameTopic(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	topic := createTestTopic(t, m, "multi-sub")

	_, err := m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "email", Endpoint: "a@b.com",
	})
	require.NoError(t, err)

	_, err = m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "sms", Endpoint: "+111",
	})
	require.NoError(t, err)

	_, err = m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "https", Endpoint: "https://hook.example.com",
	})
	require.NoError(t, err)

	subs, err := m.ListSubscriptions(ctx, topic.Name)
	require.NoError(t, err)
	assert.Equal(t, 3, len(subs))
}

func TestUnsubscribe(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	topic := createTestTopic(t, m, "unsub-topic")

	sub, err := m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "email", Endpoint: "a@b.com",
	})
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := m.Unsubscribe(ctx, sub.ID)
		require.NoError(t, err)

		subs, err := m.ListSubscriptions(ctx, topic.Name)
		require.NoError(t, err)
		assert.Equal(t, 0, len(subs))
	})

	t.Run("not found", func(t *testing.T) {
		err := m.Unsubscribe(ctx, "arn:aws:sns:us-east-1:123456789012:subscription/nonexistent")
		require.Error(t, err)
	})
}

func TestListSubscriptions(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	topic := createTestTopic(t, m, "list-subs")

	subs, err := m.ListSubscriptions(ctx, topic.Name)
	require.NoError(t, err)
	assert.Equal(t, 0, len(subs))

	_, err = m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "email", Endpoint: "x@y.com",
	})
	require.NoError(t, err)

	subs, err = m.ListSubscriptions(ctx, topic.Name)
	require.NoError(t, err)
	assert.Equal(t, 1, len(subs))
}

func TestListSubscriptionsNonexistentTopic(t *testing.T) {
	m := newTestMock()

	_, err := m.ListSubscriptions(
		context.Background(),
		"arn:aws:sns:us-east-1:123456789012:nonexistent",
	)
	require.Error(t, err)
}

func TestPublish(t *testing.T) {
	tests := []struct {
		name      string
		input     driver.PublishInput
		setup     func(*Mock) driver.PublishInput
		expectErr bool
	}{
		{
			name: "success",
			setup: func(m *Mock) driver.PublishInput {
				info := createTopicHelper(m, "pub-topic")
				return driver.PublishInput{
					TopicID: info.Name,
					Message: "hello world",
					Subject: "greetings",
				}
			},
		},
		{
			name: "with attributes",
			setup: func(m *Mock) driver.PublishInput {
				info := createTopicHelper(m, "attr-topic")
				return driver.PublishInput{
					TopicID:    info.Name,
					Message:    "test",
					Attributes: map[string]string{"key": "value"},
				}
			},
		},
		{
			name: "nonexistent topic",
			input: driver.PublishInput{
				TopicID: "arn:aws:sns:us-east-1:123456789012:nope",
				Message: "hello",
			},
			expectErr: true,
		},
		{
			name: "empty message",
			setup: func(m *Mock) driver.PublishInput {
				info := createTopicHelper(m, "empty-msg")
				return driver.PublishInput{TopicID: info.Name, Message: ""}
			},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			input := tc.input

			if tc.setup != nil {
				input = tc.setup(m)
			}

			out, err := m.Publish(context.Background(), input)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			assert.NotEmpty(t, out.MessageID)
		})
	}
}

func TestPublishReturnsUniqueMessageIDs(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	topic := createTestTopic(t, m, "unique-ids")

	out1, err := m.Publish(ctx, driver.PublishInput{TopicID: topic.Name, Message: "msg1"})
	require.NoError(t, err)

	out2, err := m.Publish(ctx, driver.PublishInput{TopicID: topic.Name, Message: "msg2"})
	require.NoError(t, err)

	assert.NotEqual(t, out1.MessageID, out2.MessageID)
}
