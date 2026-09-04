package functions

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
	"github.com/stretchr/testify/assert"
)

// blobTriggerConfig builds a function.json-shaped config declaring a single
// blobTrigger input binding on path.
func blobTriggerConfig(path string) map[string]any {
	return map[string]any{
		"bindings": []any{
			map[string]any{
				"name":       "blob",
				"type":       "blobTrigger",
				"direction":  "in",
				"path":       path,
				"connection": "AzureWebJobsStorage",
			},
		},
	}
}

func TestDeliverBlobFunctionTriggerInvokesBoundApp(t *testing.T) {
	m := newTestMock()
	count, last := seedTriggeredApp(t, m, "blob-app", blobTriggerConfig("images/{name}"))

	m.DeliverBlobFunctionTrigger(context.Background(), "images", "cat.png", []byte("bytes"))

	assert.Equal(t, int32(1), atomic.LoadInt32(count), "bound function should fire exactly once")
	assert.Equal(t, "bytes", string(*last), "function should receive the blob content")
}

func TestDeliverBlobFunctionTriggerNoMatch(t *testing.T) {
	m := newTestMock()
	count, _ := seedTriggeredApp(t, m, "blob-app", blobTriggerConfig("images/{name}"))

	// A different container, an empty container, and an empty binding path must
	// all no-op.
	m.DeliverBlobFunctionTrigger(context.Background(), "docs", "cat.png", []byte("x"))
	m.DeliverBlobFunctionTrigger(context.Background(), "", "cat.png", []byte("x"))

	assert.Equal(t, int32(0), atomic.LoadInt32(count))
}

func TestDeliverBlobFunctionTriggerBareContainerMatchesEveryBlob(t *testing.T) {
	m := newTestMock()
	count, _ := seedTriggeredApp(t, m, "blob-app", blobTriggerConfig("images"))

	m.DeliverBlobFunctionTrigger(context.Background(), "images", "anything.jpg", []byte("x"))

	assert.Equal(t, int32(1), atomic.LoadInt32(count))
}

func TestDeliverBlobFunctionTriggerLiteralPrefixConstrainsBlobName(t *testing.T) {
	m := newTestMock()
	count, _ := seedTriggeredApp(t, m, "blob-app", blobTriggerConfig("images/logs/{name}.txt"))

	// The prefix before the first token ("logs/") must match the blob name.
	m.DeliverBlobFunctionTrigger(context.Background(), "images", "logs/access.txt", []byte("x"))
	assert.Equal(t, int32(1), atomic.LoadInt32(count))

	m.DeliverBlobFunctionTrigger(context.Background(), "images", "other/access.txt", []byte("x"))
	assert.Equal(t, int32(1), atomic.LoadInt32(count), "a blob outside the literal prefix must not fire")
}

func TestDeliverBlobFunctionTriggerSkipsDisabledFunction(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateFunction(ctx, driver.FunctionConfig{Name: "disabled-app", Runtime: "node"})
	assert.NoError(t, err)

	_, err = m.UpsertSiteMeta(ctx, SiteMeta{Name: "disabled-app", Subscription: "sub", ResourceGroup: "rg"})
	assert.NoError(t, err)

	_, _, err = m.CreateSiteFunction(ctx, "disabled-app", SiteFunction{
		Name: "fn1", Config: blobTriggerConfig("images/{name}"), IsDisabled: true,
	})
	assert.NoError(t, err)

	var count int32

	m.RegisterHandler("disabled-app", func(_ context.Context, p []byte) ([]byte, error) {
		atomic.AddInt32(&count, 1)

		return p, nil
	})

	m.DeliverBlobFunctionTrigger(ctx, "images", "cat.png", []byte("x"))

	assert.Equal(t, int32(0), atomic.LoadInt32(&count), "a disabled function must not fire")
}

func TestDeliverBlobFunctionTriggerOutputBindingIgnored(t *testing.T) {
	m := newTestMock()

	// An output ("out") binding to the container is not a trigger and must not
	// fire.
	cfg := map[string]any{
		"bindings": []any{
			map[string]any{"name": "out", "type": "blobTrigger", "direction": "out", "path": "images/{name}"},
		},
	}
	count, _ := seedTriggeredApp(t, m, "producer-app", cfg)

	m.DeliverBlobFunctionTrigger(context.Background(), "images", "cat.png", []byte("x"))

	assert.Equal(t, int32(0), atomic.LoadInt32(count))
}

// TestDeliverBlobFunctionTriggerRecursionGuard proves a re-entrant delivery
// already at MaxDepth is dropped without invoking the function.
func TestDeliverBlobFunctionTriggerRecursionGuard(t *testing.T) {
	m := newTestMock()
	count, _ := seedTriggeredApp(t, m, "loop-app", blobTriggerConfig("loop/{name}"))

	ctx := recursionguard.WithDepth(context.Background(), recursionguard.MaxDepth)
	m.DeliverBlobFunctionTrigger(ctx, "loop", "again.txt", []byte("x"))

	assert.Equal(t, int32(0), atomic.LoadInt32(count), "delivery at MaxDepth must be dropped")
}
