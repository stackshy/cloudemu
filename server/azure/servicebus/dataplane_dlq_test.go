package servicebus_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestDataPlaneDeadLettersAfterMaxDeliveryCount is the regression for
// non-functional dead-lettering: a message abandoned past maxDeliveryCount must
// move to the dead-letter queue (draining the main queue) and be readable from
// the $DeadLetterQueue sub-queue endpoint, preserving its system properties.
func TestDataPlaneDeadLettersAfterMaxDeliveryCount(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)

	// maxDeliveryCount=2: delivered twice, dead-lettered on the 3rd attempt.
	if r := doRequest(t, srv, http.MethodPut, queueURL("poison")+apiVer,
		`{"properties":{"maxDeliveryCount":2}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create queue = %d", r.StatusCode)
	}

	if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/poison/messages", "boom",
		map[string]string{"BrokerProperties": `{"MessageId":"m-poison"}`}); r.StatusCode != http.StatusCreated {
		t.Fatalf("send = %d", r.StatusCode)
	}

	// Two peek-lock + abandon cycles keep the message on the main queue.
	for i := 0; i < 2; i++ {
		lock := doRequest(t, srv, http.MethodPost, "/"+nsName+"/poison/messages/head", "")
		if lock.StatusCode != http.StatusCreated {
			t.Fatalf("peek-lock %d = %d, want 201", i, lock.StatusCode)
		}

		_ = readBody(t, lock)

		loc := lock.Header.Get("Location")
		if r := doRequest(t, srv, http.MethodPut, loc, ""); r.StatusCode != http.StatusOK {
			t.Fatalf("abandon %d = %d", i, r.StatusCode)
		}
	}

	// The 3rd delivery attempt exceeds maxDeliveryCount: the message is
	// dead-lettered, so the main queue drains empty.
	third := doRequest(t, srv, http.MethodPost, "/"+nsName+"/poison/messages/head", "")
	if third.StatusCode != http.StatusNoContent {
		t.Fatalf("3rd receive = %d, want 204 (message dead-lettered)", third.StatusCode)
	}

	// The poison message is now readable from the DLQ endpoint, with its
	// system properties intact.
	dlq := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/poison/$DeadLetterQueue/messages/head", "")
	if dlq.StatusCode != http.StatusOK {
		t.Fatalf("DLQ receive = %d, want 200", dlq.StatusCode)
	}

	if got := readBody(t, dlq); got != "boom" {
		t.Fatalf("DLQ body = %q, want boom", got)
	}

	if props := brokerHeader(t, dlq); props["MessageId"] != "m-poison" {
		t.Fatalf("DLQ MessageId = %q, want m-poison", props["MessageId"])
	}
}

// TestDataPlaneTTLExpiryDeadLetters confirms that with
// deadLetteringOnMessageExpiration enabled, an expired message is routed to the
// dead-letter queue rather than silently dropped.
func TestDataPlaneTTLExpiryDeadLetters(t *testing.T) {
	srv, clk := newClockServer(t)
	seedNamespace(t, srv)

	if r := doRequest(t, srv, http.MethodPut, queueURL("ttldlq")+apiVer,
		`{"properties":{"defaultMessageTimeToLive":"PT5S","deadLetteringOnMessageExpiration":true}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create queue = %d", r.StatusCode)
	}

	if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/ttldlq/messages", "expiring"); r.StatusCode != http.StatusCreated {
		t.Fatalf("send = %d", r.StatusCode)
	}

	clk.Advance(6 * time.Second)

	// A receive on the main queue lazily reaps the expired message into the DLQ.
	main := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/ttldlq/messages/head", "")
	if main.StatusCode != http.StatusNoContent {
		t.Fatalf("main receive after TTL = %d, want 204", main.StatusCode)
	}

	dlq := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/ttldlq/$DeadLetterQueue/messages/head", "")
	if dlq.StatusCode != http.StatusOK {
		t.Fatalf("DLQ receive = %d, want 200 (expired message dead-lettered)", dlq.StatusCode)
	}

	if got := readBody(t, dlq); got != "expiring" {
		t.Fatalf("DLQ body = %q, want expiring", got)
	}
}

// TestDataPlaneSubscriptionDeadLetter confirms a topic subscription also
// dead-letters poison messages onto its own $DeadLetterQueue endpoint.
func TestDataPlaneSubscriptionDeadLetter(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)

	if r := doRequest(t, srv, http.MethodPut, topicURL("orders")+apiVer, `{"properties":{}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create topic = %d", r.StatusCode)
	}

	if r := doRequest(t, srv, http.MethodPut, subURL("orders", "billing")+apiVer,
		`{"properties":{"maxDeliveryCount":1}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create subscription = %d", r.StatusCode)
	}

	if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/orders/messages", "inv-1"); r.StatusCode != http.StatusCreated {
		t.Fatalf("publish = %d", r.StatusCode)
	}

	// maxDeliveryCount=1: one delivery, then dead-lettered on the 2nd attempt.
	lock := doRequest(t, srv, http.MethodPost, "/"+nsName+"/orders/subscriptions/billing/messages/head", "")
	if lock.StatusCode != http.StatusCreated {
		t.Fatalf("peek-lock = %d", lock.StatusCode)
	}

	_ = readBody(t, lock)

	if r := doRequest(t, srv, http.MethodPut, lock.Header.Get("Location"), ""); r.StatusCode != http.StatusOK {
		t.Fatalf("abandon = %d", r.StatusCode)
	}

	second := doRequest(t, srv, http.MethodPost, "/"+nsName+"/orders/subscriptions/billing/messages/head", "")
	if second.StatusCode != http.StatusNoContent {
		t.Fatalf("2nd receive = %d, want 204 (dead-lettered)", second.StatusCode)
	}

	dlq := doRequest(t, srv, http.MethodDelete,
		"/"+nsName+"/orders/subscriptions/billing/$DeadLetterQueue/messages/head", "")
	if dlq.StatusCode != http.StatusOK {
		t.Fatalf("subscription DLQ receive = %d, want 200", dlq.StatusCode)
	}

	if got := readBody(t, dlq); got != "inv-1" {
		t.Fatalf("subscription DLQ body = %q, want inv-1", got)
	}
}

// TestDataPlaneLowerMaxDeliveryCountViaPut is the regression for an update PUT
// not propagating maxDeliveryCount to the backing store: lowering it from 10 to
// 1 must take effect, dead-lettering at the new threshold.
func TestDataPlaneLowerMaxDeliveryCountViaPut(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)

	// Created with the default maxDeliveryCount (10).
	if r := doRequest(t, srv, http.MethodPut, queueURL("relax")+apiVer, `{"properties":{}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create queue = %d", r.StatusCode)
	}

	// Update PUT lowers maxDeliveryCount to 1.
	if r := doRequest(t, srv, http.MethodPut, queueURL("relax")+apiVer,
		`{"properties":{"maxDeliveryCount":1}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("update queue = %d", r.StatusCode)
	}

	if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/relax/messages", "boom"); r.StatusCode != http.StatusCreated {
		t.Fatalf("send = %d", r.StatusCode)
	}

	// maxDeliveryCount=1: one delivery, then dead-lettered on the 2nd attempt.
	lock := doRequest(t, srv, http.MethodPost, "/"+nsName+"/relax/messages/head", "")
	if lock.StatusCode != http.StatusCreated {
		t.Fatalf("peek-lock = %d, want 201", lock.StatusCode)
	}

	_ = readBody(t, lock)

	if r := doRequest(t, srv, http.MethodPut, lock.Header.Get("Location"), ""); r.StatusCode != http.StatusOK {
		t.Fatalf("abandon = %d", r.StatusCode)
	}

	second := doRequest(t, srv, http.MethodPost, "/"+nsName+"/relax/messages/head", "")
	if second.StatusCode != http.StatusNoContent {
		t.Fatalf("2nd receive = %d, want 204 (dead-lettered at lowered threshold)", second.StatusCode)
	}

	dlq := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/relax/$DeadLetterQueue/messages/head", "")
	if dlq.StatusCode != http.StatusOK {
		t.Fatalf("DLQ receive = %d, want 200", dlq.StatusCode)
	}

	if got := readBody(t, dlq); got != "boom" {
		t.Fatalf("DLQ body = %q, want boom", got)
	}
}

// TestDataPlaneRenewLock is the regression for the unimplemented renew-lock
// verb: a POST on a locked message must extend its lock (200), not return 405.
func TestDataPlaneRenewLock(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)

	if r := doRequest(t, srv, http.MethodPut, queueURL("renew")+apiVer, `{"properties":{}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create queue = %d", r.StatusCode)
	}

	if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/renew/messages", "m"); r.StatusCode != http.StatusCreated {
		t.Fatalf("send = %d", r.StatusCode)
	}

	lock := doRequest(t, srv, http.MethodPost, "/"+nsName+"/renew/messages/head", "")
	if lock.StatusCode != http.StatusCreated {
		t.Fatalf("peek-lock = %d", lock.StatusCode)
	}

	_ = readBody(t, lock)

	loc := lock.Header.Get("Location")
	if !strings.Contains(loc, "/renew/messages/") {
		t.Fatalf("Location = %q, want locked-message path", loc)
	}

	renew := doRequest(t, srv, http.MethodPost, loc, "")
	if renew.StatusCode != http.StatusOK {
		t.Fatalf("renew-lock = %d, want 200", renew.StatusCode)
	}
}
