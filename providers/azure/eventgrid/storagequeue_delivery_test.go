package eventgrid

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/providers/azure/servicebus"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storageQueueDestinationJSON builds the raw ARM EventSubscription properties
// for a StorageQueue destination (armeventgrid
// StorageQueueEventSubscriptionDestinationProperties: resourceId is the
// storage account, queueName is the queue under it — distinct fields, unlike
// ServiceBusQueue/Topic's single resourceId).
func storageQueueDestinationJSON(storageAccountResourceID, queueName, subjectBeginsWith string) string {
	props := map[string]any{
		"destination": map[string]any{
			"endpointType": endpointTypeStorageQueue,
			"properties": map[string]any{
				"resourceId": storageAccountResourceID,
				"queueName":  queueName,
			},
		},
	}

	if subjectBeginsWith != "" {
		props["filter"] = map[string]any{"subjectBeginsWith": subjectBeginsWith}
	}

	b, _ := json.Marshal(props)

	return string(b)
}

// TestPutEventsDeliversToStorageQueue proves a StorageQueue destination
// enqueues the event envelope into the peer Azure Queue Storage queue, named by
// the destination's queueName field (not the storage-account resourceId).
func TestPutEventsDeliversToStorageQueue(t *testing.T) {
	ctx := context.Background()

	qs := servicebus.New(newPeerOpts())
	queue, err := qs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "orders-sq"})
	require.NoError(t, err)

	m, _ := newTestMock()
	m.SetStorageQueueDeliverer(qs)

	createTestTopic(t, m, "orders")

	storageAccountID := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/mystorage"

	_, err = m.PutRule(ctx, &ebdriver.RuleConfig{
		Name:        "sub-sq",
		EventBus:    "orders",
		Description: storageQueueDestinationJSON(storageAccountID, "orders-sq", ""),
	})
	require.NoError(t, err)

	_, err = m.PutEvents(ctx, []ebdriver.Event{{
		EventBus:   "orders",
		DetailType: "Order.Created",
		Detail:     `{"total":42}`,
		Subject:    "orders/1",
	}})
	require.NoError(t, err)

	msgs, err := qs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: queue.URL, MaxMessages: 10})
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	var batch []deliveryEvent
	require.NoError(t, json.Unmarshal([]byte(msgs[0].Body), &batch))
	require.Len(t, batch, 1)
	assert.Equal(t, "Order.Created", batch[0].EventType)
	assert.Equal(t, "orders/1", batch[0].Subject)
	assert.JSONEq(t, `{"total":42}`, string(batch[0].Data))
}

// TestPutEventsStorageQueueHonorsFilter proves a non-matching subscription
// filter suppresses delivery to a StorageQueue destination, exactly as it does
// for the other destination types.
func TestPutEventsStorageQueueHonorsFilter(t *testing.T) {
	ctx := context.Background()

	qs := servicebus.New(newPeerOpts())
	queue, err := qs.CreateQueue(ctx, mqdriver.QueueConfig{Name: "orders-sq"})
	require.NoError(t, err)

	m, _ := newTestMock()
	m.SetStorageQueueDeliverer(qs)

	createTestTopic(t, m, "orders")

	storageAccountID := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/mystorage"

	_, err = m.PutRule(ctx, &ebdriver.RuleConfig{
		Name:        "sub-sq",
		EventBus:    "orders",
		Description: storageQueueDestinationJSON(storageAccountID, "orders-sq", "orders/"),
	})
	require.NoError(t, err)

	_, err = m.PutEvents(ctx, []ebdriver.Event{{
		EventBus:   "orders",
		DetailType: "Invoice.Created",
		Subject:    "invoices/1",
	}})
	require.NoError(t, err)

	msgs, err := qs.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: queue.URL, MaxMessages: 10})
	require.NoError(t, err)
	assert.Empty(t, msgs, "a non-matching subject must not be delivered to the Storage Queue")
}

// TestPutEventsStorageQueueNilPeerNoPanic proves a StorageQueue destination is
// skipped gracefully (no panic, publish still succeeds) when no peer is wired.
func TestPutEventsStorageQueueNilPeerNoPanic(t *testing.T) {
	ctx := context.Background()

	m, _ := newTestMock()
	createTestTopic(t, m, "orders")

	storageAccountID := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/mystorage"

	_, err := m.PutRule(ctx, &ebdriver.RuleConfig{
		Name:        "sub-sq",
		EventBus:    "orders",
		Description: storageQueueDestinationJSON(storageAccountID, "missing-q", ""),
	})
	require.NoError(t, err)

	result, err := m.PutEvents(ctx, []ebdriver.Event{{
		EventBus: "orders", DetailType: "Order.Created", Subject: "orders/1",
	}})
	require.NoError(t, err)
	assert.Equal(t, 1, result.SuccessCount)
}

// TestPutEventsStorageQueueMissingQueueNoPanic proves a StorageQueue
// destination naming a queue that doesn't exist on the peer is skipped
// gracefully (DeliverExternal returns NotFound, which the dispatcher ignores),
// matching the best-effort-sink behavior of the other destination types.
func TestPutEventsStorageQueueMissingQueueNoPanic(t *testing.T) {
	ctx := context.Background()

	qs := servicebus.New(newPeerOpts())

	m, _ := newTestMock()
	m.SetStorageQueueDeliverer(qs)

	createTestTopic(t, m, "orders")

	storageAccountID := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/mystorage"

	_, err := m.PutRule(ctx, &ebdriver.RuleConfig{
		Name:        "sub-sq",
		EventBus:    "orders",
		Description: storageQueueDestinationJSON(storageAccountID, "missing-q", ""),
	})
	require.NoError(t, err)

	result, err := m.PutEvents(ctx, []ebdriver.Event{{
		EventBus: "orders", DetailType: "Order.Created", Subject: "orders/1",
	}})
	require.NoError(t, err)
	assert.Equal(t, 1, result.SuccessCount)
}
