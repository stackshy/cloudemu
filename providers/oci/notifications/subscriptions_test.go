package notifications_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/oci/notifications"
	"github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const missingTopic = "ocid1.onstopic.oc1..missing"

// newPendingSubscription creates a PENDING subscription and returns it.
func newPendingSubscription(t *testing.T, m *notifications.Mock, topicID string) *notifications.Subscription {
	t.Helper()

	sub, err := m.CreateSubscription(context.Background(), notifications.SubscriptionSpec{
		TopicID:       topicID,
		CompartmentID: compartment,
		Protocol:      "EMAIL",
		Endpoint:      "ops@example.com",
	})
	require.NoError(t, err)

	return sub
}

func TestUpdateUnknownTopic(t *testing.T) {
	_, err := newMock(t).UpdateTopic(context.Background(), driver.TopicConfig{
		Name:  missingTopic,
		Scope: scope.Scope{Compartment: compartment},
	})
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestListSubscriptionsOfAnUnknownTopic(t *testing.T) {
	_, err := newMock(t).ListSubscriptions(context.Background(), missingTopic)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestUnsubscribeByTokenErrors(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	topicID := newTopic(t, m, "alpha", compartment)
	sub := newPendingSubscription(t, m, topicID)

	tests := map[string]struct {
		id, token, protocol string
		code                cerrors.Code
	}{
		"no token":       {sub.ID, "", "EMAIL", cerrors.InvalidArgument},
		"no protocol":    {sub.ID, sub.ConfirmationToken, "", cerrors.InvalidArgument},
		"unknown id":     {"ocid1.onssubscription.oc1..missing", sub.ConfirmationToken, "EMAIL", cerrors.NotFound},
		"wrong token":    {sub.ID, "token-wrong", "EMAIL", cerrors.InvalidArgument},
		"bad protocol":   {sub.ID, sub.ConfirmationToken, "CARRIER_PIGEON", cerrors.InvalidArgument},
		"other protocol": {sub.ID, sub.ConfirmationToken, "SMS", cerrors.InvalidArgument},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := m.UnsubscribeByToken(ctx, tc.id, tc.token, tc.protocol)
			require.Error(t, err)
			assert.Equal(t, tc.code, cerrors.GetCode(err))
		})
	}

	// The token and its protocol unsubscribe. Removes the subscription, so it
	// runs after the rejection cases.
	require.NoError(t, m.UnsubscribeByToken(ctx, sub.ID, sub.ConfirmationToken, "EMAIL"))
	assert.Empty(t, m.Deliveries(sub.ID))
}

// A confirmed subscription has no token left to re-issue.
func TestResendConfirmationErrors(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	topicID := newTopic(t, m, "alpha", compartment)
	sub := newPendingSubscription(t, m, topicID)

	_, err := m.ResendSubscriptionConfirmation(ctx, "ocid1.onssubscription.oc1..missing")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.ConfirmSubscription(ctx, sub.ID, sub.ConfirmationToken, "EMAIL")
	require.NoError(t, err)

	_, err = m.ResendSubscriptionConfirmation(ctx, sub.ID)
	require.Error(t, err)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
}

func TestChangeSubscriptionCompartmentErrors(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	topicID := newTopic(t, m, "alpha", compartment)
	sub := newPendingSubscription(t, m, topicID)

	require.Error(t, m.ChangeSubscriptionCompartment(ctx, sub.ID, ""))
	assert.Equal(t, cerrors.InvalidArgument,
		cerrors.GetCode(m.ChangeSubscriptionCompartment(ctx, sub.ID, "")))

	err := m.ChangeSubscriptionCompartment(ctx, "ocid1.onssubscription.oc1..missing", otherCompartment)
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

// The endpoint shape each protocol delivers to is checked at create, naming
// what is wrong rather than failing the first delivery.
func TestCreateSubscriptionEndpointValidation(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	topicID := newTopic(t, m, "alpha", compartment)

	tests := map[string]struct {
		protocol, endpoint string
		wantErr            bool
	}{
		"email":               {"EMAIL", "ops@example.com", false},
		"email without an at": {"EMAIL", "ops-example.com", true},
		"https":               {"CUSTOM_HTTPS", "https://hooks.example.com/x", false},
		"https over http":     {"CUSTOM_HTTPS", "http://hooks.example.com/x", true},
		"slack over http":     {"SLACK", "http://hooks.slack.com/x", true},
		"pagerduty https":     {"PAGERDUTY", "https://events.pagerduty.com/x", false},
		"sms is unchecked":    {"SMS", "+15550100", false},
		"empty":               {"EMAIL", "", true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := m.CreateSubscription(ctx, notifications.SubscriptionSpec{
				TopicID: topicID, CompartmentID: compartment,
				Protocol: tc.protocol, Endpoint: tc.endpoint,
			})
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
		})
	}
}

// ONS does not ask a function subscription to confirm: it starts ACTIVE with no
// token and receives the very next publish.
func TestOracleFunctionsSubscriptionStartsActive(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	topicID := newTopic(t, m, "alpha", compartment)

	sub, err := m.CreateSubscription(ctx, notifications.SubscriptionSpec{
		TopicID: topicID, CompartmentID: compartment,
		Protocol: notifications.ProtocolFunctions, Endpoint: "ocid1.fnfunc.oc1..x",
	})
	require.NoError(t, err)
	assert.Equal(t, notifications.StateActive, sub.LifecycleState)
	assert.Empty(t, sub.ConfirmationToken)

	_, err = m.PublishMessage(ctx, topicID, notifications.MessageSpec{Body: "hello"})
	require.NoError(t, err)
	require.Len(t, m.Deliveries(sub.ID), 1)

	// There is no confirmation to resend.
	_, err = m.ResendSubscriptionConfirmation(ctx, sub.ID)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	// The portable entry point reports it confirmed, not pending.
	info, err := m.Subscribe(ctx, driver.SubscriptionConfig{
		TopicID: topicID, Protocol: "ORACLE_FUNCTIONS", Endpoint: "ocid1.fnfunc.oc1..y",
	})
	require.NoError(t, err)
	assert.Equal(t, notifications.StatusConfirmed, info.Status)
}

// The if-match precondition is compared under the same lock as the write, so of
// several writers holding the same etag exactly one wins.
func TestUpdateTopicIfMatchHasOneWinner(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	id := newTopic(t, m, "alpha", compartment)

	details, ok := m.TopicDetails(id)
	require.True(t, ok)

	const writers = 8

	var (
		wg       sync.WaitGroup
		won      atomic.Int32
		rejected atomic.Int32
	)

	for i := range writers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, err := m.UpdateTopicIfMatch(ctx, driver.TopicConfig{
				Name: id, DisplayName: "writer " + strconv.Itoa(i),
			}, details.Etag)

			switch {
			case err == nil:
				won.Add(1)
			case errors.Is(err, notifications.ErrNoEtagMatch):
				rejected.Add(1)
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int32(1), won.Load())
	assert.Equal(t, int32(writers-1), rejected.Load())
}

// A stale etag is refused on every guarded write and leaves the resource alone.
func TestStaleIfMatchIsRefused(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	topicID := newTopic(t, m, "alpha", compartment)
	sub := newPendingSubscription(t, m, topicID)

	err := m.DeleteTopicIfMatch(ctx, topicID, "etag-stale")
	require.ErrorIs(t, err, notifications.ErrNoEtagMatch)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	_, err = m.UpdateSubscription(ctx, sub.ID, notifications.SubscriptionPatch{
		FreeformTags: map[string]string{"team": "ops"}, IfMatch: "etag-stale",
	})
	require.ErrorIs(t, err, notifications.ErrNoEtagMatch)

	require.ErrorIs(t, m.DeleteSubscription(ctx, sub.ID, "etag-stale"), notifications.ErrNoEtagMatch)

	// Nothing changed, and the current etag still works.
	got, err := m.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.Empty(t, got.FreeformTags)
	require.NoError(t, m.DeleteSubscription(ctx, sub.ID, got.Etag))

	details, ok := m.TopicDetails(topicID)
	require.True(t, ok)
	require.NoError(t, m.DeleteTopicIfMatch(ctx, topicID, details.Etag))
}
