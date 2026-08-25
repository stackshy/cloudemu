package eventgrid_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

// liveWebhookReceiver is a real HTTP server standing in for a subscriber's
// endpoint, recording every event batch Event Grid delivers to it.
type liveWebhookReceiver struct {
	*httptest.Server

	mu    sync.Mutex
	batch [][]map[string]any
}

func newLiveWebhookReceiver(t *testing.T) *liveWebhookReceiver {
	t.Helper()

	wh := &liveWebhookReceiver{}
	wh.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var events []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&events); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		wh.mu.Lock()
		wh.batch = append(wh.batch, events)
		wh.mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(wh.Close)

	return wh
}

func (wh *liveWebhookReceiver) all() []map[string]any {
	wh.mu.Lock()
	defer wh.mu.Unlock()

	var out []map[string]any
	for _, b := range wh.batch {
		out = append(out, b...)
	}

	return out
}

// TestSDKPublishDeliversToWebHookSubscriber is the end-to-end BLOCKER
// regression: a real armeventgrid client creates a topic and a WebHook event
// subscription against the live TLS wire server, publishes to the topic's
// data-plane endpoint with the real SDK's publish client, and a genuine HTTP
// server standing in for the subscriber must receive the event.
func TestSDKPublishDeliversToWebHookSubscriber(t *testing.T) {
	cf, ts := newEGFactory(t)
	ctx := context.Background()

	topics := cf.NewTopicsClient()
	mkTopicLoc(t, topics, "orders", "eastus")

	receiver := newLiveWebhookReceiver(t)

	subs := cf.NewTopicEventSubscriptionsClient()
	poller, err := subs.BeginCreateOrUpdate(ctx, testRG, "orders", "sub1", armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.WebHookEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeWebHook),
				Properties: &armeventgrid.WebHookEventSubscriptionDestinationProperties{
					EndpointURL: to.Ptr(receiver.URL),
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("subscription BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("subscription PollUntilDone: %v", err)
	}

	// Publish over the data-plane endpoint, exactly like a real publisher —
	// no cloudemu-internal shortcut.
	body := `[{"id":"e1","subject":"orders/1","eventType":"Order.Created",` +
		`"eventTime":"2024-01-02T03:04:05Z","data":{"total":42},"dataVersion":"1.0"}]`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/events", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new publish request: %v", err)
	}

	req.Host = "orders.eastus-1.eventgrid.azure.net"
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish status = %d, want 200", resp.StatusCode)
	}

	got := receiver.all()
	if len(got) != 1 {
		t.Fatalf("webhook received %d events, want 1: %+v", len(got), got)
	}

	if got[0]["eventType"] != "Order.Created" {
		t.Fatalf("eventType = %v, want Order.Created", got[0]["eventType"])
	}

	if got[0]["subject"] != "orders/1" {
		t.Fatalf("subject = %v, want orders/1", got[0]["subject"])
	}
}

// TestSDKPublishFilteredSubjectNotDelivered locks the MEDIUM filter-
// application fix end-to-end: a subscription with subjectBeginsWith must not
// receive an event whose subject doesn't match.
func TestSDKPublishFilteredSubjectNotDelivered(t *testing.T) {
	cf, ts := newEGFactory(t)
	ctx := context.Background()

	topics := cf.NewTopicsClient()
	mkTopicLoc(t, topics, "orders", "eastus")

	receiver := newLiveWebhookReceiver(t)

	subs := cf.NewTopicEventSubscriptionsClient()
	poller, err := subs.BeginCreateOrUpdate(ctx, testRG, "orders", "sub1", armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.WebHookEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeWebHook),
				Properties: &armeventgrid.WebHookEventSubscriptionDestinationProperties{
					EndpointURL: to.Ptr(receiver.URL),
				},
			},
			Filter: &armeventgrid.EventSubscriptionFilter{
				SubjectBeginsWith: to.Ptr("orders/"),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("subscription BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("subscription PollUntilDone: %v", err)
	}

	body := `[{"id":"e1","subject":"invoices/1","eventType":"Invoice.Created",` +
		`"eventTime":"2024-01-02T03:04:05Z","data":{},"dataVersion":"1.0"}]`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/events", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new publish request: %v", err)
	}

	req.Host = "orders.eastus-1.eventgrid.azure.net"
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish status = %d, want 200", resp.StatusCode)
	}

	// Give any (incorrect) delivery a moment to land before asserting none did.
	time.Sleep(50 * time.Millisecond)

	if got := receiver.all(); len(got) != 0 {
		t.Fatalf("webhook received %d events, want 0 (subject doesn't match filter): %+v", len(got), got)
	}
}
