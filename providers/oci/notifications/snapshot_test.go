package notifications

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

const snapCompartment = "ocid1.compartment.oc1..aaaaaaaasnap"

func newSnapshotMock(t *testing.T) *Mock {
	t.Helper()

	return New(config.NewOptions(
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(snapCompartment),
	))
}

// TestSnapshotRestoreRoundTrip seeds two topics, a confirmed subscription with
// a delivery and a second subscription still PENDING, then restores into a
// fresh mock and asserts identities, the topic cross-reference, the delivery
// history and the pending confirmation all survive.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := t.Context()
	src := newSnapshotMock(t)

	topic, err := src.CreateTopic(ctx, driver.TopicConfig{
		Name:        "alerts",
		DisplayName: "ops alerts",
		Tags:        map[string]string{"team": "ops"},
		Scope:       scope.Scope{Compartment: snapCompartment},
	})
	require.NoError(t, err)

	other, err := src.CreateTopic(ctx, driver.TopicConfig{
		Name:  "audit",
		Scope: scope.Scope{Compartment: snapCompartment},
	})
	require.NoError(t, err)

	active, err := src.CreateSubscription(ctx, SubscriptionSpec{
		TopicID: topic.ID, CompartmentID: snapCompartment,
		Protocol: ProtocolEmail, Endpoint: "ops@example.com",
	})
	require.NoError(t, err)

	_, err = src.ConfirmSubscription(ctx, active.ID, active.ConfirmationToken, ProtocolEmail)
	require.NoError(t, err)

	pending, err := src.CreateSubscription(ctx, SubscriptionSpec{
		TopicID: other.ID, CompartmentID: snapCompartment,
		Protocol: ProtocolEmail, Endpoint: "audit@example.com",
	})
	require.NoError(t, err)

	_, err = src.PublishMessage(ctx, topic.ID, MessageSpec{Title: "deploy", Body: "shipped"})
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newSnapshotMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	// The topic is back under its OCID with its OCI-only state.
	restored, err := dst.GetTopic(ctx, topic.ID)
	require.NoError(t, err)
	assert.Equal(t, "alerts", restored.Name)
	assert.Equal(t, map[string]string{"team": "ops"}, restored.Tags)

	details, ok := dst.TopicDetails(topic.ID)
	require.True(t, ok)
	assert.Equal(t, StateActive, details.LifecycleState)
	assert.NotEmpty(t, details.ShortTopicID)
	assert.NotEmpty(t, details.Etag)

	// The subscription still points at the topic it was created on, so the
	// cross-reference survived.
	subs, err := dst.ListSubscriptions(ctx, topic.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)

	got, err := dst.GetSubscription(ctx, active.ID)
	require.NoError(t, err)
	assert.Equal(t, topic.ID, got.TopicID)
	assert.Equal(t, StateActive, got.LifecycleState)

	// The delivery history came back with it.
	delivered := dst.Deliveries(active.ID)
	require.Len(t, delivered, 1)
	assert.Equal(t, "shipped", delivered[0].Body)

	// The second topic's subscription is still PENDING and received nothing.
	stillPending, err := dst.GetSubscription(ctx, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, other.ID, stillPending.TopicID)
	assert.Equal(t, StatePending, stillPending.LifecycleState)
	assert.Empty(t, dst.Deliveries(pending.ID))
}

// A subscription still PENDING at snapshot time is still PENDING after a
// restore, and its original token still confirms it.
func TestSnapshotKeepsAPendingSubscriptionConfirmable(t *testing.T) {
	ctx := t.Context()
	src := newSnapshotMock(t)

	topic, err := src.CreateTopic(ctx, driver.TopicConfig{
		Name:  "alerts",
		Scope: scope.Scope{Compartment: snapCompartment},
	})
	require.NoError(t, err)

	pending, err := src.CreateSubscription(ctx, SubscriptionSpec{
		TopicID: topic.ID, CompartmentID: snapCompartment,
		Protocol: ProtocolEmail, Endpoint: "ops@example.com",
	})
	require.NoError(t, err)
	require.Equal(t, StatePending, pending.LifecycleState)

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newSnapshotMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	got, err := dst.GetSubscription(ctx, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, StatePending, got.LifecycleState)
	assert.Equal(t, pending.ConfirmationToken, got.ConfirmationToken)

	// Publishing before the confirmation still delivers to nobody.
	_, err = dst.PublishMessage(ctx, topic.ID, MessageSpec{Body: "early"})
	require.NoError(t, err)
	assert.Empty(t, dst.Deliveries(pending.ID))

	// The token minted before the snapshot still confirms after the restore.
	result, err := dst.ConfirmSubscription(ctx, pending.ID, pending.ConfirmationToken, ProtocolEmail)
	require.NoError(t, err)
	assert.Equal(t, pending.ID, result.SubscriptionID)

	_, err = dst.PublishMessage(ctx, topic.ID, MessageSpec{Body: "later"})
	require.NoError(t, err)

	delivered := dst.Deliveries(pending.ID)
	require.Len(t, delivered, 1)
	assert.Equal(t, "later", delivered[0].Body)
}

func TestRestoreRejectsMalformedInput(t *testing.T) {
	require.Error(t, newSnapshotMock(t).Restore(t.Context(), json.RawMessage("not json")))
}

// An empty snapshot restores cleanly and leaves the mock usable.
func TestRestoreEmptySnapshot(t *testing.T) {
	ctx := t.Context()
	m := newSnapshotMock(t)

	require.NoError(t, m.Restore(ctx, json.RawMessage("{}")))

	topics, err := m.ListTopics(ctx, scope.Scope{Compartment: snapCompartment})
	require.NoError(t, err)
	assert.Empty(t, topics)

	_, err = m.CreateTopic(ctx, driver.TopicConfig{
		Name:  "alerts",
		Scope: scope.Scope{Compartment: snapCompartment},
	})
	require.NoError(t, err)
}

// Snapshotting an untouched mock produces a document that restores to an empty
// mock rather than failing.
func TestSnapshotOfAnEmptyMock(t *testing.T) {
	ctx := t.Context()

	data, err := newSnapshotMock(t).Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newSnapshotMock(t)
	require.NoError(t, dst.Restore(ctx, data))

	topics, err := dst.ListTopics(ctx, scope.Scope{Compartment: snapCompartment})
	require.NoError(t, err)
	assert.Empty(t, topics)
}
