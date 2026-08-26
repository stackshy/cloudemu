package servicebus_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// armEntityProps is the subset of queue/topic ARM properties the round-trip
// tests assert on.
type armEntityProps struct {
	AutoDeleteOnIdle                    string `json:"autoDeleteOnIdle"`
	DuplicateDetectionHistoryTimeWindow string `json:"duplicateDetectionHistoryTimeWindow"`
	ForwardTo                           string `json:"forwardTo"`
	RequiresDuplicateDetection          bool   `json:"requiresDuplicateDetection"`
	EnableExpress                       bool   `json:"enableExpress"`
	EnableBatchedOperations             *bool  `json:"enableBatchedOperations"`
}

func decodeEntityProps(t *testing.T, resp *http.Response) armEntityProps {
	t.Helper()

	var got struct {
		Properties armEntityProps `json:"properties"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	_ = resp.Body.Close()

	return got.Properties
}

// TestQueueARMPropertiesRoundTrip guards the azurerm perpetual-diff bug: the
// queue ARM properties azurerm_servicebus_queue sets (autoDeleteOnIdle,
// duplicateDetectionHistoryTimeWindow, enableBatchedOperations, forwardTo,
// enableExpress) must survive PUT -> GET so plan-after-apply is clean.
func TestQueueARMPropertiesRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)

	body := `{"properties":{` +
		`"autoDeleteOnIdle":"PT30M",` +
		`"duplicateDetectionHistoryTimeWindow":"PT20M",` +
		`"requiresDuplicateDetection":true,` +
		`"enableBatchedOperations":false,` +
		`"enableExpress":true,` +
		`"forwardTo":"other-queue"}}`

	put := doRequest(t, srv, http.MethodPut, queueURL("orders")+apiVer, body)
	if put.StatusCode != http.StatusOK {
		t.Fatalf("PUT queue = %d, body: %s", put.StatusCode, readBody(t, put))
	}

	_ = put.Body.Close()

	get := doRequest(t, srv, http.MethodGet, queueURL("orders")+apiVer, "")
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET queue = %d", get.StatusCode)
	}

	assertRoundTrip(t, decodeEntityProps(t, get), armEntityProps{
		AutoDeleteOnIdle:                    "PT30M",
		DuplicateDetectionHistoryTimeWindow: "PT20M",
		ForwardTo:                           "other-queue",
		RequiresDuplicateDetection:          true,
		EnableExpress:                       true,
		EnableBatchedOperations:             boolPtr(false),
	})
}

// TestQueueARMPropertyDefaults checks the real-Service-Bus defaults a create
// that omits these fields reports back.
func TestQueueARMPropertyDefaults(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)

	if r := doRequest(t, srv, http.MethodPut, queueURL("plain")+apiVer, `{"properties":{}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("PUT queue = %d", r.StatusCode)
	}

	get := doRequest(t, srv, http.MethodGet, queueURL("plain")+apiVer, "")
	props := decodeEntityProps(t, get)

	if props.DuplicateDetectionHistoryTimeWindow != "PT10M" {
		t.Fatalf("default duplicateDetectionHistoryTimeWindow = %q, want PT10M", props.DuplicateDetectionHistoryTimeWindow)
	}

	if props.AutoDeleteOnIdle == "" {
		t.Fatal("default autoDeleteOnIdle is empty, want the max sentinel")
	}

	if props.EnableBatchedOperations == nil || !*props.EnableBatchedOperations {
		t.Fatalf("default enableBatchedOperations = %v, want true", props.EnableBatchedOperations)
	}
}

// TestTopicARMPropertiesRoundTrip mirrors the queue round-trip for the topic
// properties azurerm_servicebus_topic sets.
func TestTopicARMPropertiesRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)
	seedNamespace(t, srv)

	body := `{"properties":{` +
		`"autoDeleteOnIdle":"PT45M",` +
		`"duplicateDetectionHistoryTimeWindow":"PT15M",` +
		`"requiresDuplicateDetection":true,` +
		`"enableBatchedOperations":false}}`

	put := doRequest(t, srv, http.MethodPut, topicURL("events")+apiVer, body)
	if put.StatusCode != http.StatusOK {
		t.Fatalf("PUT topic = %d, body: %s", put.StatusCode, readBody(t, put))
	}

	_ = put.Body.Close()

	get := doRequest(t, srv, http.MethodGet, topicURL("events")+apiVer, "")
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET topic = %d", get.StatusCode)
	}

	got := decodeEntityProps(t, get)
	if got.AutoDeleteOnIdle != "PT45M" || got.DuplicateDetectionHistoryTimeWindow != "PT15M" {
		t.Fatalf("topic durations = %q / %q, want PT45M / PT15M",
			got.AutoDeleteOnIdle, got.DuplicateDetectionHistoryTimeWindow)
	}

	if !got.RequiresDuplicateDetection {
		t.Fatal("topic requiresDuplicateDetection = false, want true")
	}

	if got.EnableBatchedOperations == nil || *got.EnableBatchedOperations {
		t.Fatalf("topic enableBatchedOperations = %v, want explicit false", got.EnableBatchedOperations)
	}
}

func assertRoundTrip(t *testing.T, got, want armEntityProps) {
	t.Helper()

	if got.AutoDeleteOnIdle != want.AutoDeleteOnIdle {
		t.Errorf("autoDeleteOnIdle = %q, want %q", got.AutoDeleteOnIdle, want.AutoDeleteOnIdle)
	}

	if got.DuplicateDetectionHistoryTimeWindow != want.DuplicateDetectionHistoryTimeWindow {
		t.Errorf("duplicateDetectionHistoryTimeWindow = %q, want %q",
			got.DuplicateDetectionHistoryTimeWindow, want.DuplicateDetectionHistoryTimeWindow)
	}

	if got.ForwardTo != want.ForwardTo {
		t.Errorf("forwardTo = %q, want %q", got.ForwardTo, want.ForwardTo)
	}

	if got.RequiresDuplicateDetection != want.RequiresDuplicateDetection {
		t.Errorf("requiresDuplicateDetection = %v, want %v", got.RequiresDuplicateDetection, want.RequiresDuplicateDetection)
	}

	if got.EnableExpress != want.EnableExpress {
		t.Errorf("enableExpress = %v, want %v", got.EnableExpress, want.EnableExpress)
	}

	if got.EnableBatchedOperations == nil || *got.EnableBatchedOperations != *want.EnableBatchedOperations {
		t.Errorf("enableBatchedOperations = %v, want %v", got.EnableBatchedOperations, *want.EnableBatchedOperations)
	}
}

func boolPtr(b bool) *bool { return &b }
