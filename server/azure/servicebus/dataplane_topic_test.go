package servicebus_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func topicURL(topic string) string {
	return nsURL() + "/topics/" + topic
}

func subURL(topic, sub string) string {
	return topicURL(topic) + "/subscriptions/" + sub
}

func nsURLNamed(name string) string {
	return "/subscriptions/" + subID + "/resourceGroups/" + rgName +
		"/providers/Microsoft.ServiceBus/namespaces/" + name
}

// seedTopicSub creates a topic and one or more subscriptions under the seeded
// namespace so the data plane can fan out to them.
func seedTopicSub(t *testing.T, srv *httptest.Server, topic string, subs ...string) {
	t.Helper()

	if r := doRequest(t, srv, http.MethodPut, topicURL(topic)+apiVer, `{"properties":{}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create topic %s = %d", topic, r.StatusCode)
	}

	for _, s := range subs {
		if r := doRequest(t, srv, http.MethodPut, subURL(topic, s)+apiVer,
			`{"properties":{}}`); r.StatusCode != http.StatusOK {
			t.Fatalf("create subscription %s = %d", s, r.StatusCode)
		}
	}
}

// TestDataPlaneTopicFanout publishes one message to a topic and confirms every
// subscription receives its own independent copy.
func TestDataPlaneTopicFanout(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)
	seedTopicSub(t, srv, "events", "s1", "s2")

	if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/events/messages", "hello"); r.StatusCode != http.StatusCreated {
		t.Fatalf("topic send = %d, want 201", r.StatusCode)
	}

	for _, s := range []string{"s1", "s2"} {
		rcv := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/events/subscriptions/"+s+"/messages/head", "")
		if rcv.StatusCode != http.StatusOK {
			t.Fatalf("receive from %s = %d, want 200", s, rcv.StatusCode)
		}

		if got := readBody(t, rcv); got != "hello" {
			t.Fatalf("%s body = %q, want hello", s, got)
		}

		// Each subscription's queue drains independently.
		empty := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/events/subscriptions/"+s+"/messages/head", "")
		if empty.StatusCode != http.StatusNoContent {
			t.Fatalf("second receive from %s = %d, want 204", s, empty.StatusCode)
		}
	}
}

// TestDataPlaneTopicNoSubscriptions confirms a publish to a subscription-less
// topic is accepted (201) and dropped, matching Azure.
func TestDataPlaneTopicNoSubscriptions(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)
	seedTopicSub(t, srv, "lonely")

	if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/lonely/messages", "x"); r.StatusCode != http.StatusCreated {
		t.Fatalf("send to empty topic = %d, want 201", r.StatusCode)
	}
}

// TestDataPlaneSubscriptionPeekLockComplete drives peek-lock receive against a
// subscription: the Location header must address the subscription path, and
// completing it drains that subscription.
func TestDataPlaneSubscriptionPeekLockComplete(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)
	seedTopicSub(t, srv, "orders", "billing")

	if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/orders/messages", "inv-1"); r.StatusCode != http.StatusCreated {
		t.Fatalf("send = %d", r.StatusCode)
	}

	lock := doRequest(t, srv, http.MethodPost, "/"+nsName+"/orders/subscriptions/billing/messages/head", "")
	if lock.StatusCode != http.StatusCreated {
		t.Fatalf("peek-lock = %d, want 201", lock.StatusCode)
	}

	if got := readBody(t, lock); got != "inv-1" {
		t.Fatalf("peek-lock body = %q, want inv-1", got)
	}

	loc := lock.Header.Get("Location")
	if !strings.Contains(loc, "/orders/subscriptions/billing/messages/") {
		t.Fatalf("Location = %q, want subscription-scoped path", loc)
	}

	if r := doRequest(t, srv, http.MethodDelete, loc, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("complete = %d, want 200", r.StatusCode)
	}

	empty := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/orders/subscriptions/billing/messages/head", "")
	if empty.StatusCode != http.StatusNoContent {
		t.Fatalf("post-complete receive = %d, want 204", empty.StatusCode)
	}
}

// TestDataPlaneReceiveFromTopicRejected confirms you cannot receive directly
// from a topic; receiving is only valid against a subscription.
func TestDataPlaneReceiveFromTopicRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)
	seedTopicSub(t, srv, "events", "s1")

	r := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/events/messages/head", "")
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("receive from topic = %d, want 400", r.StatusCode)
	}
}

// TestDataPlaneSendToSubscriptionRejected confirms sending to a subscription is
// rejected; publishes go to the parent topic.
func TestDataPlaneSendToSubscriptionRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)
	seedTopicSub(t, srv, "events", "s1")

	r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/events/subscriptions/s1/messages", "x")
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("send to subscription = %d, want 400", r.StatusCode)
	}
}

// TestDataPlaneSubscriptionNotFound confirms a missing subscription reports 404
// rather than a misleading "queue not found".
func TestDataPlaneSubscriptionNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)
	seedTopicSub(t, srv, "events")

	r := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/events/subscriptions/ghost/messages/head", "")
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("missing subscription = %d, want 404", r.StatusCode)
	}
}

// TestDataPlaneSubscriptionDeleteDrainsStore confirms deleting a subscription
// drops its backing message store: a re-created subscription starts empty.
func TestDataPlaneSubscriptionDeleteDrainsStore(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)
	seedTopicSub(t, srv, "events", "s1")

	if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/events/messages", "m"); r.StatusCode != http.StatusCreated {
		t.Fatalf("send = %d", r.StatusCode)
	}

	if r := doRequest(t, srv, http.MethodDelete, subURL("events", "s1")+apiVer, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("delete subscription = %d", r.StatusCode)
	}

	seedTopicSub(t, srv, "events", "s1")

	empty := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/events/subscriptions/s1/messages/head", "")
	if empty.StatusCode != http.StatusNoContent {
		t.Fatalf("receive on re-created subscription = %d, want 204", empty.StatusCode)
	}
}

// TestNamespaceCaseInsensitive confirms namespace names are treated
// case-insensitively for lookup and name-availability, mirroring Azure's
// DNS-hostname uniqueness.
func TestNamespaceCaseInsensitive(t *testing.T) {
	srv, _ := newTestServer(t)

	if r := doRequest(t, srv, http.MethodPut, nsURLNamed("Case-NS")+apiVer, `{"location":"eastus"}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create namespace = %d", r.StatusCode)
	}

	// A GET with different casing resolves the same namespace.
	get := doRequest(t, srv, http.MethodGet, nsURLNamed("case-ns")+apiVer, "")
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET lowercased namespace = %d, want 200", get.StatusCode)
	}

	// checkNameAvailability is case-insensitive: the name is taken.
	check := doRequest(t, srv, http.MethodPost,
		"/subscriptions/"+subID+"/providers/Microsoft.ServiceBus/checkNameAvailability"+apiVer,
		`{"name":"CASE-NS"}`)
	if check.StatusCode != http.StatusOK {
		t.Fatalf("checkNameAvailability = %d", check.StatusCode)
	}

	if body := readBody(t, check); !strings.Contains(body, "NameInUse") {
		t.Fatalf("checkNameAvailability body = %q, want NameInUse", body)
	}
}
