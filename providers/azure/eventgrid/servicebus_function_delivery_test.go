package eventgrid

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/functions"
	"github.com/stackshy/cloudemu/v2/providers/azure/servicebus"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	fndriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// destinationResourceJSON builds the raw ARM EventSubscription properties for a
// resourceId-based destination (ServiceBusQueue/ServiceBusTopic/AzureFunction),
// optionally carrying a subjectBeginsWith filter.
func destinationResourceJSON(endpointType, resourceID, subjectBeginsWith string) string {
	props := map[string]any{
		"destination": map[string]any{
			"endpointType": endpointType,
			"properties":   map[string]any{"resourceId": resourceID},
		},
	}

	if subjectBeginsWith != "" {
		props["filter"] = map[string]any{"subjectBeginsWith": subjectBeginsWith}
	}

	b, _ := json.Marshal(props)

	return string(b)
}

func newPeerOpts() *config.Options {
	return config.NewOptions(config.WithRegion("eastus"), config.WithAccountID("cloudemu"))
}

// TestPutEventsDeliversToServiceBusQueue is test (a): a ServiceBusQueue
// destination must enqueue the event envelope into the peer Service Bus queue so
// a receiver reads it.
func TestPutEventsDeliversToServiceBusQueue(t *testing.T) {
	ctx := context.Background()

	sb := servicebus.New(newPeerOpts())
	queue, err := sb.CreateQueue(ctx, mqdriver.QueueConfig{Name: "orders-q"})
	require.NoError(t, err)

	m, _ := newTestMock()
	m.SetServiceBusDeliverer(sb)

	createTestTopic(t, m, "orders")

	resourceID := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.ServiceBus/namespaces/ns1/queues/orders-q"

	_, err = m.PutRule(ctx, &ebdriver.RuleConfig{
		Name:        "sub-sbq",
		EventBus:    "orders",
		Description: destinationResourceJSON(endpointTypeServiceBusQueue, resourceID, ""),
	})
	require.NoError(t, err)

	_, err = m.PutEvents(ctx, []ebdriver.Event{{
		EventBus:   "orders",
		DetailType: "Order.Created",
		Detail:     `{"total":42}`,
		Subject:    "orders/1",
	}})
	require.NoError(t, err)

	msgs, err := sb.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: queue.URL, MaxMessages: 10})
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	var batch []deliveryEvent
	require.NoError(t, json.Unmarshal([]byte(msgs[0].Body), &batch))
	require.Len(t, batch, 1)
	assert.Equal(t, "Order.Created", batch[0].EventType)
	assert.Equal(t, "orders/1", batch[0].Subject)
	assert.JSONEq(t, `{"total":42}`, string(batch[0].Data))
}

// TestPutEventsDeliversToServiceBusTopic is test (b): a ServiceBusTopic
// destination enqueues into the peer topic (modeled as a queue) so its
// subscriptions receive the event.
func TestPutEventsDeliversToServiceBusTopic(t *testing.T) {
	ctx := context.Background()

	sb := servicebus.New(newPeerOpts())
	topic, err := sb.CreateQueue(ctx, mqdriver.QueueConfig{Name: "orders-t"})
	require.NoError(t, err)

	m, _ := newTestMock()
	m.SetServiceBusDeliverer(sb)

	createTestTopic(t, m, "orders")

	resourceID := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.ServiceBus/namespaces/ns1/topics/orders-t"

	_, err = m.PutRule(ctx, &ebdriver.RuleConfig{
		Name:        "sub-sbt",
		EventBus:    "orders",
		Description: destinationResourceJSON(endpointTypeServiceBusTopic, resourceID, ""),
	})
	require.NoError(t, err)

	_, err = m.PutEvents(ctx, []ebdriver.Event{{
		EventBus:   "orders",
		DetailType: "Order.Shipped",
		Subject:    "orders/9",
	}})
	require.NoError(t, err)

	msgs, err := sb.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: topic.URL, MaxMessages: 10})
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	var batch []deliveryEvent
	require.NoError(t, json.Unmarshal([]byte(msgs[0].Body), &batch))
	require.Len(t, batch, 1)
	assert.Equal(t, "Order.Shipped", batch[0].EventType)
}

// TestPutEventsInvokesAzureFunction is test (c): an AzureFunction destination
// must invoke the peer Functions app with the event envelope.
func TestPutEventsInvokesAzureFunction(t *testing.T) {
	ctx := context.Background()

	fn := functions.New(newPeerOpts())
	_, err := fn.CreateFunction(ctx, fndriver.FunctionConfig{Name: "orderfn", Runtime: "dotnet"})
	require.NoError(t, err)

	var (
		mu      sync.Mutex
		invoked [][]byte
	)

	fn.RegisterHandler("orderfn", func(_ context.Context, payload []byte) ([]byte, error) {
		mu.Lock()
		invoked = append(invoked, append([]byte(nil), payload...))
		mu.Unlock()

		return payload, nil
	})

	m, _ := newTestMock()
	m.SetFunctionInvoker(fn)

	createTestTopic(t, m, "orders")

	resourceID := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Web/sites/orderfn/functions/onEvent"

	_, err = m.PutRule(ctx, &ebdriver.RuleConfig{
		Name:        "sub-fn",
		EventBus:    "orders",
		Description: destinationResourceJSON(endpointTypeAzureFunction, resourceID, ""),
	})
	require.NoError(t, err)

	_, err = m.PutEvents(ctx, []ebdriver.Event{{
		EventBus:   "orders",
		DetailType: "Order.Created",
		Detail:     `{"total":7}`,
		Subject:    "orders/2",
	}})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, invoked, 1)

	var batch []deliveryEvent
	require.NoError(t, json.Unmarshal(invoked[0], &batch))
	require.Len(t, batch, 1)
	assert.Equal(t, "Order.Created", batch[0].EventType)
	assert.JSONEq(t, `{"total":7}`, string(batch[0].Data))
}

// TestPutEventsResourceDestinationHonorsFilter is test (d): a filter that does
// not match must suppress delivery to a ServiceBus destination, exactly as it
// does for WebHook.
func TestPutEventsResourceDestinationHonorsFilter(t *testing.T) {
	ctx := context.Background()

	sb := servicebus.New(newPeerOpts())
	queue, err := sb.CreateQueue(ctx, mqdriver.QueueConfig{Name: "orders-q"})
	require.NoError(t, err)

	m, _ := newTestMock()
	m.SetServiceBusDeliverer(sb)

	createTestTopic(t, m, "orders")

	resourceID := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.ServiceBus/namespaces/ns1/queues/orders-q"

	_, err = m.PutRule(ctx, &ebdriver.RuleConfig{
		Name:        "sub-sbq",
		EventBus:    "orders",
		Description: destinationResourceJSON(endpointTypeServiceBusQueue, resourceID, "orders/"),
	})
	require.NoError(t, err)

	_, err = m.PutEvents(ctx, []ebdriver.Event{{
		EventBus:   "orders",
		DetailType: "Invoice.Created",
		Subject:    "invoices/1",
	}})
	require.NoError(t, err)

	msgs, err := sb.ReceiveMessages(ctx, mqdriver.ReceiveMessageInput{QueueURL: queue.URL, MaxMessages: 10})
	require.NoError(t, err)
	assert.Empty(t, msgs, "a non-matching subject must not be delivered to the Service Bus queue")
}

// TestPutEventsResourceDestinationNilPeerNoPanic is test (e): with no peer wired,
// ServiceBus/Function destinations are skipped gracefully — no panic, publish
// still succeeds.
func TestPutEventsResourceDestinationNilPeerNoPanic(t *testing.T) {
	ctx := context.Background()

	m, _ := newTestMock()
	createTestTopic(t, m, "orders")

	sbResource := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.ServiceBus/namespaces/ns1/queues/q"
	fnResource := "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Web/sites/app/functions/fn"

	_, err := m.PutRule(ctx, &ebdriver.RuleConfig{
		Name: "sub-sbq", EventBus: "orders",
		Description: destinationResourceJSON(endpointTypeServiceBusQueue, sbResource, ""),
	})
	require.NoError(t, err)

	_, err = m.PutRule(ctx, &ebdriver.RuleConfig{
		Name: "sub-fn", EventBus: "orders",
		Description: destinationResourceJSON(endpointTypeAzureFunction, fnResource, ""),
	})
	require.NoError(t, err)

	result, err := m.PutEvents(ctx, []ebdriver.Event{{
		EventBus: "orders", DetailType: "Order.Created", Subject: "orders/1",
	}})
	require.NoError(t, err)
	assert.Equal(t, 1, result.SuccessCount)
}

// TestResourceLeafAndFunctionAppName pins the ARM resourceId parsing helpers.
func TestResourceLeafAndFunctionAppName(t *testing.T) {
	assert.Equal(t, "myq",
		resourceLeafName("/subscriptions/s/providers/Microsoft.ServiceBus/namespaces/ns/queues/myq"))
	assert.Equal(t, "myt",
		resourceLeafName("/subscriptions/s/providers/Microsoft.ServiceBus/namespaces/ns/topics/myt/"))
	assert.Equal(t, "", resourceLeafName(""))

	assert.Equal(t, "app",
		functionAppName("/subscriptions/s/providers/Microsoft.Web/sites/app/functions/fn"))
	assert.Equal(t, "app", functionAppName("/subscriptions/s/providers/Microsoft.Web/sites/app"))
	assert.Equal(t, "", functionAppName("/no/marker/here"))
}
