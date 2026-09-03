package functions

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
	"github.com/stretchr/testify/assert"
)

// cosmosTriggerConfig builds a function.json-shaped config declaring a
// single cosmosDBTrigger input binding on (database, container).
func cosmosTriggerConfig(database, container string) map[string]any {
	return map[string]any{
		"bindings": []any{
			map[string]any{
				"name":          "docs",
				"type":          "cosmosDBTrigger",
				"direction":     "in",
				"databaseName":  database,
				"containerName": container,
				"connection":    "CosmosDBConnection",
			},
		},
	}
}

func TestDeliverCosmosFunctionTriggerInvokesBoundApp(t *testing.T) {
	m := newTestMock()
	count, last := seedTriggeredApp(t, m, "cosmos-app", cosmosTriggerConfig("orders-db", "orders"))

	m.DeliverCosmosFunctionTrigger(context.Background(), "orders-db", "orders", []byte(`[{"id":"1"}]`))

	assert.Equal(t, int32(1), atomic.LoadInt32(count), "bound function should fire exactly once")
	assert.Equal(t, `[{"id":"1"}]`, string(*last), "function should receive the changed document array")
}

func TestDeliverCosmosFunctionTriggerNoMatch(t *testing.T) {
	m := newTestMock()
	count, _ := seedTriggeredApp(t, m, "cosmos-app", cosmosTriggerConfig("orders-db", "orders"))

	// A different container, a different database, an empty database and an
	// empty container must all no-op.
	m.DeliverCosmosFunctionTrigger(context.Background(), "orders-db", "invoices", []byte("x"))
	m.DeliverCosmosFunctionTrigger(context.Background(), "other-db", "orders", []byte("x"))
	m.DeliverCosmosFunctionTrigger(context.Background(), "", "orders", []byte("x"))
	m.DeliverCosmosFunctionTrigger(context.Background(), "orders-db", "", []byte("x"))

	assert.Equal(t, int32(0), atomic.LoadInt32(count))
}

func TestDeliverCosmosFunctionTriggerSkipsDisabledFunction(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "disabled-app", Runtime: "node"})
	assert.NoError(t, err)

	_, err = m.UpsertSiteMeta(ctx, SiteMeta{Name: "disabled-app", Subscription: "sub", ResourceGroup: "rg"})
	assert.NoError(t, err)

	_, _, err = m.CreateSiteFunction(ctx, "disabled-app", SiteFunction{
		Name: "fn1", Config: cosmosTriggerConfig("orders-db", "orders"), IsDisabled: true,
	})
	assert.NoError(t, err)

	var count int32

	m.RegisterHandler("disabled-app", func(_ context.Context, p []byte) ([]byte, error) {
		atomic.AddInt32(&count, 1)

		return p, nil
	})

	m.DeliverCosmosFunctionTrigger(ctx, "orders-db", "orders", []byte("x"))

	assert.Equal(t, int32(0), atomic.LoadInt32(&count), "a disabled function must not fire")
}

func TestDeliverCosmosFunctionTriggerOutputBindingIgnored(t *testing.T) {
	m := newTestMock()

	// An output binding to the container is not a trigger and must not fire.
	cfg := map[string]any{
		"bindings": []any{
			map[string]any{
				"name": "out", "type": "cosmosDBTrigger", "direction": "out",
				"databaseName": "orders-db", "containerName": "orders",
			},
		},
	}
	count, _ := seedTriggeredApp(t, m, "producer-app", cfg)

	m.DeliverCosmosFunctionTrigger(context.Background(), "orders-db", "orders", []byte("x"))

	assert.Equal(t, int32(0), atomic.LoadInt32(count))
}

// TestDeliverCosmosFunctionTriggerRecursionGuard proves a re-entrant delivery
// already at MaxDepth is dropped without invoking the function.
func TestDeliverCosmosFunctionTriggerRecursionGuard(t *testing.T) {
	m := newTestMock()
	count, _ := seedTriggeredApp(t, m, "loop-app", cosmosTriggerConfig("loop-db", "loop"))

	ctx := recursionguard.WithDepth(context.Background(), recursionguard.MaxDepth)
	m.DeliverCosmosFunctionTrigger(ctx, "loop-db", "loop", []byte("x"))

	assert.Equal(t, int32(0), atomic.LoadInt32(count), "delivery at MaxDepth must be dropped")
}
