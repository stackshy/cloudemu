package notifications_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/oci/notifications"
	"github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const (
	compartment      = "ocid1.compartment.oc1..aaaaaaaatest"
	otherCompartment = "ocid1.compartment.oc1..aaaaaaaaother"
	region           = "us-ashburn-1"
)

func newMock(t *testing.T) *notifications.Mock {
	t.Helper()

	return notifications.New(config.NewOptions(
		config.WithRegion(region),
		config.WithCompartmentID(compartment),
	))
}

// newTopic creates a topic in the given compartment and returns its OCID.
func newTopic(t *testing.T, m *notifications.Mock, name, compartmentID string) string {
	t.Helper()

	info, err := m.CreateTopic(context.Background(), driver.TopicConfig{
		Name:        name,
		DisplayName: "topic " + name,
		Scope:       scope.Scope{Compartment: compartmentID},
	})
	require.NoError(t, err)

	return info.ID
}

func TestCreateTopic(t *testing.T) {
	tests := []struct {
		name      string
		topicName string
		twice     bool
		code      cerrors.Code
	}{
		{name: "valid name", topicName: "alerts"},
		{name: "dashes and underscores", topicName: "prod-alerts_v2"},
		{name: "empty name", topicName: "", code: cerrors.InvalidArgument},
		{name: "illegal characters", topicName: "prod alerts!", code: cerrors.InvalidArgument},
		{name: "too long", topicName: strings.Repeat("a", 257), code: cerrors.InvalidArgument},
		{name: "duplicate in compartment", topicName: "alerts", twice: true, code: cerrors.AlreadyExists},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMock(t)

			if tc.twice {
				_, err := m.CreateTopic(t.Context(), driver.TopicConfig{Name: tc.topicName})
				require.NoError(t, err)
			}

			info, err := m.CreateTopic(t.Context(), driver.TopicConfig{
				Name:        tc.topicName,
				DisplayName: "the topic",
				Tags:        map[string]string{"env": "prod"},
			})

			if tc.code != cerrors.OK {
				require.Error(t, err)
				assert.Equal(t, tc.code, cerrors.GetCode(err))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.topicName, info.Name)
			assert.Equal(t, "the topic", info.DisplayName)
			assert.Equal(t, compartment, info.Scope.Compartment)
			assert.Equal(t, info.ID, info.ResourceID)
			assert.Equal(t, map[string]string{"env": "prod"}, info.Tags)
		})
	}
}

func TestTopicOCIDShape(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	assert.True(t, strings.HasPrefix(topicID, "ocid1.onstopic.oc1.iad."), topicID)

	sub, err := m.CreateSubscription(t.Context(), notifications.SubscriptionSpec{
		TopicID: topicID, Protocol: "EMAIL", Endpoint: "ops@example.com",
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(sub.ID, "ocid1.onssubscription.oc1.iad."), sub.ID)
}

func TestTopicDetails(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	details, ok := m.TopicDetails(topicID)
	require.True(t, ok)
	assert.Equal(t, notifications.StateActive, details.LifecycleState)
	assert.NotEmpty(t, details.TimeCreated)
	assert.NotEmpty(t, details.Etag)
	assert.Len(t, details.ShortTopicID, 8)

	_, ok = m.TopicDetails("ocid1.onstopic.oc1.iad.missing")
	assert.False(t, ok)
}

func TestTopicLifecycle(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	got, err := m.GetTopic(t.Context(), topicID)
	require.NoError(t, err)
	assert.Equal(t, "alerts", got.Name)

	updated, err := m.UpdateTopic(t.Context(), driver.TopicConfig{
		Name:        topicID,
		DisplayName: "renamed description",
		Tags:        map[string]string{"team": "sre"},
	})
	require.NoError(t, err)
	assert.Equal(t, "alerts", updated.Name, "ONS does not rename a topic")
	assert.Equal(t, "renamed description", updated.DisplayName)
	assert.Equal(t, map[string]string{"team": "sre"}, updated.Tags)

	byName, err := m.UpdateTopic(t.Context(), driver.TopicConfig{Name: "alerts", DisplayName: "by name"})
	require.NoError(t, err)
	assert.Equal(t, topicID, byName.ID)

	require.NoError(t, m.DeleteTopic(t.Context(), topicID))

	_, err = m.GetTopic(t.Context(), topicID)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestTopicNotFound(t *testing.T) {
	const missing = "ocid1.onstopic.oc1.iad.missing"

	m := newMock(t)

	tests := []struct {
		name string
		call func() error
	}{
		{name: "get", call: func() error { _, err := m.GetTopic(t.Context(), missing); return err }},
		{name: "delete", call: func() error { return m.DeleteTopic(t.Context(), missing) }},
		{
			name: "update",
			call: func() error {
				_, err := m.UpdateTopic(t.Context(), driver.TopicConfig{Name: missing})

				return err
			},
		},
		{
			name: "subscribe",
			call: func() error {
				_, err := m.Subscribe(t.Context(), driver.SubscriptionConfig{
					TopicID: missing, Protocol: "EMAIL", Endpoint: "a@b.c",
				})

				return err
			},
		},
		{
			name: "list subscriptions",
			call: func() error { _, err := m.ListSubscriptions(t.Context(), missing); return err },
		},
		{
			name: "publish",
			call: func() error {
				_, err := m.Publish(t.Context(), driver.PublishInput{TopicID: missing, Message: "hi"})

				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, cerrors.NotFound, cerrors.GetCode(tc.call()))
		})
	}
}

func TestListTopicsFiltersByCompartment(t *testing.T) {
	m := newMock(t)
	newTopic(t, m, "mine", compartment)
	newTopic(t, m, "theirs", otherCompartment)

	tests := []struct {
		name   string
		filter scope.Scope
		expect []string
	}{
		{name: "own compartment", filter: scope.Scope{Compartment: compartment}, expect: []string{"mine"}},
		{name: "other compartment", filter: scope.Scope{Compartment: otherCompartment}, expect: []string{"theirs"}},
		{
			name:   "unknown compartment",
			filter: scope.Scope{Compartment: "ocid1.compartment.oc1..aaaaaaaanone"},
			expect: []string{},
		},
		{name: "unscoped lists all", filter: scope.Scope{}, expect: []string{"mine", "theirs"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			topics, err := m.ListTopics(t.Context(), tc.filter)
			require.NoError(t, err)

			names := make([]string, 0, len(topics))
			for _, topic := range topics {
				names = append(names, topic.Name)
			}

			assert.ElementsMatch(t, tc.expect, names)
		})
	}
}

func TestSameTopicNameInAnotherCompartment(t *testing.T) {
	m := newMock(t)
	first := newTopic(t, m, "alerts", compartment)
	second := newTopic(t, m, "alerts", otherCompartment)

	assert.NotEqual(t, first, second)
}

func TestCreateSubscription(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	tests := []struct {
		name     string
		protocol string
		endpoint string
		expect   string
		code     cerrors.Code
	}{
		{name: "email", protocol: "EMAIL", endpoint: "ops@example.com", expect: notifications.ProtocolEmail},
		{name: "lowercase alias", protocol: "email", endpoint: "a@example.com", expect: notifications.ProtocolEmail},
		{name: "https alias", protocol: "https", endpoint: "https://hook", expect: notifications.ProtocolHTTPS},
		{name: "sms", protocol: "SMS", endpoint: "+15550100", expect: notifications.ProtocolSMS},
		{name: "unknown protocol", protocol: "sqs", endpoint: "q", code: cerrors.InvalidArgument},
		{name: "missing protocol", protocol: "", endpoint: "q", code: cerrors.InvalidArgument},
		{name: "missing endpoint", protocol: "EMAIL", endpoint: "", code: cerrors.InvalidArgument},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sub, err := m.CreateSubscription(t.Context(), notifications.SubscriptionSpec{
				TopicID: topicID, Protocol: tc.protocol, Endpoint: tc.endpoint,
			})

			if tc.code != cerrors.OK {
				require.Error(t, err)
				assert.Equal(t, tc.code, cerrors.GetCode(err))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expect, sub.Protocol)
			assert.Equal(t, notifications.StatePending, sub.LifecycleState)
			assert.NotEmpty(t, sub.ConfirmationToken)
			assert.Equal(t, compartment, sub.CompartmentID)
		})
	}
}

func TestDuplicateSubscription(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	spec := notifications.SubscriptionSpec{TopicID: topicID, Protocol: "EMAIL", Endpoint: "ops@example.com"}

	_, err := m.CreateSubscription(t.Context(), spec)
	require.NoError(t, err)

	_, err = m.CreateSubscription(t.Context(), spec)
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))
}

// TestConfirmationFlow is the PENDING -> ACTIVE transition ONS puts in front
// of delivery, and the guarantee that nothing reaches an unconfirmed endpoint.
func TestConfirmationFlow(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	sub, err := m.CreateSubscription(t.Context(), notifications.SubscriptionSpec{
		TopicID: topicID, Protocol: "EMAIL", Endpoint: "ops@example.com",
	})
	require.NoError(t, err)
	require.Equal(t, notifications.StatePending, sub.LifecycleState)

	_, err = m.PublishMessage(t.Context(), topicID, notifications.MessageSpec{Body: "before confirmation"})
	require.NoError(t, err)
	assert.Empty(t, m.Deliveries(sub.ID), "a PENDING subscription must receive nothing")

	subs, err := m.ListSubscriptions(t.Context(), topicID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, notifications.StatusPending, subs[0].Status)

	result, err := m.ConfirmSubscription(t.Context(), sub.ID, sub.ConfirmationToken, "EMAIL")
	require.NoError(t, err)
	assert.Equal(t, "alerts", result.TopicName)
	assert.Equal(t, sub.ID, result.SubscriptionID)

	confirmed, err := m.GetSubscription(t.Context(), sub.ID)
	require.NoError(t, err)
	assert.Equal(t, notifications.StateActive, confirmed.LifecycleState)

	subs, err = m.ListSubscriptions(t.Context(), topicID)
	require.NoError(t, err)
	assert.Equal(t, notifications.StatusConfirmed, subs[0].Status)

	msg, err := m.PublishMessage(t.Context(), topicID, notifications.MessageSpec{
		Title: "disk", Body: "after confirmation",
	})
	require.NoError(t, err)

	delivered := m.Deliveries(sub.ID)
	require.Len(t, delivered, 1)
	assert.Equal(t, msg.ID, delivered[0].ID)
	assert.Equal(t, "after confirmation", delivered[0].Body)
	assert.Equal(t, notifications.MessageTypeRawText, delivered[0].Type)
}

func TestConfirmSubscriptionErrors(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	sub, err := m.CreateSubscription(t.Context(), notifications.SubscriptionSpec{
		TopicID: topicID, Protocol: "EMAIL", Endpoint: "ops@example.com",
	})
	require.NoError(t, err)

	tests := []struct {
		name     string
		id       string
		token    string
		protocol string
		code     cerrors.Code
	}{
		{name: "wrong token", id: sub.ID, token: "nope", code: cerrors.InvalidArgument},
		{name: "missing token", id: sub.ID, token: "", code: cerrors.InvalidArgument},
		{
			name: "mismatched protocol", id: sub.ID, token: sub.ConfirmationToken,
			protocol: "SMS", code: cerrors.InvalidArgument,
		},
		{
			name: "unknown subscription", id: "ocid1.onssubscription.oc1.iad.missing",
			token: sub.ConfirmationToken, code: cerrors.NotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.ConfirmSubscription(t.Context(), tc.id, tc.token, tc.protocol)
			require.Error(t, err)
			assert.Equal(t, tc.code, cerrors.GetCode(err))
		})
	}
}

func TestResendConfirmation(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	sub, err := m.CreateSubscription(t.Context(), notifications.SubscriptionSpec{
		TopicID: topicID, Protocol: "EMAIL", Endpoint: "ops@example.com",
	})
	require.NoError(t, err)

	resent, err := m.ResendSubscriptionConfirmation(t.Context(), sub.ID)
	require.NoError(t, err)
	assert.NotEqual(t, sub.ConfirmationToken, resent.ConfirmationToken)

	_, err = m.ConfirmSubscription(t.Context(), sub.ID, resent.ConfirmationToken, "")
	require.NoError(t, err)

	_, err = m.ResendSubscriptionConfirmation(t.Context(), sub.ID)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
}

func TestUnsubscribe(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	sub, err := m.Subscribe(t.Context(), driver.SubscriptionConfig{
		TopicID: topicID, Protocol: "email", Endpoint: "ops@example.com",
	})
	require.NoError(t, err)
	assert.Equal(t, notifications.StatusPending, sub.Status)

	require.NoError(t, m.Unsubscribe(t.Context(), sub.ID))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.Unsubscribe(t.Context(), sub.ID)))
}

func TestUnsubscribeByToken(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	sub, err := m.CreateSubscription(t.Context(), notifications.SubscriptionSpec{
		TopicID: topicID, Protocol: "EMAIL", Endpoint: "ops@example.com",
	})
	require.NoError(t, err)

	err = m.UnsubscribeByToken(t.Context(), sub.ID, "wrong", "")
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	require.NoError(t, m.UnsubscribeByToken(t.Context(), sub.ID, sub.ConfirmationToken, "EMAIL"))

	_, err = m.GetSubscription(t.Context(), sub.ID)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestListOCISubscriptions(t *testing.T) {
	m := newMock(t)
	mine := newTopic(t, m, "mine", compartment)
	theirs := newTopic(t, m, "theirs", otherCompartment)

	_, err := m.CreateSubscription(t.Context(), notifications.SubscriptionSpec{
		TopicID: mine, Protocol: "EMAIL", Endpoint: "a@example.com",
	})
	require.NoError(t, err)

	_, err = m.CreateSubscription(t.Context(), notifications.SubscriptionSpec{
		TopicID: theirs, CompartmentID: otherCompartment, Protocol: "EMAIL", Endpoint: "b@example.com",
	})
	require.NoError(t, err)

	tests := []struct {
		name        string
		compartment string
		topicID     string
		expect      int
	}{
		{name: "own compartment", compartment: compartment, expect: 1},
		{name: "other compartment", compartment: otherCompartment, expect: 1},
		{name: "narrowed to topic", compartment: compartment, topicID: mine, expect: 1},
		{name: "topic in another compartment", compartment: compartment, topicID: theirs, expect: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subs, err := m.ListOCISubscriptions(t.Context(), tc.compartment, tc.topicID)
			require.NoError(t, err)
			assert.Len(t, subs, tc.expect)
		})
	}
}

func TestUpdateAndMoveSubscription(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	sub, err := m.CreateSubscription(t.Context(), notifications.SubscriptionSpec{
		TopicID: topicID, Protocol: "https", Endpoint: "https://hook",
	})
	require.NoError(t, err)

	updated, err := m.UpdateSubscription(t.Context(), sub.ID, notifications.SubscriptionPatch{
		DeliveryPolicy: &notifications.DeliveryPolicy{
			BackoffRetryPolicy: &notifications.BackoffRetryPolicy{MaxRetryDuration: 7200, PolicyType: "EXPONENTIAL"},
		},
		FreeformTags: map[string]string{"team": "sre"},
	})
	require.NoError(t, err)
	require.NotNil(t, updated.DeliveryPolicy)
	assert.Equal(t, 7200, updated.DeliveryPolicy.BackoffRetryPolicy.MaxRetryDuration)
	assert.Equal(t, map[string]string{"team": "sre"}, updated.FreeformTags)

	require.NoError(t, m.ChangeSubscriptionCompartment(t.Context(), sub.ID, otherCompartment))

	moved, err := m.GetSubscription(t.Context(), sub.ID)
	require.NoError(t, err)
	assert.Equal(t, otherCompartment, moved.CompartmentID)

	_, err = m.UpdateSubscription(t.Context(), "ocid1.onssubscription.oc1.iad.missing",
		notifications.SubscriptionPatch{})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestDeleteTopicRemovesItsSubscriptions(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)
	other := newTopic(t, m, "keep", compartment)

	doomed, err := m.CreateSubscription(t.Context(), notifications.SubscriptionSpec{
		TopicID: topicID, Protocol: "EMAIL", Endpoint: "a@example.com",
	})
	require.NoError(t, err)

	survivor, err := m.CreateSubscription(t.Context(), notifications.SubscriptionSpec{
		TopicID: other, Protocol: "EMAIL", Endpoint: "b@example.com",
	})
	require.NoError(t, err)

	require.NoError(t, m.DeleteTopic(t.Context(), topicID))

	_, err = m.GetSubscription(t.Context(), doomed.ID)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.GetSubscription(t.Context(), survivor.ID)
	assert.NoError(t, err)
}

func TestPublish(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	tests := []struct {
		name  string
		input driver.PublishInput
		code  cerrors.Code
	}{
		{name: "ok", input: driver.PublishInput{TopicID: topicID, Subject: "s", Message: "hello"}},
		{name: "empty message", input: driver.PublishInput{TopicID: topicID}, code: cerrors.InvalidArgument},
		{
			name: "attributes are refused, not dropped",
			input: driver.PublishInput{
				TopicID: topicID, Message: "hello", Attributes: map[string]string{"k": "v"},
			},
			code: cerrors.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := m.Publish(t.Context(), tc.input)

			if tc.code != cerrors.OK {
				require.Error(t, err)
				assert.Equal(t, tc.code, cerrors.GetCode(err))

				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, out.MessageID)
		})
	}
}

func TestPublishMessageType(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	tests := []struct {
		name    string
		msgType string
		expect  string
		code    cerrors.Code
	}{
		{name: "default", msgType: "", expect: notifications.MessageTypeRawText},
		{name: "raw text", msgType: "RAW_TEXT", expect: notifications.MessageTypeRawText},
		{name: "json", msgType: "JSON", expect: notifications.MessageTypeJSON},
		{name: "unknown", msgType: "PROTOBUF", code: cerrors.InvalidArgument},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := m.PublishMessage(t.Context(), topicID,
				notifications.MessageSpec{Body: "hello", Type: tc.msgType})

			if tc.code != cerrors.OK {
				require.Error(t, err)
				assert.Equal(t, tc.code, cerrors.GetCode(err))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expect, msg.Type)
		})
	}
}

func TestSubscriptionCountTracksTopic(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	info, err := m.GetTopic(t.Context(), topicID)
	require.NoError(t, err)
	assert.Equal(t, 0, info.SubscriptionCount)

	_, err = m.Subscribe(t.Context(), driver.SubscriptionConfig{
		TopicID: topicID, Protocol: "EMAIL", Endpoint: "a@example.com",
	})
	require.NoError(t, err)

	info, err = m.GetTopic(t.Context(), topicID)
	require.NoError(t, err)
	assert.Equal(t, 1, info.SubscriptionCount)
}

// TestConcurrentUse exercises the mutex under -race: every exported method
// locks, and none of them calls another that locks.
func TestConcurrentUse(t *testing.T) {
	m := newMock(t)
	topicID := newTopic(t, m, "alerts", compartment)

	done := make(chan struct{})

	for i := range 8 {
		go func(i int) {
			defer func() { done <- struct{}{} }()

			ctx := context.Background()

			sub, err := m.CreateSubscription(ctx, notifications.SubscriptionSpec{
				TopicID: topicID, Protocol: "EMAIL", Endpoint: string(rune('a'+i)) + "@example.com",
			})
			if err != nil {
				return
			}

			_, _ = m.ConfirmSubscription(ctx, sub.ID, sub.ConfirmationToken, "EMAIL")
			_, _ = m.PublishMessage(ctx, topicID, notifications.MessageSpec{Body: "hello"})
			_, _ = m.ListOCISubscriptions(ctx, compartment, topicID)
			_, _ = m.ListTopics(ctx, scope.Scope{Compartment: compartment})
			m.Deliveries(sub.ID)
		}(i)
	}

	for range 8 {
		<-done
	}
}
