package azure_test

// Routing regression tests for the Azure Queue Storage / Service Bus data-plane
// collision (real-user BLOCKER). Both surfaces address a flat
// "/{entity}/messages" URL; behind CloudEmu's shared endpoint the hostname that
// separates them on real Azure is unavailable. The Service Bus handler is
// registered before Queue Storage and used to claim EVERY "/messages" path,
// which swallowed the entire Queue Storage message plane (create/list still
// worked, but enqueue/dequeue/peek/delete/clear 404'd as "Service Bus entity
// not found").
//
// The fix narrows Service Bus's Matches so it claims a flat "/{entity}/messages"
// request only when {entity} resolves to a Service Bus queue or topic it holds.
// These tests drive the FULL production server (NewFromProvider — the same
// wiring `cloudemu serve` uses) with the real azqueue/azblob/aztables SDKs plus
// the Service Bus REST data plane, proving both planes work simultaneously.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/data/aztables"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azqueue"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	sbAPIVer = "?api-version=2022-10-01-preview"
	sbSubID  = "sub-routing"
	sbRG     = "rg-routing"
	sbNS     = "ns-routing"
)

// newFullAzureServer boots the full production Azure server (all handlers
// registered, same as `cloudemu serve`).
func newFullAzureServer(t *testing.T) *httptest.Server {
	t.Helper()

	ts := httptest.NewServer(azureserver.NewFromProvider(cloudemu.NewAzure()))
	t.Cleanup(ts.Close)

	return ts
}

// anonOpts returns SDK client options that route through the test server with
// retries disabled (anonymous access; no SharedKey signing needed).
func anonOpts(ts *httptest.Server) policy.ClientOptions {
	return policy.ClientOptions{
		Transport: ts.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}
}

// sbDo issues a raw HTTP request against the full server (Service Bus REST),
// asserts the status code, and returns the response body as a string.
func sbDo(t *testing.T, ts *httptest.Server, method, path, body string, wantStatus int) string {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	defer resp.Body.Close()

	out, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s = %d, want %d (body %q)", method, path, resp.StatusCode, wantStatus, string(out))
	}

	return string(out)
}

// nsScope is the ARM namespace path prefix.
func nsScope() string {
	return "/subscriptions/" + sbSubID + "/resourceGroups/" + sbRG +
		"/providers/Microsoft.ServiceBus/namespaces/" + sbNS
}

// seedSBNamespace creates the Service Bus namespace via ARM.
func seedSBNamespace(t *testing.T, ts *httptest.Server) {
	t.Helper()

	sbDo(t, ts, http.MethodPut, nsScope()+sbAPIVer, `{"location":"eastus"}`, http.StatusCreated)
}

// newStorageQueueClient returns an azqueue client for a named Storage queue on
// the full server.
func newStorageQueueClient(t *testing.T, ts *httptest.Server, queue string) *azqueue.QueueClient {
	t.Helper()

	qc, err := azqueue.NewQueueClientWithNoCredential(ts.URL+"/"+queue,
		&azqueue.ClientOptions{ClientOptions: anonOpts(ts)})
	if err != nil {
		t.Fatalf("new queue client: %v", err)
	}

	return qc
}

// createStorageQueue creates a Storage queue via the azqueue service client.
func createStorageQueue(t *testing.T, ts *httptest.Server, queue string) {
	t.Helper()

	svc, err := azqueue.NewServiceClientWithNoCredential(ts.URL+"/",
		&azqueue.ClientOptions{ClientOptions: anonOpts(ts)})
	if err != nil {
		t.Fatalf("new service client: %v", err)
	}

	if _, err := svc.CreateQueue(context.Background(), queue, nil); err != nil {
		t.Fatalf("CreateQueue %q: %v", queue, err)
	}
}

// TestQueueStoragePlaneAliveOnFullServer is the core proof: the full azqueue
// message-plane round-trip must succeed against the full server (no 404s from
// Service Bus swallowing the /messages path).
func TestQueueStoragePlaneAliveOnFullServer(t *testing.T) {
	ts := newFullAzureServer(t)
	ctx := context.Background()

	createStorageQueue(t, ts, "sq")
	qc := newStorageQueueClient(t, ts, "sq")

	const payload = "hello-queue-storage"

	if _, err := qc.EnqueueMessage(ctx, payload, nil); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	peek, err := qc.PeekMessage(ctx, nil)
	if err != nil {
		t.Fatalf("PeekMessage: %v", err)
	}

	if len(peek.Messages) != 1 || peek.Messages[0].MessageText == nil || *peek.Messages[0].MessageText != payload {
		t.Fatalf("PeekMessage = %+v, want one message %q", peek.Messages, payload)
	}

	deq, err := qc.DequeueMessage(ctx, nil)
	if err != nil {
		t.Fatalf("DequeueMessage: %v", err)
	}

	if len(deq.Messages) != 1 {
		t.Fatalf("DequeueMessage returned %d messages, want 1", len(deq.Messages))
	}

	msg := deq.Messages[0]
	if msg.MessageText == nil || *msg.MessageText != payload {
		t.Fatalf("dequeued body = %v, want %q", msg.MessageText, payload)
	}

	if _, err := qc.DeleteMessage(ctx, *msg.MessageID, *msg.PopReceipt, nil); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}

	// Enqueue again then clear the whole queue.
	if _, err := qc.EnqueueMessage(ctx, "to-be-cleared", nil); err != nil {
		t.Fatalf("EnqueueMessage (2): %v", err)
	}

	if _, err := qc.ClearMessages(ctx, nil); err != nil {
		t.Fatalf("ClearMessages: %v", err)
	}

	after, err := qc.PeekMessage(ctx, nil)
	if err != nil {
		t.Fatalf("PeekMessage after clear: %v", err)
	}

	if len(after.Messages) != 0 {
		t.Fatalf("queue not empty after ClearMessages: %+v", after.Messages)
	}
}

// TestServiceBusUnaffectedOnFullServer confirms a Service Bus flat-queue
// send/receive round-trip still works end-to-end on the full server.
func TestServiceBusUnaffectedOnFullServer(t *testing.T) {
	ts := newFullAzureServer(t)
	seedSBNamespace(t, ts)

	sbDo(t, ts, http.MethodPut, nsScope()+"/queues/orders"+sbAPIVer, `{"properties":{}}`, http.StatusOK)
	sbDo(t, ts, http.MethodPost, "/"+sbNS+"/orders/messages", "sb-payload", http.StatusCreated)

	got := sbDo(t, ts, http.MethodDelete, "/"+sbNS+"/orders/messages/head", "", http.StatusOK)
	if got != "sb-payload" {
		t.Fatalf("SB received body = %q, want sb-payload", got)
	}
}

// TestServiceBusTopicSubUnaffectedOnFullServer confirms topic publish +
// subscription receive still works on the full server (the subscription path
// shape is unambiguously Service Bus).
func TestServiceBusTopicSubUnaffectedOnFullServer(t *testing.T) {
	ts := newFullAzureServer(t)
	seedSBNamespace(t, ts)

	sbDo(t, ts, http.MethodPut, nsScope()+"/topics/events"+sbAPIVer, `{"properties":{}}`, http.StatusOK)
	sbDo(t, ts, http.MethodPut, nsScope()+"/topics/events/subscriptions/s1"+sbAPIVer, `{"properties":{}}`, http.StatusOK)

	// Publish to the topic (fans out to s1).
	sbDo(t, ts, http.MethodPost, "/"+sbNS+"/events/messages", "topic-payload", http.StatusCreated)

	got := sbDo(t, ts, http.MethodDelete, "/"+sbNS+"/events/subscriptions/s1/messages/head", "", http.StatusOK)
	if got != "topic-payload" {
		t.Fatalf("SB subscription body = %q, want topic-payload", got)
	}
}

// TestStorageQueueAndServiceBusQueueCoexist confirms a Storage queue and a
// Service Bus queue with DIFFERENT names both work at the same time on the full
// server, with no cross-talk.
func TestStorageQueueAndServiceBusQueueCoexist(t *testing.T) {
	ts := newFullAzureServer(t)
	ctx := context.Background()

	// Service Bus queue "sb-queue".
	seedSBNamespace(t, ts)
	sbDo(t, ts, http.MethodPut, nsScope()+"/queues/sb-queue"+sbAPIVer, `{"properties":{}}`, http.StatusOK)

	// Storage queue "store-queue".
	createStorageQueue(t, ts, "store-queue")
	qc := newStorageQueueClient(t, ts, "store-queue")

	// Enqueue to Storage queue; send to SB queue.
	if _, err := qc.EnqueueMessage(ctx, "store-msg", nil); err != nil {
		t.Fatalf("Storage EnqueueMessage: %v", err)
	}

	sbDo(t, ts, http.MethodPost, "/"+sbNS+"/sb-queue/messages", "sb-msg", http.StatusCreated)

	// Storage queue returns its own message.
	deq, err := qc.DequeueMessage(ctx, nil)
	if err != nil {
		t.Fatalf("Storage DequeueMessage: %v", err)
	}

	if len(deq.Messages) != 1 || deq.Messages[0].MessageText == nil || *deq.Messages[0].MessageText != "store-msg" {
		t.Fatalf("Storage dequeue = %+v, want store-msg", deq.Messages)
	}

	// SB queue returns its own message.
	got := sbDo(t, ts, http.MethodDelete, "/"+sbNS+"/sb-queue/messages/head", "", http.StatusOK)
	if got != "sb-msg" {
		t.Fatalf("SB received body = %q, want sb-msg", got)
	}
}

// TestBlobTableRoutingUnaffected smoke-checks that blob and table routing still
// works on the full server (the routing change must not disturb the other
// storage data planes).
func TestBlobTableRoutingUnaffected(t *testing.T) {
	ts := newFullAzureServer(t)
	ctx := context.Background()

	// Blob PUT/GET.
	blobSvc, err := azblob.NewClientWithNoCredential(ts.URL+"/",
		&azblob.ClientOptions{ClientOptions: anonOpts(ts)})
	if err != nil {
		t.Fatalf("new blob client: %v", err)
	}

	if _, err := blobSvc.CreateContainer(ctx, "c1", nil); err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}

	if _, err := blobSvc.UploadBuffer(ctx, "c1", "k1", []byte("blob-body"), nil); err != nil {
		t.Fatalf("UploadBuffer: %v", err)
	}

	dl, err := blobSvc.DownloadStream(ctx, "c1", "k1", nil)
	if err != nil {
		t.Fatalf("DownloadStream: %v", err)
	}

	body, _ := io.ReadAll(dl.Body)
	_ = dl.Body.Close()

	if string(body) != "blob-body" {
		t.Fatalf("blob body = %q, want blob-body", string(body))
	}

	// Table insert/query.
	tableSvc, err := aztables.NewServiceClientWithNoCredential(ts.URL+"/",
		&aztables.ClientOptions{ClientOptions: anonOpts(ts)})
	if err != nil {
		t.Fatalf("new table client: %v", err)
	}

	if _, err := tableSvc.CreateTable(ctx, "people", nil); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	tc := tableSvc.NewClient("people")

	entity, _ := json.Marshal(map[string]any{
		"PartitionKey": "org",
		"RowKey":       "alice",
		"Email":        "alice@example.com",
	})

	if _, err := tc.AddEntity(ctx, entity, nil); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	got, err := tc.GetEntity(ctx, "org", "alice", nil)
	if err != nil {
		t.Fatalf("GetEntity: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(got.Value, &out); err != nil {
		t.Fatalf("unmarshal entity: %v", err)
	}

	if out["Email"] != "alice@example.com" {
		t.Fatalf("table entity Email = %v, want alice@example.com", out["Email"])
	}
}
