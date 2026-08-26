package servicebus_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// sendWithMessageID posts a message carrying the given MessageId (empty for
// none) and asserts a 201.
func sendWithMessageID(t *testing.T, srv *httptest.Server, queue, body, messageID string) {
	t.Helper()

	headers := map[string]string{}
	if messageID != "" {
		headers["BrokerProperties"] = `{"MessageId":"` + messageID + `"}`
	}

	r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/"+queue+"/messages", body, headers)
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("send %q = %d, want 201", body, r.StatusCode)
	}

	_ = r.Body.Close()
}

// receiveQueue receive-and-deletes one message from a queue, returning its body
// and whether one was present.
func receiveQueue(t *testing.T, srv *httptest.Server, queue string) (string, bool) {
	t.Helper()

	r := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/"+queue+"/messages/head", "")
	switch r.StatusCode {
	case http.StatusOK:
		return readBody(t, r), true
	case http.StatusNoContent:
		return "", false
	default:
		t.Fatalf("receive = %d, want 200 or 204", r.StatusCode)
		return "", false
	}
}

// TestQueueDuplicateDetection is the regression for RequiresDuplicateDetection
// being ignored on the REST send path: a second send with the same MessageId
// inside the detection window is accepted but not re-enqueued, while a send
// after the window, and one with a distinct MessageId, both enqueue.
func TestQueueDuplicateDetection(t *testing.T) {
	srv, clk := newClockServer(t)
	seedNamespace(t, srv)

	body := `{"properties":{"requiresDuplicateDetection":true,"duplicateDetectionHistoryTimeWindow":"PT5M"}}`
	if r := doRequest(t, srv, http.MethodPut, queueURL("dedup")+apiVer, body); r.StatusCode != http.StatusOK {
		t.Fatalf("create queue = %d", r.StatusCode)
	}

	// Two sends with the same MessageId inside the window: only one enqueues.
	sendWithMessageID(t, srv, "dedup", "first", "id-1")
	sendWithMessageID(t, srv, "dedup", "duplicate", "id-1")

	if got, ok := receiveQueue(t, srv, "dedup"); !ok || got != "first" {
		t.Fatalf("first receive = %q/%v, want first/true", got, ok)
	}

	if _, ok := receiveQueue(t, srv, "dedup"); ok {
		t.Fatal("duplicate was enqueued; want it dropped by duplicate detection")
	}

	// A distinct MessageId is always enqueued.
	sendWithMessageID(t, srv, "dedup", "other", "id-2")

	if got, ok := receiveQueue(t, srv, "dedup"); !ok || got != "other" {
		t.Fatalf("distinct receive = %q/%v, want other/true", got, ok)
	}

	// Past the detection window the same MessageId enqueues again.
	clk.Advance(6 * time.Minute)
	sendWithMessageID(t, srv, "dedup", "after-window", "id-1")

	if got, ok := receiveQueue(t, srv, "dedup"); !ok || got != "after-window" {
		t.Fatalf("post-window receive = %q/%v, want after-window/true", got, ok)
	}
}

// TestQueueDuplicateDetectionRequiresMessageID confirms an empty MessageId is
// never deduplicated (every send enqueues), matching real Service Bus.
func TestQueueDuplicateDetectionRequiresMessageID(t *testing.T) {
	srv, _ := newClockServer(t)
	seedNamespace(t, srv)

	body := `{"properties":{"requiresDuplicateDetection":true}}`
	if r := doRequest(t, srv, http.MethodPut, queueURL("nomid")+apiVer, body); r.StatusCode != http.StatusOK {
		t.Fatalf("create queue = %d", r.StatusCode)
	}

	sendWithMessageID(t, srv, "nomid", "a", "")
	sendWithMessageID(t, srv, "nomid", "b", "")

	got := 0
	for {
		if _, ok := receiveQueue(t, srv, "nomid"); !ok {
			break
		}

		got++
	}

	if got != 2 {
		t.Fatalf("received %d messages, want 2 (no dedup without MessageId)", got)
	}
}

// TestTopicDuplicateDetection confirms a topic's RequiresDuplicateDetection is
// honored on the fan-out path: a duplicate MessageId published to the topic
// reaches each subscription only once.
func TestTopicDuplicateDetection(t *testing.T) {
	srv, _ := newClockServer(t)
	seedNamespace(t, srv)

	if r := doRequest(t, srv, http.MethodPut, topicURL("events")+apiVer,
		`{"properties":{"requiresDuplicateDetection":true,"duplicateDetectionHistoryTimeWindow":"PT5M"}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create topic = %d", r.StatusCode)
	}

	if r := doRequest(t, srv, http.MethodPut, subURL("events", "s1")+apiVer, `{"properties":{}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create subscription = %d", r.StatusCode)
	}

	pub := func(body, messageID string) {
		t.Helper()

		r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/events/messages", body,
			map[string]string{"BrokerProperties": `{"MessageId":"` + messageID + `"}`})
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("publish = %d, want 201", r.StatusCode)
		}

		_ = r.Body.Close()
	}

	pub("first", "id-1")
	pub("duplicate", "id-1")

	if got, ok := receiveSub(t, srv, "events", "s1"); !ok || got != "first" {
		t.Fatalf("first receive = %q/%v, want first/true", got, ok)
	}

	if _, ok := receiveSub(t, srv, "events", "s1"); ok {
		t.Fatal("duplicate reached the subscription; want it dropped")
	}
}

// receiveSub receive-and-deletes one message from a subscription.
func receiveSub(t *testing.T, srv *httptest.Server, topic, sub string) (string, bool) {
	t.Helper()

	r := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/"+topic+"/subscriptions/"+sub+"/messages/head", "")
	switch r.StatusCode {
	case http.StatusOK:
		return readBody(t, r), true
	case http.StatusNoContent:
		return "", false
	default:
		t.Fatalf("receive = %d, want 200 or 204", r.StatusCode)
		return "", false
	}
}

// TestQueueWithoutDuplicateDetection confirms MessageId is NOT deduplicated when
// the queue does not require duplicate detection.
func TestQueueWithoutDuplicateDetection(t *testing.T) {
	srv, _ := newClockServer(t)
	seedNamespace(t, srv)

	if r := doRequest(t, srv, http.MethodPut, queueURL("plain")+apiVer, `{"properties":{}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create queue = %d", r.StatusCode)
	}

	sendWithMessageID(t, srv, "plain", "a", "id-1")
	sendWithMessageID(t, srv, "plain", "b", "id-1")

	got := 0
	for {
		if _, ok := receiveQueue(t, srv, "plain"); !ok {
			break
		}

		got++
	}

	if got != 2 {
		t.Fatalf("received %d messages, want 2 (dedup off)", got)
	}
}
