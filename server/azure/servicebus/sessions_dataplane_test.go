package servicebus_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const sessOwnerHeader = "X-Cloudemu-Session-Owner"

func createSessionQueue(t *testing.T, srv *httptest.Server, name string) {
	t.Helper()

	if r := doRequest(t, srv, http.MethodPut, queueURL(name)+apiVer,
		`{"properties":{"requiresSession":true}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create session queue = %d", r.StatusCode)
	}
}

func sessionSend(t *testing.T, srv *httptest.Server, queue, sessionID, body string) {
	t.Helper()

	r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/"+queue+"/messages", body,
		map[string]string{"BrokerProperties": `{"SessionId":"` + sessionID + `"}`})
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("session send (%s) = %d, want 201", sessionID, r.StatusCode)
	}
}

// TestSessionSendEnforcement is the Tier-1 (real, faithful) behavior: a session
// entity requires a SessionId on every send, and a plain (non-session) receive
// against it returns nothing — real Azure Service Bus sessions are consumed only
// via the session receiver, which has no REST equivalent.
func TestSessionSendEnforcement(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)
	createSessionQueue(t, srv, "sq")

	noSid := doRequest(t, srv, http.MethodPost, "/"+nsName+"/sq/messages", "payload")
	if noSid.StatusCode != http.StatusBadRequest {
		t.Fatalf("session-less send to a session entity = %d, want 400", noSid.StatusCode)
	}

	sessionSend(t, srv, "sq", "s1", "payload")

	plain := doRequest(t, srv, http.MethodPost, "/"+nsName+"/sq/messages/head", "")
	if plain.StatusCode != http.StatusNoContent {
		t.Fatalf("plain receive on a session entity = %d, want 204 (empty)", plain.StatusCode)
	}
}

// TestSessionReceiveAcceptNextAndState drives the Tier-2 REST session extension:
// accept-next-session, session-scoped receive, and session state round-trip.
func TestSessionReceiveAcceptNextAndState(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)
	createSessionQueue(t, srv, "sq")

	sessionSend(t, srv, "sq", "s1", "a1")
	sessionSend(t, srv, "sq", "s2", "b1")

	// accept-next picks the FIFO-first session (s1) and returns its message.
	acc := doRequest(t, srv, http.MethodPost, "/"+nsName+"/sq/sessions/head", "")
	if acc.StatusCode != http.StatusCreated {
		t.Fatalf("accept-next = %d, want 201", acc.StatusCode)
	}

	if body := readBody(t, acc); body != "a1" {
		t.Fatalf("accept-next body = %q, want a1", body)
	}

	if sid := brokerHeader(t, acc)["SessionId"]; sid != "s1" {
		t.Fatalf("accepted SessionId = %q, want s1", sid)
	}

	owner := acc.Header.Get(sessOwnerHeader)
	if owner == "" {
		t.Fatal("accept-next response missing session owner header")
	}

	// A named receive of s2 returns only s2's message.
	rs2 := doRequest(t, srv, http.MethodPost, "/"+nsName+"/sq/sessions/s2/head", "")
	if rs2.StatusCode != http.StatusCreated {
		t.Fatalf("s2 receive = %d, want 201", rs2.StatusCode)
	}

	if body := readBody(t, rs2); body != "b1" {
		t.Fatalf("s2 body = %q, want b1", body)
	}

	// Session state on s1 round-trips.
	if r := doRequest(t, srv, http.MethodPut, "/"+nsName+"/sq/sessions/s1/state", "mystate"); r.StatusCode != http.StatusOK {
		t.Fatalf("set session state = %d, want 200", r.StatusCode)
	}

	gs := doRequest(t, srv, http.MethodGet, "/"+nsName+"/sq/sessions/s1/state", "")
	if gs.StatusCode != http.StatusOK {
		t.Fatalf("get session state = %d, want 200", gs.StatusCode)
	}

	if body := readBody(t, gs); body != "mystate" {
		t.Fatalf("session state = %q, want mystate", body)
	}

	// Renewing s1's lock with the granted owner token succeeds.
	rn := doRequest(t, srv, http.MethodPost, "/"+nsName+"/sq/sessions/s1", "",
		map[string]string{sessOwnerHeader: owner})
	if rn.StatusCode != http.StatusOK {
		t.Fatalf("renew session lock = %d, want 200", rn.StatusCode)
	}
}

// TestSessionLockExclusivity confirms a session locked by one receiver cannot be
// accepted by another until the lock is released or expires.
func TestSessionLockExclusivity(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)
	createSessionQueue(t, srv, "sq")

	sessionSend(t, srv, "sq", "s1", "a1")

	first := doRequest(t, srv, http.MethodPost, "/"+nsName+"/sq/sessions/s1/head", "")
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first accept of s1 = %d, want 201", first.StatusCode)
	}

	// A different owner is refused while the lock is held.
	second := doRequest(t, srv, http.MethodPost, "/"+nsName+"/sq/sessions/s1/head", "",
		map[string]string{sessOwnerHeader: "other-owner"})
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second owner accept of a locked session = %d, want 409", second.StatusCode)
	}
}
