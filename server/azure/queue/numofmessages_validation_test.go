package queue_test

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azqueue"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// newQueueServer stands up a server backed by a fresh Azure Queue Storage driver
// and pre-creates one queue, returning the server so raw requests can exercise
// query-parameter validation the SDK clamps away client-side.
func newQueueServer(t *testing.T, queueName string) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{QueueStorage: cloudP.QueueStorage})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	opts := &azqueue.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	}

	svc, err := azqueue.NewServiceClientWithNoCredential(ts.URL+"/", opts)
	if err != nil {
		t.Fatalf("NewServiceClientWithNoCredential: %v", err)
	}

	if _, err := svc.CreateQueue(context.Background(), queueName, nil); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	return ts
}

func getStatus(t *testing.T, ts *httptest.Server, url string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(body)
}

// TestQueueNumOfMessagesOutOfRangeRejected covers the finding: Get/Peek Messages
// with numofmessages outside [1,32] must be rejected with 400
// OutOfRangeQueryParameterValue (carrying the parameter name/value and bounds),
// not silently clamped, matching real Azure Queue Storage.
func TestQueueNumOfMessagesOutOfRangeRejected(t *testing.T) {
	ts := newQueueServer(t, "numq")
	base := ts.URL + "/numq/messages"

	type errBody struct {
		XMLName             xml.Name `xml:"Error"`
		Code                string   `xml:"Code"`
		QueryParameterName  string   `xml:"QueryParameterName"`
		QueryParameterValue string   `xml:"QueryParameterValue"`
		MinimumAllowed      string   `xml:"MinimumAllowed"`
		MaximumAllowed      string   `xml:"MaximumAllowed"`
	}

	cases := []struct {
		name string
		url  string
		val  string
	}{
		{"dequeue-zero", base + "?numofmessages=0", "0"},
		{"dequeue-too-many", base + "?numofmessages=33", "33"},
		{"dequeue-negative", base + "?numofmessages=-1", "-1"},
		{"peek-too-many", base + "?peekonly=true&numofmessages=33", "33"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := getStatus(t, ts, tc.url)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", status, body)
			}

			var e errBody
			if err := xml.Unmarshal([]byte(body), &e); err != nil {
				t.Fatalf("unmarshal error body: %v; body=%s", err, body)
			}

			if e.Code != "OutOfRangeQueryParameterValue" {
				t.Fatalf("code = %q, want OutOfRangeQueryParameterValue; body=%s", e.Code, body)
			}

			if e.QueryParameterName != "numofmessages" || e.QueryParameterValue != tc.val {
				t.Fatalf("param = %q=%q, want numofmessages=%q", e.QueryParameterName, e.QueryParameterValue, tc.val)
			}

			if e.MinimumAllowed != "1" || e.MaximumAllowed != "32" {
				t.Fatalf("bounds = [%s,%s], want [1,32]", e.MinimumAllowed, e.MaximumAllowed)
			}
		})
	}
}

// TestQueueNumOfMessagesInRangeAccepted confirms the validation does not regress
// valid requests: an in-range value and an absent value (defaulting to 1) both
// return 200.
func TestQueueNumOfMessagesInRangeAccepted(t *testing.T) {
	ts := newQueueServer(t, "okq")
	base := ts.URL + "/okq/messages"

	for _, url := range []string{base, base + "?numofmessages=1", base + "?numofmessages=32"} {
		if status, body := getStatus(t, ts, url); status != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200; body=%s", url, status, body)
		}
	}
}
