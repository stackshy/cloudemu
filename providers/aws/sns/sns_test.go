package sns

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
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
		{
			name:      "fifo topic without .fifo suffix is rejected",
			cfg:       driver.TopicConfig{Name: "not-fifo-named", FifoTopic: true},
			expectErr: true,
		},
		{
			name: "fifo topic with .fifo suffix",
			cfg:  driver.TopicConfig{Name: "properly-named.fifo", FifoTopic: true},
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

// TestUpdateTopicDisplayName guards the DisplayName path SetTopicAttributes
// uses (issue #319): UpdateTopic must change the display name in place.
func TestUpdateTopicDisplayName(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateTopic(ctx, driver.TopicConfig{Name: "t"}); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if _, err := m.UpdateTopic(ctx, driver.TopicConfig{Name: "t", DisplayName: "My Topic"}); err != nil {
		t.Fatalf("UpdateTopic: %v", err)
	}

	info, err := m.GetTopic(ctx, "t")
	require.NoError(t, err)
	assert.Equal(t, "My Topic", info.DisplayName)
}

// TestUpdateTopicFIFOAndDeliveryAttributes guards SetTopicAttributes's other
// paths through UpdateTopic (DeliveryPolicy, KmsMasterKeyId,
// ContentBasedDeduplication), which previously never made it past the wire
// handler and caused a permanent Terraform diff on aws_sns_topic's
// content_based_deduplication field.
func TestUpdateTopicFIFOAndDeliveryAttributes(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateTopic(ctx, driver.TopicConfig{Name: "t.fifo", FifoTopic: true})
	require.NoError(t, err)

	_, err = m.UpdateTopic(ctx, driver.TopicConfig{
		Name:                         "t.fifo",
		DeliveryPolicy:               `{"http":{"defaultHealthyRetryPolicy":{"numRetries":5}}}`,
		KmsMasterKeyID:               "alias/my-key",
		ContentBasedDeduplication:    true,
		ContentBasedDeduplicationSet: true,
	})
	require.NoError(t, err)

	info, err := m.GetTopic(ctx, "t.fifo")
	require.NoError(t, err)
	assert.Contains(t, info.DeliveryPolicy, "numRetries")
	assert.Equal(t, "alias/my-key", info.KmsMasterKeyID)
	assert.True(t, info.ContentBasedDeduplication)

	// An explicit false must also stick — this is exactly the bug: a plain
	// zero-value bool couldn't previously be distinguished from "not set".
	_, err = m.UpdateTopic(ctx, driver.TopicConfig{
		Name: "t.fifo", ContentBasedDeduplication: false, ContentBasedDeduplicationSet: true,
	})
	require.NoError(t, err)

	info, err = m.GetTopic(ctx, "t.fifo")
	require.NoError(t, err)
	assert.False(t, info.ContentBasedDeduplication)

	// UpdateTopic with ContentBasedDeduplicationSet left false (no attribute
	// named in the request) must leave the existing value untouched.
	_, err = m.UpdateTopic(ctx, driver.TopicConfig{Name: "t.fifo", DisplayName: "unrelated change"})
	require.NoError(t, err)

	info, err = m.GetTopic(ctx, "t.fifo")
	require.NoError(t, err)
	assert.False(t, info.ContentBasedDeduplication)
}

// TestUpdateTopicContentBasedDeduplicationRejectedOnStandardTopic guards real
// AWS's rule that ContentBasedDeduplication is only valid on a FIFO topic: a
// SetTopicAttributes call naming it on a standard topic must be rejected
// rather than silently accepted.
func TestUpdateTopicContentBasedDeduplicationRejectedOnStandardTopic(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateTopic(ctx, driver.TopicConfig{Name: "standard"})
	require.NoError(t, err)

	_, err = m.UpdateTopic(ctx, driver.TopicConfig{
		Name: "standard", ContentBasedDeduplication: true, ContentBasedDeduplicationSet: true,
	})
	require.Error(t, err)

	// The rejection must not have partially applied the attribute.
	info, err := m.GetTopic(ctx, "standard")
	require.NoError(t, err)
	assert.False(t, info.ContentBasedDeduplication)

	// The FIFO case must keep working: this is a regression guard, not a
	// blanket rejection of the attribute.
	_, err = m.CreateTopic(ctx, driver.TopicConfig{Name: "standard2.fifo", FifoTopic: true})
	require.NoError(t, err)

	_, err = m.UpdateTopic(ctx, driver.TopicConfig{
		Name: "standard2.fifo", ContentBasedDeduplication: true, ContentBasedDeduplicationSet: true,
	})
	require.NoError(t, err)
}

// TestTagUntagTopic is a regression guard for issue #319: SNS TagResource /
// UntagResource were unimplemented.
func TestTagUntagTopic(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateTopic(ctx, driver.TopicConfig{Name: "t"}); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	require.NoError(t, m.TagTopic(ctx, "t", map[string]string{"env": "prod", "team": "infra"}))

	got, err := m.ListTopicTags(ctx, "t")
	require.NoError(t, err)
	assert.Equal(t, "prod", got["env"])
	assert.Equal(t, "infra", got["team"])

	require.NoError(t, m.UntagTopic(ctx, "t", []string{"env"}))

	got, err = m.ListTopicTags(ctx, "t")
	require.NoError(t, err)
	_, has := got["env"]
	assert.False(t, has)
	assert.Equal(t, "infra", got["team"])

	assert.Error(t, m.TagTopic(ctx, "missing", map[string]string{"a": "b"}))
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

	topics, err := m.ListTopics(ctx, scope.Scope{})
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

	topics, err := m.ListTopics(ctx, scope.Scope{})
	require.NoError(t, err)
	assert.Equal(t, 0, len(topics))

	createTestTopic(t, m, "topic-1")
	createTestTopic(t, m, "topic-2")
	createTestTopic(t, m, "topic-3")

	topics, err = m.ListTopics(ctx, scope.Scope{})
	require.NoError(t, err)
	assert.Equal(t, 3, len(topics))
}

func TestSubscribe(t *testing.T) {
	tests := []struct {
		name       string
		cfg        driver.SubscriptionConfig
		setup      func(*Mock) driver.SubscriptionConfig
		wantStatus string
		expectErr  bool
	}{
		{
			name: "email subscription",
			setup: func(m *Mock) driver.SubscriptionConfig {
				info := createTopicHelper(m, "sub-topic")
				return driver.SubscriptionConfig{
					TopicID: info.Name, Protocol: "email", Endpoint: "user@example.com",
				}
			},
			// email requires out-of-band confirmation, so it starts pending.
			wantStatus: "pending",
		},
		{
			name: "sms subscription",
			setup: func(m *Mock) driver.SubscriptionConfig {
				info := createTopicHelper(m, "sms-topic")
				return driver.SubscriptionConfig{
					TopicID: info.Name, Protocol: "sms", Endpoint: "+1234567890",
				}
			},
			// SMS auto-confirms.
			wantStatus: "confirmed",
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
			assert.Equal(t, tc.wantStatus, sub.Status)
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

// TestSubscribeIdempotent guards real SNS's documented Subscribe semantics: a
// repeat Subscribe call with the same (TopicArn, Protocol, Endpoint) returns
// the existing subscription instead of creating a duplicate.
func TestSubscribeIdempotent(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	topic := createTestTopic(t, m, "idempotent-sub")

	first, err := m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "sqs", Endpoint: "arn:aws:sqs:us-east-1:123456789012:q",
	})
	require.NoError(t, err)

	second, err := m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "sqs", Endpoint: "arn:aws:sqs:us-east-1:123456789012:q",
	})
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID)

	subs, err := m.ListSubscriptions(ctx, topic.Name)
	require.NoError(t, err)
	assert.Len(t, subs, 1)
}

// TestSubscribeIdempotentPending guards the pending-confirmation case: a
// repeat Subscribe on a protocol that starts pending (e.g. email) returns the
// same pending subscription rather than minting a second confirmation token.
func TestSubscribeIdempotentPending(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	topic := createTestTopic(t, m, "idempotent-pending")

	first, err := m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "email", Endpoint: "dev@example.com",
	})
	require.NoError(t, err)
	assert.Equal(t, "pending", first.Status)

	second, err := m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "email", Endpoint: "dev@example.com",
	})
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, first.ConfirmationToken, second.ConfirmationToken)

	subs, err := m.ListSubscriptions(ctx, topic.Name)
	require.NoError(t, err)
	assert.Len(t, subs, 1)
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

// TestPublishFIFOValidation guards the two real-SNS FIFO Publish requirements:
// every message needs a MessageGroupId, and needs a MessageDeduplicationId
// unless the topic has ContentBasedDeduplication enabled. Standard topics are
// unaffected — MessageGroupId there is optional (forwarded to SQS standard
// subscriptions for fair-queue routing), never required or rejected.
func TestPublishFIFOValidation(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*Mock) driver.PublishInput
		expectErr bool
	}{
		{
			name: "fifo topic missing MessageGroupId",
			setup: func(m *Mock) driver.PublishInput {
				_, err := m.CreateTopic(context.Background(), driver.TopicConfig{
					Name: "grp.fifo", FifoTopic: true, ContentBasedDeduplication: true,
				})
				require.NoError(t, err)

				return driver.PublishInput{TopicID: "grp.fifo", Message: "hi"}
			},
			expectErr: true,
		},
		{
			name: "fifo topic missing MessageDeduplicationId without ContentBasedDeduplication",
			setup: func(m *Mock) driver.PublishInput {
				_, err := m.CreateTopic(context.Background(), driver.TopicConfig{
					Name: "dedup.fifo", FifoTopic: true,
				})
				require.NoError(t, err)

				return driver.PublishInput{TopicID: "dedup.fifo", Message: "hi", MessageGroupID: "g1"}
			},
			expectErr: true,
		},
		{
			name: "fifo topic with group and dedup id succeeds",
			setup: func(m *Mock) driver.PublishInput {
				_, err := m.CreateTopic(context.Background(), driver.TopicConfig{
					Name: "ok.fifo", FifoTopic: true,
				})
				require.NoError(t, err)

				return driver.PublishInput{
					TopicID: "ok.fifo", Message: "hi", MessageGroupID: "g1", MessageDeduplicationID: "d1",
				}
			},
		},
		{
			name: "fifo topic with ContentBasedDeduplication skips dedup id requirement",
			setup: func(m *Mock) driver.PublishInput {
				_, err := m.CreateTopic(context.Background(), driver.TopicConfig{
					Name: "cbd.fifo", FifoTopic: true, ContentBasedDeduplication: true,
				})
				require.NoError(t, err)

				return driver.PublishInput{TopicID: "cbd.fifo", Message: "hi", MessageGroupID: "g1"}
			},
		},
		{
			name: "standard topic accepts MessageGroupId without a dedup id",
			setup: func(m *Mock) driver.PublishInput {
				info := createTopicHelper(m, "std-with-group")
				return driver.PublishInput{TopicID: info.Name, Message: "hi", MessageGroupID: "g1"}
			},
		},
		{
			name: "standard topic requires neither MessageGroupId nor dedup id",
			setup: func(m *Mock) driver.PublishInput {
				info := createTopicHelper(m, "std-plain")
				return driver.PublishInput{TopicID: info.Name, Message: "hi"}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			input := tc.setup(m)

			_, err := m.Publish(context.Background(), input)
			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
		})
	}
}
