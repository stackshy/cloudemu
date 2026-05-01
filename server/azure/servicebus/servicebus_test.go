package servicebus_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

const (
	subID  = "00000000-0000-0000-0000-000000000000"
	rgName = "rg-1"
	nsName = "test-ns"
	apiVer = "?api-version=2022-10-01-preview"
)

func nsURL() string {
	return "/subscriptions/" + subID + "/resourceGroups/" + rgName +
		"/providers/Microsoft.ServiceBus/namespaces/" + nsName
}

func queueURL(name string) string {
	return nsURL() + "/queues/" + name
}

func newTestServer(t *testing.T) (*httptest.Server, *cloudemuHandle) {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := httptest.NewServer(azureserver.New(azureserver.Drivers{ServiceBus: cloud.ServiceBus}))
	t.Cleanup(srv.Close)

	return srv, &cloudemuHandle{provider: cloud}
}

type cloudemuHandle struct {
	provider any
}

func TestNamespaceLifecycle(t *testing.T) {
	srv, _ := newTestServer(t)

	put := doRequest(t, srv, http.MethodPut, nsURL()+apiVer, `{"location":"eastus"}`)
	if put.StatusCode != http.StatusOK {
		t.Fatalf("PUT namespace = %d", put.StatusCode)
	}

	get := doRequest(t, srv, http.MethodGet, nsURL()+apiVer, "")
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET namespace = %d", get.StatusCode)
	}

	body := readBody(t, get)
	if !strings.Contains(body, "Succeeded") {
		t.Fatalf("namespace body missing provisioningState: %s", body)
	}

	del := doRequest(t, srv, http.MethodDelete, nsURL()+apiVer, "")
	if del.StatusCode != http.StatusOK {
		t.Fatalf("DELETE namespace = %d", del.StatusCode)
	}
}

func TestQueueLifecycle(t *testing.T) {
	srv, _ := newTestServer(t)

	body := `{"properties":{"maxSizeInMegabytes":1024}}`

	put := doRequest(t, srv, http.MethodPut, queueURL("orders")+apiVer, body)
	if put.StatusCode != http.StatusOK {
		t.Fatalf("PUT queue = %d, body: %s", put.StatusCode, readBody(t, put))
	}

	get := doRequest(t, srv, http.MethodGet, queueURL("orders")+apiVer, "")
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET queue = %d", get.StatusCode)
	}

	var got struct {
		Name       string `json:"name"`
		Properties struct {
			Status string `json:"status"`
		} `json:"properties"`
	}

	if err := json.NewDecoder(get.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	_ = get.Body.Close()

	if got.Name != "orders" {
		t.Fatalf("Name = %q, want orders", got.Name)
	}

	if got.Properties.Status != "Active" {
		t.Fatalf("Status = %q, want Active", got.Properties.Status)
	}

	del := doRequest(t, srv, http.MethodDelete, queueURL("orders")+apiVer, "")
	if del.StatusCode != http.StatusOK {
		t.Fatalf("DELETE queue = %d", del.StatusCode)
	}

	missing := doRequest(t, srv, http.MethodGet, queueURL("orders")+apiVer, "")
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("post-delete GET = %d, want 404", missing.StatusCode)
	}
}

func TestQueueIdempotentPut(t *testing.T) {
	srv, _ := newTestServer(t)

	body := `{"properties":{}}`

	first := doRequest(t, srv, http.MethodPut, queueURL("idem")+apiVer, body)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first PUT = %d", first.StatusCode)
	}

	second := doRequest(t, srv, http.MethodPut, queueURL("idem")+apiVer, body)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second PUT = %d (idempotent expected)", second.StatusCode)
	}
}

func TestListQueues(t *testing.T) {
	srv, _ := newTestServer(t)

	for _, n := range []string{"a", "b", "c"} {
		_ = doRequest(t, srv, http.MethodPut, queueURL(n)+apiVer, `{"properties":{}}`)
	}

	resp := doRequest(t, srv, http.MethodGet, nsURL()+"/queues"+apiVer, "")

	var got struct {
		Value []map[string]any `json:"value"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	_ = resp.Body.Close()

	if len(got.Value) != 3 {
		t.Fatalf("listed %d queues, want 3", len(got.Value))
	}
}

func TestDataPlaneSendReceive(t *testing.T) {
	srv, _ := newTestServer(t)

	if r := doRequest(t, srv, http.MethodPut, queueURL("loop")+apiVer,
		`{"properties":{}}`); r.StatusCode != http.StatusOK {
		t.Fatalf("create queue: %d", r.StatusCode)
	}

	send := doRequest(t, srv, http.MethodPost, "/"+nsName+"/loop/messages", "hello")
	if send.StatusCode != http.StatusCreated {
		t.Fatalf("send = %d, want 201", send.StatusCode)
	}

	rcv := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/loop/messages/head", "")
	if rcv.StatusCode != http.StatusOK {
		t.Fatalf("receive = %d, want 200", rcv.StatusCode)
	}

	got := readBody(t, rcv)
	if got != "hello" {
		t.Fatalf("body = %q, want hello", got)
	}

	// Subsequent receive on empty queue should return 204.
	empty := doRequest(t, srv, http.MethodDelete, "/"+nsName+"/loop/messages/head", "")
	if empty.StatusCode != http.StatusNoContent {
		t.Fatalf("empty receive = %d, want 204", empty.StatusCode)
	}
}

func TestDataPlaneSendToMissingQueue(t *testing.T) {
	srv, _ := newTestServer(t)

	resp := doRequest(t, srv, http.MethodPost, "/"+nsName+"/no-such/messages", "x")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("send to missing queue = %d, want 404", resp.StatusCode)
	}
}

// helpers --------------------------------------------------------------------

func doRequest(t *testing.T, srv *httptest.Server, method, path, body string) *http.Response {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	req, _ := http.NewRequestWithContext(context.Background(), method, srv.URL+path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return strings.TrimSpace(string(b))
}
