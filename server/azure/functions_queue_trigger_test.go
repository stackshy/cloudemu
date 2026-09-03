package azure_test

// Real-user end-to-end proof that an Azure Queue Storage trigger actually FIRES
// the bound function: a function app declares a queueTrigger binding for queue
// Q via ARM, then a message enqueued to Q with the real azqueue SDK synchronously
// invokes the function with the message as its payload. Before this wiring the
// binding round-tripped as CRUD only and no message ever reached the function.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureprovider "github.com/stackshy/cloudemu/v2/providers/azure"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// newFullAzureServerWithProvider boots the full production Azure server over a
// caller-held provider, so the test can register a Go handler to observe that a
// function was actually invoked.
func newFullAzureServerWithProvider(t *testing.T) (*httptest.Server, *azureprovider.Provider) {
	t.Helper()

	p := cloudemu.NewAzure()
	ts := httptest.NewServer(azureserver.NewFromProvider(p))
	t.Cleanup(ts.Close)

	return ts, p
}

// armPut issues a raw ARM PUT (the SDK's BeginCreateFunction only accepts 201,
// so a raw client keeps the test independent of that status quirk).
func armPut(t *testing.T, ts *httptest.Server, path, body string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request %s: %v", path, err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT %s = %d, want 200/201", path, resp.StatusCode)
	}
}

func TestQueueStorageTriggerInvokesFunction(t *testing.T) {
	ts, p := newFullAzureServerWithProvider(t)
	ctx := context.Background()

	const (
		app   = "qt-app"
		queue = "qt-queue"
	)

	base := "/subscriptions/sub-qt/resourceGroups/rg-qt/providers/Microsoft.Web/sites/" + app

	// 1. Create the function app.
	armPut(t, ts, base+"?api-version=2022-03-01", `{"location":"eastus","properties":{"siteConfig":{}}}`)

	// 2. Deploy a function that declares a queueTrigger binding for the queue.
	armPut(t, ts, base+"/functions/consume?api-version=2022-03-01",
		`{"properties":{"config":{"bindings":[`+
			`{"name":"item","type":"queueTrigger","direction":"in",`+
			`"queueName":"`+queue+`","connection":"AzureWebJobsStorage"}]}}}`)

	// Observe invocation via a registered handler on the held provider.
	got := make(chan string, 1)
	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		select {
		case got <- string(payload):
		default:
		}

		return payload, nil
	})

	// 3. Create the Storage queue and enqueue a message with the real SDK.
	createStorageQueue(t, ts, queue)
	qc := newStorageQueueClient(t, ts, queue)

	const payload = "order-42"
	if _, err := qc.EnqueueMessage(ctx, payload, nil); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	// Delivery is synchronous, so the function has fired by the time enqueue
	// returns.
	select {
	case body := <-got:
		if body != payload {
			t.Fatalf("function received %q, want %q", body, payload)
		}
	default:
		t.Fatal("queue-triggered function was not invoked")
	}
}

// TestQueueStorageTriggerUnboundQueueDoesNotFire proves a message on a queue no
// function is bound to invokes nothing (the enqueue still succeeds).
func TestQueueStorageTriggerUnboundQueueDoesNotFire(t *testing.T) {
	ts, p := newFullAzureServerWithProvider(t)
	ctx := context.Background()

	const app = "qt-app2"
	base := "/subscriptions/sub-qt/resourceGroups/rg-qt/providers/Microsoft.Web/sites/" + app

	armPut(t, ts, base+"?api-version=2022-03-01", `{"location":"eastus","properties":{"siteConfig":{}}}`)
	armPut(t, ts, base+"/functions/consume?api-version=2022-03-01",
		`{"properties":{"config":{"bindings":[`+
			`{"name":"item","type":"queueTrigger","direction":"in",`+
			`"queueName":"bound-queue","connection":"AzureWebJobsStorage"}]}}}`)

	fired := make(chan struct{}, 1)
	p.Functions.RegisterHandler(app, func(_ context.Context, payload []byte) ([]byte, error) {
		fired <- struct{}{}

		return payload, nil
	})

	// Enqueue to a DIFFERENT queue than the binding names.
	createStorageQueue(t, ts, "other-queue")
	qc := newStorageQueueClient(t, ts, "other-queue")

	if _, err := qc.EnqueueMessage(ctx, "x", nil); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	select {
	case <-fired:
		t.Fatal("function fired for a queue it is not bound to")
	default:
	}
}
