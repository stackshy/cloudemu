package notificationhubs

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	"github.com/stackshy/cloudemu/notification/driver"
)

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()

	switch {
	case expectErr && err == nil:
		t.Fatal("expected error but got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		t.Error("expected non-empty string")
	}
}

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("eastus"))

	return New(opts)
}

func createTestTopic(t *testing.T, m *Mock, name string) *driver.TopicInfo {
	t.Helper()

	info, err := m.CreateTopic(context.Background(), driver.TopicConfig{Name: name})
	requireNoError(t, err)

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
			cfg:  driver.TopicConfig{Name: "my-hub"},
		},
		{
			name: "with display name",
			cfg:  driver.TopicConfig{Name: "alerts", DisplayName: "Alert Hub"},
		},
		{
			name: "with tags",
			cfg: driver.TopicConfig{
				Name: "tagged-hub",
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
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertNotEmpty(t, info.ID)
			assertNotEmpty(t, info.ARN)
			assertEqual(t, tc.cfg.Name, info.Name)
			assertEqual(t, tc.cfg.DisplayName, info.DisplayName)
			assertEqual(t, 0, info.SubscriptionCount)
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
	requireNoError(t, err)

	assertEqual(t, "staging", info.Tags["env"])
	assertEqual(t, "notifications", info.Tags["service"])

	// Verify tags are copied and not shared.
	tags["env"] = "production"
	assertEqual(t, "staging", info.Tags["env"])
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
			topicID:   "nonexistent-hub",
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
			assertError(t, err, tc.expectErr)
		})
	}
}

func TestDeleteTopicRemovesFromList(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	info := createTestTopic(t, m, "to-delete")

	err := m.DeleteTopic(ctx, info.Name)
	requireNoError(t, err)

	topics, err := m.ListTopics(ctx)
	requireNoError(t, err)
	assertEqual(t, 0, len(topics))
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
			topicID:   "nonexistent-hub",
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
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, "get-me", info.Name)
			assertEqual(t, "Get Me", info.DisplayName)
		})
	}
}

func TestGetTopicSubscriptionCount(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	topic := createTestTopic(t, m, "sub-count")

	info, err := m.GetTopic(ctx, topic.Name)
	requireNoError(t, err)
	assertEqual(t, 0, info.SubscriptionCount)

	_, err = m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID:  topic.Name,
		Protocol: "email",
		Endpoint: "a@b.com",
	})
	requireNoError(t, err)

	_, err = m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID:  topic.Name,
		Protocol: "sms",
		Endpoint: "+1234567890",
	})
	requireNoError(t, err)

	info, err = m.GetTopic(ctx, topic.Name)
	requireNoError(t, err)
	assertEqual(t, 2, info.SubscriptionCount)
}

func TestListTopics(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	topics, err := m.ListTopics(ctx)
	requireNoError(t, err)
	assertEqual(t, 0, len(topics))

	createTestTopic(t, m, "hub-1")
	createTestTopic(t, m, "hub-2")
	createTestTopic(t, m, "hub-3")

	topics, err = m.ListTopics(ctx)
	requireNoError(t, err)
	assertEqual(t, 3, len(topics))
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
				info := createTopicHelper(m, "sub-hub")
				return driver.SubscriptionConfig{
					TopicID: info.Name, Protocol: "email", Endpoint: "user@example.com",
				}
			},
		},
		{
			name: "sms subscription",
			setup: func(m *Mock) driver.SubscriptionConfig {
				info := createTopicHelper(m, "sms-hub")
				return driver.SubscriptionConfig{
					TopicID: info.Name, Protocol: "sms", Endpoint: "+1234567890",
				}
			},
		},
		{
			name: "nonexistent topic",
			cfg: driver.SubscriptionConfig{
				TopicID: "nonexistent-hub", Protocol: "email", Endpoint: "a@b.com",
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
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertNotEmpty(t, sub.ID)
			assertEqual(t, cfg.Protocol, sub.Protocol)
			assertEqual(t, cfg.Endpoint, sub.Endpoint)
			assertEqual(t, "confirmed", sub.Status)
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
	requireNoError(t, err)

	_, err = m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "sms", Endpoint: "+111",
	})
	requireNoError(t, err)

	_, err = m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "https", Endpoint: "https://hook.example.com",
	})
	requireNoError(t, err)

	subs, err := m.ListSubscriptions(ctx, topic.Name)
	requireNoError(t, err)
	assertEqual(t, 3, len(subs))
}

func TestUnsubscribe(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	topic := createTestTopic(t, m, "unsub-hub")

	sub, err := m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "email", Endpoint: "a@b.com",
	})
	requireNoError(t, err)

	t.Run("success", func(t *testing.T) {
		err := m.Unsubscribe(ctx, sub.ID)
		requireNoError(t, err)

		subs, err := m.ListSubscriptions(ctx, topic.Name)
		requireNoError(t, err)
		assertEqual(t, 0, len(subs))
	})

	t.Run("not found", func(t *testing.T) {
		err := m.Unsubscribe(ctx, "nonexistent-sub-id")
		assertError(t, err, true)
	})
}

func TestListSubscriptions(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	topic := createTestTopic(t, m, "list-subs")

	subs, err := m.ListSubscriptions(ctx, topic.Name)
	requireNoError(t, err)
	assertEqual(t, 0, len(subs))

	_, err = m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topic.Name, Protocol: "email", Endpoint: "x@y.com",
	})
	requireNoError(t, err)

	subs, err = m.ListSubscriptions(ctx, topic.Name)
	requireNoError(t, err)
	assertEqual(t, 1, len(subs))
}

func TestListSubscriptionsNonexistentTopic(t *testing.T) {
	m := newTestMock()

	_, err := m.ListSubscriptions(context.Background(), "nonexistent-hub")
	assertError(t, err, true)
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
				info := createTopicHelper(m, "pub-hub")
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
				info := createTopicHelper(m, "attr-hub")
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
				TopicID: "nonexistent-hub",
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
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertNotEmpty(t, out.MessageID)
		})
	}
}

func TestPublishReturnsUniqueMessageIDs(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	topic := createTestTopic(t, m, "unique-ids")

	out1, err := m.Publish(ctx, driver.PublishInput{TopicID: topic.Name, Message: "msg1"})
	requireNoError(t, err)

	out2, err := m.Publish(ctx, driver.PublishInput{TopicID: topic.Name, Message: "msg2"})
	requireNoError(t, err)

	if out1.MessageID == out2.MessageID {
		t.Error("expected unique message IDs")
	}
}
