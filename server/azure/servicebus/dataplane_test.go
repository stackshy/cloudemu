package servicebus_test

import (
	"net/http"
	"testing"
)

// TestDataPlanePeekLockComplete exercises the REST peek-lock receive path:
// POST /messages/head locks a message and returns a Location to the locked
// message; DELETE on that Location completes it.
func TestDataPlanePeekLockComplete(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)

	if r := doRequest(t, srv, http.MethodPut, queueURL("pl")+apiVer, `{"properties":{}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create queue: %d", r.StatusCode)
	}

	if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/pl/messages", "payload"); r.StatusCode != http.StatusCreated {
		t.Fatalf("send: %d", r.StatusCode)
	}

	// Peek-lock returns 201 with the body and a Location header.
	lock := doRequest(t, srv, http.MethodPost, "/"+nsName+"/pl/messages/head", "")
	if lock.StatusCode != http.StatusCreated {
		t.Fatalf("peek-lock = %d, want 201", lock.StatusCode)
	}

	if got := readBody(t, lock); got != "payload" {
		t.Fatalf("peek-lock body = %q, want payload", got)
	}

	loc := lock.Header.Get("Location")
	if loc == "" {
		t.Fatal("peek-lock missing Location header")
	}

	// Completing the locked message removes it; the queue then drains empty.
	if r := doRequest(t, srv, http.MethodDelete, loc, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("complete = %d, want 200", r.StatusCode)
	}

	empty := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/pl/messages/head", "")
	if empty.StatusCode != http.StatusNoContent {
		t.Fatalf("post-complete receive = %d, want 204", empty.StatusCode)
	}
}

// TestDataPlanePeekLockAbandon locks a message, abandons it (PUT), then a
// receive-and-delete succeeds because the lock was released.
func TestDataPlanePeekLockAbandon(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)

	if r := doRequest(t, srv, http.MethodPut, queueURL("ab")+apiVer, `{"properties":{}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create queue: %d", r.StatusCode)
	}

	if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/ab/messages", "m1"); r.StatusCode != http.StatusCreated {
		t.Fatalf("send: %d", r.StatusCode)
	}

	lock := doRequest(t, srv, http.MethodPost, "/"+nsName+"/ab/messages/head", "")
	if lock.StatusCode != http.StatusCreated {
		t.Fatalf("peek-lock = %d", lock.StatusCode)
	}

	_ = readBody(t, lock)
	loc := lock.Header.Get("Location")

	// Abandon releases the lock (visibility timeout -> 0).
	if r := doRequest(t, srv, http.MethodPut, loc, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("abandon = %d, want 200", r.StatusCode)
	}

	// The message is immediately available again.
	again := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/ab/messages/head", "")
	if again.StatusCode != http.StatusOK {
		t.Fatalf("receive after abandon = %d, want 200", again.StatusCode)
	}

	if got := readBody(t, again); got != "m1" {
		t.Fatalf("body after abandon = %q, want m1", got)
	}
}
