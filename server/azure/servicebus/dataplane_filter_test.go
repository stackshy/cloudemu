package servicebus_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func ruleURL(topic, sub, rule string) string {
	return subURL(topic, sub) + "/rules/" + rule
}

// TestDataPlaneCorrelationFilterRoutesOnlyMatchingMessages is the regression
// for the subscription-filter bug: a CorrelationFilter rule must stop a
// non-matching message from reaching that subscription, while a plain
// $Default ("1=1") subscription still receives every message.
//
// https://learn.microsoft.com/azure/service-bus-messaging/topic-filters
// https://learn.microsoft.com/rest/api/servicebus/send-message-to-queue
// (BrokerProperties header carries CorrelationId/MessageId/Label etc.)
func TestDataPlaneCorrelationFilterRoutesOnlyMatchingMessages(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)
	seedTopicSub(t, srv, "orders", "urgent-only", "everything")

	// Replace "urgent-only"'s $Default rule with a CorrelationFilter that
	// only accepts CorrelationId=urgent.
	if r := doRequest(t, srv, http.MethodDelete, ruleURL("orders", "urgent-only", "$Default")+apiVer, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("delete $Default rule = %d", r.StatusCode)
	}

	ruleBody := `{"properties":{"filterType":"CorrelationFilter","correlationFilter":{"correlationId":"urgent"}}}`
	if r := doRequest(t, srv, http.MethodPut, ruleURL("orders", "urgent-only", "urgent-rule")+apiVer, ruleBody); r.StatusCode != http.StatusOK {
		t.Fatalf("create correlation rule = %d", r.StatusCode)
	}

	send := func(body, correlationID string) {
		t.Helper()

		headers := map[string]string{}
		if correlationID != "" {
			headers["BrokerProperties"] = `{"CorrelationId":"` + correlationID + `"}`
		}

		if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/orders/messages", body, headers); r.StatusCode != http.StatusCreated {
			t.Fatalf("send %q = %d, want 201", body, r.StatusCode)
		}
	}

	send("matches", "urgent")
	send("no-match", "routine")

	// "everything" has no filter beyond $Default: both messages arrive.
	for _, want := range []string{"matches", "no-match"} {
		r := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/orders/subscriptions/everything/messages/head", "")
		if r.StatusCode != http.StatusOK {
			t.Fatalf("everything receive = %d, want 200", r.StatusCode)
		}

		if got := readBody(t, r); got != want {
			t.Fatalf("everything body = %q, want %q", got, want)
		}
	}

	// "urgent-only" only receives the message whose CorrelationId matched.
	r := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/orders/subscriptions/urgent-only/messages/head", "")
	if r.StatusCode != http.StatusOK {
		t.Fatalf("urgent-only receive = %d, want 200", r.StatusCode)
	}

	if got := readBody(t, r); got != "matches" {
		t.Fatalf("urgent-only body = %q, want matches", got)
	}

	empty := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/orders/subscriptions/urgent-only/messages/head", "")
	if empty.StatusCode != http.StatusNoContent {
		t.Fatalf("urgent-only second receive = %d, want 204 (no-match message must not have arrived)", empty.StatusCode)
	}
}

// TestDataPlaneSQLFilterSysProperty confirms a SqlFilter expression over a
// sys.* system property (here sys.Label) is evaluated, not just stored.
func TestDataPlaneSQLFilterSysProperty(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)
	seedTopicSub(t, srv, "events", "errors-only")

	if r := doRequest(t, srv, http.MethodDelete, ruleURL("events", "errors-only", "$Default")+apiVer, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("delete $Default rule = %d", r.StatusCode)
	}

	ruleBody := `{"properties":{"filterType":"SqlFilter","sqlFilter":{"sqlExpression":"sys.Label = 'error'"}}}`
	if r := doRequest(t, srv, http.MethodPut, ruleURL("events", "errors-only", "label-rule")+apiVer, ruleBody); r.StatusCode != http.StatusOK {
		t.Fatalf("create sql rule = %d", r.StatusCode)
	}

	send := func(body, label string) {
		t.Helper()

		r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/events/messages", body,
			map[string]string{"BrokerProperties": `{"Label":"` + label + `"}`})
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("send %q = %d, want 201", body, r.StatusCode)
		}
	}

	send("info-msg", "info")
	send("error-msg", "error")

	r := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/events/subscriptions/errors-only/messages/head", "")
	if r.StatusCode != http.StatusOK {
		t.Fatalf("receive = %d, want 200", r.StatusCode)
	}

	if got := readBody(t, r); got != "error-msg" {
		t.Fatalf("body = %q, want error-msg", got)
	}

	empty := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/events/subscriptions/errors-only/messages/head", "")
	if empty.StatusCode != http.StatusNoContent {
		t.Fatalf("second receive = %d, want 204 (info-msg must not have matched)", empty.StatusCode)
	}
}

// TestQueueLockDurationHonored is the regression for LockDuration being
// cosmetic: a queue created with a 5-second LockDuration must release a
// peek-locked message after 5 seconds, not the driver's 30-second default.
func TestQueueLockDurationHonored(t *testing.T) {
	clk := config.NewFakeClock(time.Now())
	cloud := cloudemu.NewAzure(config.WithClock(clk))
	srv := httptest.NewServer(azureserver.New(azureserver.Drivers{ServiceBus: cloud.ServiceBus}))
	t.Cleanup(srv.Close)

	seedNamespace(t, srv)

	if r := doRequest(t, srv, http.MethodPut, queueURL("short-lock")+apiVer,
		`{"properties":{"lockDuration":"PT5S"}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create queue = %d", r.StatusCode)
	}

	if r := doRequest(t, srv, http.MethodPost, "/"+nsName+"/short-lock/messages", "m"); r.StatusCode != http.StatusCreated {
		t.Fatalf("send = %d", r.StatusCode)
	}

	lock := doRequest(t, srv, http.MethodPost, "/"+nsName+"/short-lock/messages/head", "")
	if lock.StatusCode != http.StatusCreated {
		t.Fatalf("peek-lock = %d", lock.StatusCode)
	}

	_ = readBody(t, lock)

	// Still within the 5s lock: the message stays invisible.
	stillLocked := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/short-lock/messages/head", "")
	if stillLocked.StatusCode != http.StatusNoContent {
		t.Fatalf("receive while locked = %d, want 204", stillLocked.StatusCode)
	}

	clk.Advance(6 * time.Second)

	// Past the configured 5s LockDuration (well under the driver's 30s
	// default), the message is visible again.
	again := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/short-lock/messages/head", "")
	if again.StatusCode != http.StatusOK {
		t.Fatalf("receive after LockDuration elapses = %d, want 200", again.StatusCode)
	}

	if got := readBody(t, again); got != "m" {
		t.Fatalf("body = %q, want m", got)
	}
}
