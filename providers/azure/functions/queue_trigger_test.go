package functions

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queueTriggerConfig builds a function.json-shaped config declaring a single
// input trigger binding of the given type bound to queueName.
func queueTriggerConfig(bindingType, queueName string) map[string]any {
	return map[string]any{
		"bindings": []any{
			map[string]any{
				"name":       "item",
				"type":       bindingType,
				"direction":  "in",
				"queueName":  queueName,
				"connection": "AzureWebJobsStorage",
			},
		},
	}
}

// seedTriggeredApp creates the portable function app plus its SiteMeta and one
// deployed function carrying the supplied binding config, then registers a Go
// handler that records each invocation's payload. The returned pointers observe
// invocations.
func seedTriggeredApp(
	t *testing.T, m *Mock, app string, cfg map[string]any,
) (*int32, *[]byte) {
	t.Helper()

	ctx := context.Background()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: app, Runtime: "node"})
	require.NoError(t, err)

	_, err = m.UpsertSiteMeta(ctx, SiteMeta{Name: app, Subscription: "sub", ResourceGroup: "rg"})
	require.NoError(t, err)

	_, _, err = m.CreateSiteFunction(ctx, app, SiteFunction{Name: "fn1", Config: cfg})
	require.NoError(t, err)

	var count int32

	var last []byte

	m.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		atomic.AddInt32(&count, 1)
		last = append([]byte(nil), payload...)

		return payload, nil
	})

	return &count, &last
}

func TestDeliverFunctionTriggerInvokesBoundApp(t *testing.T) {
	m := newTestMock()
	count, last := seedTriggeredApp(t, m, "orders-app", queueTriggerConfig("queueTrigger", "orders"))

	m.DeliverFunctionTrigger(context.Background(), "queueTrigger", "orders", []byte("hello"))

	assert.Equal(t, int32(1), atomic.LoadInt32(count), "bound function should fire exactly once")
	assert.Equal(t, "hello", string(*last), "function should receive the enqueued message body")
}

func TestDeliverFunctionTriggerNoMatch(t *testing.T) {
	m := newTestMock()
	count, _ := seedTriggeredApp(t, m, "orders-app", queueTriggerConfig("queueTrigger", "orders"))

	// Different queue name, wrong binding type, and empty inputs must all no-op.
	m.DeliverFunctionTrigger(context.Background(), "queueTrigger", "other-queue", []byte("x"))
	m.DeliverFunctionTrigger(context.Background(), "serviceBusTrigger", "orders", []byte("x"))
	m.DeliverFunctionTrigger(context.Background(), "", "orders", []byte("x"))
	m.DeliverFunctionTrigger(context.Background(), "queueTrigger", "", []byte("x"))

	assert.Equal(t, int32(0), atomic.LoadInt32(count))
}

func TestDeliverFunctionTriggerServiceBusBinding(t *testing.T) {
	m := newTestMock()
	count, _ := seedTriggeredApp(t, m, "sb-app", queueTriggerConfig("serviceBusTrigger", "jobs"))

	m.DeliverFunctionTrigger(context.Background(), "serviceBusTrigger", "jobs", []byte("j"))

	assert.Equal(t, int32(1), atomic.LoadInt32(count))
}

func TestDeliverFunctionTriggerSkipsDisabledFunction(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "disabled-app", Runtime: "node"})
	require.NoError(t, err)
	_, err = m.UpsertSiteMeta(ctx, SiteMeta{Name: "disabled-app", Subscription: "sub", ResourceGroup: "rg"})
	require.NoError(t, err)
	_, _, err = m.CreateSiteFunction(ctx, "disabled-app", SiteFunction{
		Name: "fn1", Config: queueTriggerConfig("queueTrigger", "orders"), IsDisabled: true,
	})
	require.NoError(t, err)

	var count int32

	m.RegisterHandler("disabled-app", func(_ context.Context, p []byte) ([]byte, error) {
		atomic.AddInt32(&count, 1)

		return p, nil
	})

	m.DeliverFunctionTrigger(ctx, "queueTrigger", "orders", []byte("x"))

	assert.Equal(t, int32(0), atomic.LoadInt32(&count), "a disabled function must not fire")
}

func TestDeliverFunctionTriggerOutputBindingIgnored(t *testing.T) {
	m := newTestMock()

	// An output ("out") binding to the queue is not a trigger and must not fire.
	cfg := map[string]any{
		"bindings": []any{
			map[string]any{"name": "out", "type": "queue", "direction": "out", "queueName": "orders"},
		},
	}
	count, _ := seedTriggeredApp(t, m, "producer-app", cfg)

	m.DeliverFunctionTrigger(context.Background(), "queueTrigger", "orders", []byte("x"))

	assert.Equal(t, int32(0), atomic.LoadInt32(count))
}

// TestDeliverFunctionTriggerRecursionGuard proves a re-entrant delivery already
// at MaxDepth is dropped without invoking the function.
func TestDeliverFunctionTriggerRecursionGuard(t *testing.T) {
	m := newTestMock()
	count, _ := seedTriggeredApp(t, m, "loop-app", queueTriggerConfig("queueTrigger", "loop"))

	ctx := recursionguard.WithDepth(context.Background(), recursionguard.MaxDepth)
	m.DeliverFunctionTrigger(ctx, "queueTrigger", "loop", []byte("x"))

	assert.Equal(t, int32(0), atomic.LoadInt32(count), "delivery at MaxDepth must be dropped")
}
