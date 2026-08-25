package servicebus_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// newClockServer starts a Service Bus test server backed by a FakeClock so
// time-dependent data-plane behavior (TTL, scheduling) can be driven
// deterministically without sleeping.
func newClockServer(t *testing.T) (*httptest.Server, *config.FakeClock) {
	t.Helper()

	clk := config.NewFakeClock(time.Now())
	cloud := cloudemu.NewAzure(config.WithClock(clk))
	srv := httptest.NewServer(azureserver.New(azureserver.Drivers{ServiceBus: cloud.ServiceBus}))
	t.Cleanup(srv.Close)

	return srv, clk
}

// brokerHeader parses the BrokerProperties response header into a map.
func brokerHeader(t *testing.T, resp *http.Response) map[string]string {
	t.Helper()

	raw := resp.Header.Get("BrokerProperties")
	if raw == "" {
		t.Fatal("response missing BrokerProperties header")
	}

	var props map[string]string
	if err := json.Unmarshal([]byte(raw), &props); err != nil {
		t.Fatalf("decode BrokerProperties %q: %v", raw, err)
	}

	return props
}

// TestDataPlaneSystemPropertiesRoundTrip is the regression for dropped brokered
// system properties: a sender's MessageId/CorrelationId/Label must be preserved
// and echoed on receive, and the sender's MessageId must not be overwritten by
// the server-assigned id.
func TestDataPlaneSystemPropertiesRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)

	if r := doRequest(t, srv, http.MethodPut, queueURL("props")+apiVer, `{"properties":{}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create queue = %d", r.StatusCode)
	}

	send := doRequest(t, srv, http.MethodPost, "/"+nsName+"/props/messages", "payload",
		map[string]string{"BrokerProperties": `{"MessageId":"m-1","CorrelationId":"cid-1","Label":"lbl"}`})
	if send.StatusCode != http.StatusCreated {
		t.Fatalf("send = %d, want 201", send.StatusCode)
	}

	lock := doRequest(t, srv, http.MethodPost, "/"+nsName+"/props/messages/head", "")
	if lock.StatusCode != http.StatusCreated {
		t.Fatalf("peek-lock = %d, want 201", lock.StatusCode)
	}

	_ = readBody(t, lock)

	props := brokerHeader(t, lock)
	if props["MessageId"] != "m-1" {
		t.Fatalf("MessageId = %q, want sender's m-1 (not the server id)", props["MessageId"])
	}

	if props["CorrelationId"] != "cid-1" {
		t.Fatalf("CorrelationId = %q, want cid-1", props["CorrelationId"])
	}

	if props["Label"] != "lbl" {
		t.Fatalf("Label = %q, want lbl", props["Label"])
	}

	if props["LockToken"] == "" {
		t.Fatal("peek-lock BrokerProperties missing LockToken")
	}
}

// TestDataPlaneMessageTTLExpires is the regression for cosmetic TTL: a queue's
// defaultMessageTimeToLive must actually expire messages so an elapsed message
// is not delivered, while the queue itself keeps working for fresh messages.
func TestDataPlaneMessageTTLExpires(t *testing.T) {
	srv, clk := newClockServer(t)
	seedNamespace(t, srv)

	if r := doRequest(t, srv, http.MethodPut, queueURL("ttl")+apiVer,
		`{"properties":{"defaultMessageTimeToLive":"PT5S"}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create queue = %d", r.StatusCode)
	}

	if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/ttl/messages", "temp"); r.StatusCode != http.StatusCreated {
		t.Fatalf("send = %d", r.StatusCode)
	}

	clk.Advance(6 * time.Second)

	expired := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/ttl/messages/head", "")
	if expired.StatusCode != http.StatusNoContent {
		t.Fatalf("receive after TTL = %d, want 204 (message expired)", expired.StatusCode)
	}

	// A fresh message on the same queue is still deliverable, proving the 204
	// was expiry rather than a broken queue.
	if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/ttl/messages", "fresh"); r.StatusCode != http.StatusCreated {
		t.Fatalf("second send = %d", r.StatusCode)
	}

	fresh := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/ttl/messages/head", "")
	if fresh.StatusCode != http.StatusOK {
		t.Fatalf("fresh receive = %d, want 200", fresh.StatusCode)
	}

	if got := readBody(t, fresh); got != "fresh" {
		t.Fatalf("fresh body = %q, want fresh", got)
	}
}

// TestDataPlaneScheduledMessageDelayed is the regression for ignored
// ScheduledEnqueueTimeUtc: a message scheduled for a future enqueue time must
// not be delivered immediately.
func TestDataPlaneScheduledMessageDelayed(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)

	if r := doRequest(t, srv, http.MethodPut, queueURL("sched")+apiVer, `{"properties":{}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create queue = %d", r.StatusCode)
	}

	send := doRequest(t, srv, http.MethodPost, "/"+nsName+"/sched/messages", "future",
		map[string]string{"BrokerProperties": `{"ScheduledEnqueueTimeUtc":"2099-01-01T00:00:00Z"}`})
	if send.StatusCode != http.StatusCreated {
		t.Fatalf("send = %d, want 201", send.StatusCode)
	}

	early := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/sched/messages/head", "")
	if early.StatusCode != http.StatusNoContent {
		t.Fatalf("receive before scheduled time = %d, want 204 (not yet enqueued)", early.StatusCode)
	}
}
