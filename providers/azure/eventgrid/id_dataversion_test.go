package eventgrid

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPutEventsPreservesPublisherID locks the fix: a publisher-supplied event id
// must survive end-to-end — carried onto the delivered payload and recorded in
// event history unchanged (subscribers dedup on it).
func TestPutEventsPreservesPublisherID(t *testing.T) {
	receiver := newWebhookReceiver(t)

	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "orders")

	_, err := m.PutRule(ctx, &driver.RuleConfig{
		Name:        "sub1",
		EventBus:    "orders",
		Description: destinationJSON(receiver.URL),
	})
	require.NoError(t, err)

	res, err := m.PutEvents(ctx, []driver.Event{{
		ID:         "e1",
		EventBus:   "orders",
		DetailType: "Order.Created",
		Subject:    "orders/1",
	}})
	require.NoError(t, err)
	require.Equal(t, []string{"e1"}, res.EventIDs)

	got := receiver.events()
	require.Len(t, got, 1)
	assert.Equal(t, "e1", got[0].ID, "publisher id must be delivered unchanged")

	hist, err := m.GetEventHistory(ctx, "orders", 0)
	require.NoError(t, err)
	require.Len(t, hist, 1)
	assert.Equal(t, "e1", hist[0].ID, "publisher id must appear in event history")
}

// TestPutEventsSynthesizesMissingID locks the other half: a publish with no id
// still gets a synthesized one.
func TestPutEventsSynthesizesMissingID(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "orders")

	res, err := m.PutEvents(ctx, []driver.Event{{
		EventBus:   "orders",
		DetailType: "Order.Created",
	}})
	require.NoError(t, err)
	require.Len(t, res.EventIDs, 1)
	assert.NotEmpty(t, res.EventIDs[0], "a publish without an id must get a synthesized one")

	hist, err := m.GetEventHistory(ctx, "orders", 0)
	require.NoError(t, err)
	require.Len(t, hist, 1)
	assert.NotEmpty(t, hist[0].ID)
}

// TestDeliveryPreservesDataVersion locks the fix: the publisher's dataVersion is
// delivered verbatim and metadataVersion is always "1".
func TestDeliveryPreservesDataVersion(t *testing.T) {
	receiver := newWebhookReceiver(t)

	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "orders")

	_, err := m.PutRule(ctx, &driver.RuleConfig{
		Name:        "sub1",
		EventBus:    "orders",
		Description: destinationJSON(receiver.URL),
	})
	require.NoError(t, err)

	_, err = m.PutEvents(ctx, []driver.Event{{
		EventBus:    "orders",
		DetailType:  "Order.Created",
		DataVersion: "2.0",
	}})
	require.NoError(t, err)

	got := receiver.events()
	require.Len(t, got, 1)
	assert.Equal(t, "2.0", got[0].DataVersion, "publisher dataVersion must be preserved")
	assert.Equal(t, "1", got[0].MetadataVersion, "metadataVersion must be 1")
}

// TestDeliveryDefaultsDataVersion locks the default: when the publisher omits
// dataVersion, delivery defaults it to "1.0" (metadataVersion still "1").
func TestDeliveryDefaultsDataVersion(t *testing.T) {
	receiver := newWebhookReceiver(t)

	m, _ := newTestMock()
	ctx := context.Background()

	createTestTopic(t, m, "orders")

	_, err := m.PutRule(ctx, &driver.RuleConfig{
		Name:        "sub1",
		EventBus:    "orders",
		Description: destinationJSON(receiver.URL),
	})
	require.NoError(t, err)

	_, err = m.PutEvents(ctx, []driver.Event{{
		EventBus:   "orders",
		DetailType: "Order.Created",
	}})
	require.NoError(t, err)

	got := receiver.events()
	require.Len(t, got, 1)
	assert.Equal(t, "1.0", got[0].DataVersion)
	assert.Equal(t, "1", got[0].MetadataVersion)
}
