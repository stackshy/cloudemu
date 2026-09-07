package notifications_test

import (
	"context"
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
		"unknown id":     {"ocid1.onssubscription.oc1..missing", sub.ConfirmationToken, "", cerrors.NotFound},
		"wrong token":    {sub.ID, "token-wrong", "", cerrors.InvalidArgument},
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

	// The token alone unsubscribes; the protocol is optional. Removes the
	// subscription, so it runs after the rejection cases.
	require.NoError(t, m.UnsubscribeByToken(ctx, sub.ID, sub.ConfirmationToken, ""))
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
