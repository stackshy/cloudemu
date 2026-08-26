package queue_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azqueue"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// minVisibilityGap is the lower bound we require between a dequeued message's
// InsertionTime and TimeNextVisible: the driver defaults an omitted visibility
// timeout to 30s, so the gap must be clearly non-zero (the pre-fix bug reported
// TimeNextVisible == dequeue time, a ~0 gap).
const minVisibilityGap = 20 * time.Second

// newFullServerClient stands up the full Azure wire server from a real provider
// (NewFromProvider) and returns an azqueue service client pointed at it, plus the
// test server so callers can build per-queue clients that share its transport.
func newFullServerClient(t *testing.T) (*azqueue.ServiceClient, *httptest.Server) {
	t.Helper()

	srv := azureserver.NewFromProvider(cloudemu.NewAzure())
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := azqueue.NewServiceClientWithNoCredential(ts.URL+"/", clientOpts(ts))
	if err != nil {
		t.Fatalf("NewServiceClientWithNoCredential: %v", err)
	}

	return svc, ts
}

func clientOpts(ts *httptest.Server) *azqueue.ClientOptions {
	return &azqueue.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	}
}

func queueClientFor(t *testing.T, ts *httptest.Server, name string) *azqueue.QueueClient {
	t.Helper()

	qc, err := azqueue.NewQueueClientWithNoCredential(ts.URL+"/"+name, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewQueueClientWithNoCredential: %v", err)
	}

	return qc
}

// TestQueueCreateMetadataRoundTrips (BUG 1): metadata supplied on Create Queue is
// persisted and returned by Get Properties.
func TestQueueCreateMetadataRoundTrips(t *testing.T) {
	ctx := context.Background()
	svc, ts := newFullServerClient(t)

	// HTTP header canonicalization title-cases metadata keys, so use a canonical key.
	opts := &azqueue.CreateOptions{Metadata: map[string]*string{"Team": to.Ptr("payments")}}
	if _, err := svc.CreateQueue(ctx, "b1q", opts); err != nil {
		t.Fatalf("CreateQueue with metadata: %v", err)
	}

	props, err := queueClientFor(t, ts, "b1q").GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	if v, ok := props.Metadata["Team"]; !ok || v == nil || *v != "payments" {
		t.Errorf("metadata Team = %v, want payments", props.Metadata["Team"])
	}
}

// TestQueueDeleteStaleReceiptIsMessageNotFound (BUG 2): deleting with a pop
// receipt that no longer matches returns 404 MessageNotFound, not QueueNotFound.
func TestQueueDeleteStaleReceiptIsMessageNotFound(t *testing.T) {
	ctx := context.Background()
	svc, ts := newFullServerClient(t)

	if _, err := svc.CreateQueue(ctx, "b2q", nil); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	qc := queueClientFor(t, ts, "b2q")

	enq, err := qc.EnqueueMessage(ctx, "m", nil)
	if err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	msgID := *enq.Messages[0].MessageID

	// A dequeue issues a fresh pop receipt, so the enqueue receipt is now stale.
	if _, err := qc.DequeueMessage(ctx, nil); err != nil {
		t.Fatalf("DequeueMessage: %v", err)
	}

	stale := *enq.Messages[0].PopReceipt

	_, err = qc.DeleteMessage(ctx, msgID, stale, nil)
	if err == nil || !strings.Contains(err.Error(), "MessageNotFound") {
		t.Errorf("delete with stale receipt: got %v, want MessageNotFound", err)
	}
}

// TestQueueEnqueueReceiptUsableBeforeDequeue (BUG 3): the pop receipt returned by
// Put Message can delete the message before any dequeue.
func TestQueueEnqueueReceiptUsableBeforeDequeue(t *testing.T) {
	ctx := context.Background()
	svc, ts := newFullServerClient(t)

	if _, err := svc.CreateQueue(ctx, "b3q", nil); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	qc := queueClientFor(t, ts, "b3q")

	enq, err := qc.EnqueueMessage(ctx, "m", nil)
	if err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	msg := enq.Messages[0]
	if msg.MessageID == nil || msg.PopReceipt == nil {
		t.Fatalf("enqueue returned no id/popreceipt: %+v", msg)
	}

	// Delete using the enqueue-returned receipt, without dequeuing first.
	if _, err := qc.DeleteMessage(ctx, *msg.MessageID, *msg.PopReceipt, nil); err != nil {
		t.Fatalf("DeleteMessage with enqueue receipt: %v", err)
	}

	peek, err := qc.PeekMessages(ctx, &azqueue.PeekMessagesOptions{NumberOfMessages: to.Ptr(int32(1))})
	if err != nil {
		t.Fatalf("PeekMessages: %v", err)
	}

	if len(peek.Messages) != 0 {
		t.Errorf("queue still has %d messages after delete, want 0", len(peek.Messages))
	}
}

// TestQueueDequeueReportsAccurateTimes (BUG 4): InsertionTime is the enqueue time
// and TimeNextVisible is in the future by the effective (defaulted) visibility.
func TestQueueDequeueReportsAccurateTimes(t *testing.T) {
	ctx := context.Background()
	svc, ts := newFullServerClient(t)

	if _, err := svc.CreateQueue(ctx, "b4q", nil); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	qc := queueClientFor(t, ts, "b4q")

	before := time.Now()

	if _, err := qc.EnqueueMessage(ctx, "m", nil); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	deq, err := qc.DequeueMessage(ctx, nil)
	if err != nil {
		t.Fatalf("DequeueMessage: %v", err)
	}

	after := time.Now()

	m := deq.Messages[0]
	if m.InsertionTime == nil || m.TimeNextVisible == nil {
		t.Fatalf("dequeue missing times: %+v", m)
	}

	// InsertionTime must be the enqueue instant, inside the test's real-time window
	// (RFC1123 truncates to whole seconds, hence the 1s slack).
	if m.InsertionTime.Before(before.Add(-time.Second)) || m.InsertionTime.After(after.Add(time.Second)) {
		t.Errorf("InsertionTime %v outside enqueue window [%v, %v]", *m.InsertionTime, before, after)
	}

	// TimeNextVisible must be pushed out by the driver's effective 30s default.
	if gap := m.TimeNextVisible.Sub(*m.InsertionTime); gap < minVisibilityGap {
		t.Errorf("TimeNextVisible gap = %v, want >= %v (30s default applied)", gap, minVisibilityGap)
	}
}

// TestQueueDuplicateCreateIdempotency (BUG 5): a duplicate Create with identical
// metadata is a 204 no-op; different metadata is a 409 QueueAlreadyExists.
func TestQueueDuplicateCreateIdempotency(t *testing.T) {
	ctx := context.Background()
	svc, _ := newFullServerClient(t)

	same := &azqueue.CreateOptions{Metadata: map[string]*string{"Team": to.Ptr("payments")}}
	if _, err := svc.CreateQueue(ctx, "b5q", same); err != nil {
		t.Fatalf("initial CreateQueue: %v", err)
	}

	// Identical metadata -> idempotent success (204).
	if _, err := svc.CreateQueue(ctx, "b5q", same); err != nil {
		t.Errorf("duplicate CreateQueue with identical metadata: got %v, want success", err)
	}

	// Different metadata -> 409 QueueAlreadyExists.
	diff := &azqueue.CreateOptions{Metadata: map[string]*string{"Team": to.Ptr("billing")}}

	_, err := svc.CreateQueue(ctx, "b5q", diff)
	if err == nil || !strings.Contains(err.Error(), "QueueAlreadyExists") {
		t.Errorf("duplicate CreateQueue with different metadata: got %v, want QueueAlreadyExists", err)
	}
}
