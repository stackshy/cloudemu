package servicebus_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestQueueUnknownSubPathDoesNotCorruptQueue guards the routing rule that
// queue-scoped authorizationRules now rely on: a path deeper than
// "queues/{name}" must never fall through to the queue's own CRUD. Before the
// guard, a PUT reset the queue's properties, a GET echoed the queue and a
// DELETE removed it. An unmodeled sub-path is a clean 501.
func TestQueueUnknownSubPathDoesNotCorruptQueue(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)

	put := doRequest(t, srv, http.MethodPut, queueURL("protected")+apiVer,
		`{"properties":{"maxDeliveryCount":7}}`)
	if put.StatusCode != http.StatusOK {
		t.Fatalf("PUT queue = %d", put.StatusCode)
	}

	_ = put.Body.Close()

	for _, m := range []string{http.MethodPut, http.MethodGet, http.MethodDelete} {
		resp := doRequest(t, srv, m, queueURL("protected")+"/unknownChild/x"+apiVer, `{}`)
		if resp.StatusCode != http.StatusNotImplemented {
			t.Fatalf("%s unknown sub-path = %d, want 501", m, resp.StatusCode)
		}

		_ = resp.Body.Close()
	}

	get := doRequest(t, srv, http.MethodGet, queueURL("protected")+apiVer, "")
	if body := readBody(t, get); get.StatusCode != http.StatusOK || !strings.Contains(body, `"maxDeliveryCount":7`) {
		t.Fatalf("queue corrupted: %d %s", get.StatusCode, body)
	}
}
