package queue_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azqueue"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func newQueueClient(t *testing.T, name string) *azqueue.QueueClient {
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

	if _, err := svc.CreateQueue(context.Background(), name, nil); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	qc, err := azqueue.NewQueueClientWithNoCredential(ts.URL+"/"+name, opts)
	if err != nil {
		t.Fatalf("NewQueueClientWithNoCredential: %v", err)
	}

	return qc
}

// TestQueuePeekIsNonDestructive covers finding #4: a peek leaves the message on
// the queue so a later dequeue still returns it.
func TestQueuePeekIsNonDestructive(t *testing.T) {
	ctx := context.Background()
	qc := newQueueClient(t, "peekq")

	if _, err := qc.EnqueueMessage(ctx, "payload", nil); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	peek, err := qc.PeekMessage(ctx, nil)
	if err != nil {
		t.Fatalf("PeekMessage: %v", err)
	}

	if len(peek.Messages) != 1 || peek.Messages[0].MessageText == nil || *peek.Messages[0].MessageText != "payload" {
		t.Fatalf("peek = %+v, want the payload", peek.Messages)
	}

	// The peek must not have hidden the message.
	deq, err := qc.DequeueMessage(ctx, nil)
	if err != nil {
		t.Fatalf("DequeueMessage: %v", err)
	}

	if len(deq.Messages) != 1 {
		t.Fatalf("dequeue after peek returned %d messages, want 1", len(deq.Messages))
	}
}

// TestQueueUpdateMessage covers finding #5: Update Message renews visibility,
// replaces the body, and returns a fresh pop receipt.
func TestQueueUpdateMessage(t *testing.T) {
	ctx := context.Background()
	qc := newQueueClient(t, "updq")

	if _, err := qc.EnqueueMessage(ctx, "original", nil); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	deq, err := qc.DequeueMessage(ctx, nil)
	if err != nil {
		t.Fatalf("DequeueMessage: %v", err)
	}

	msg := deq.Messages[0]

	upd, err := qc.UpdateMessage(ctx, *msg.MessageID, *msg.PopReceipt, "updated",
		&azqueue.UpdateMessageOptions{VisibilityTimeout: to.Ptr(int32(0))})
	if err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}

	if upd.PopReceipt == nil || *upd.PopReceipt == *msg.PopReceipt {
		t.Errorf("UpdateMessage did not return a fresh pop receipt: %+v", upd.PopReceipt)
	}

	peek, err := qc.PeekMessage(ctx, nil)
	if err != nil {
		t.Fatalf("PeekMessage: %v", err)
	}

	if len(peek.Messages) != 1 || peek.Messages[0].MessageText == nil || *peek.Messages[0].MessageText != "updated" {
		t.Fatalf("peek after update = %+v, want body 'updated'", peek.Messages)
	}
}

// TestQueueUpdateMessageBadReceipt: an unknown pop receipt is a 404 MessageNotFound.
func TestQueueUpdateMessageBadReceipt(t *testing.T) {
	ctx := context.Background()
	qc := newQueueClient(t, "updbad")

	enq, err := qc.EnqueueMessage(ctx, "x", nil)
	if err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	_, err = qc.UpdateMessage(ctx, *enq.Messages[0].MessageID, "not-a-real-receipt", "y", nil)
	if err == nil {
		t.Fatal("UpdateMessage with a bogus pop receipt succeeded, want an error")
	}
}

// TestQueueGetPropertiesAndSetMetadata covers finding #6: queue metadata and the
// approximate message count are served.
func TestQueueGetPropertiesAndSetMetadata(t *testing.T) {
	ctx := context.Background()
	qc := newQueueClient(t, "metaq")

	// HTTP header canonicalization title-cases metadata keys on the wire, so we
	// use an already-canonical key to keep the round-trip deterministic.
	if _, err := qc.SetMetadata(ctx, &azqueue.SetMetadataOptions{
		Metadata: map[string]*string{"Team": to.Ptr("payments")},
	}); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}

	for range [2]int{} {
		if _, err := qc.EnqueueMessage(ctx, "m", nil); err != nil {
			t.Fatalf("EnqueueMessage: %v", err)
		}
	}

	props, err := qc.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	if props.ApproximateMessagesCount == nil || *props.ApproximateMessagesCount != 2 {
		t.Errorf("ApproximateMessagesCount = %v, want 2", props.ApproximateMessagesCount)
	}

	if v, ok := props.Metadata["Team"]; !ok || v == nil || *v != "payments" {
		t.Errorf("metadata Team = %v, want payments", props.Metadata["Team"])
	}
}

// TestQueueDequeueUpTo32 covers the Azure Queue Storage max-messages limit: a
// single Get Messages call may return up to 32 messages, not Service Bus's 10.
func TestQueueDequeueUpTo32(t *testing.T) {
	ctx := context.Background()
	qc := newQueueClient(t, "maxq")

	const want = 12
	for range [want]int{} {
		if _, err := qc.EnqueueMessage(ctx, "m", nil); err != nil {
			t.Fatalf("EnqueueMessage: %v", err)
		}
	}

	deq, err := qc.DequeueMessages(ctx, &azqueue.DequeueMessagesOptions{
		NumberOfMessages: to.Ptr(int32(want)),
	})
	if err != nil {
		t.Fatalf("DequeueMessages: %v", err)
	}

	if len(deq.Messages) != want {
		t.Fatalf("dequeued %d messages, want %d (Azure allows up to 32 per call)", len(deq.Messages), want)
	}
}

// TestQueuePeekUpTo32 covers the same 32-message limit on the Peek Messages path.
func TestQueuePeekUpTo32(t *testing.T) {
	ctx := context.Background()
	qc := newQueueClient(t, "peekmaxq")

	const want = 12
	for range [want]int{} {
		if _, err := qc.EnqueueMessage(ctx, "m", nil); err != nil {
			t.Fatalf("EnqueueMessage: %v", err)
		}
	}

	peek, err := qc.PeekMessages(ctx, &azqueue.PeekMessagesOptions{
		NumberOfMessages: to.Ptr(int32(want)),
	})
	if err != nil {
		t.Fatalf("PeekMessages: %v", err)
	}

	if len(peek.Messages) != want {
		t.Fatalf("peeked %d messages, want %d (Azure allows up to 32 per call)", len(peek.Messages), want)
	}
}

// TestQueueMetadataCountsInFlight covers the x-ms-approximate-messages-count
// semantics: the count reflects total messages in the queue, including ones that
// are invisible because they are mid-flight (dequeued but not yet deleted).
func TestQueueMetadataCountsInFlight(t *testing.T) {
	ctx := context.Background()
	qc := newQueueClient(t, "inflightq")

	for range [2]int{} {
		if _, err := qc.EnqueueMessage(ctx, "m", nil); err != nil {
			t.Fatalf("EnqueueMessage: %v", err)
		}
	}

	// Dequeue one message; it becomes invisible for the visibility timeout but
	// remains in the queue until deleted.
	if _, err := qc.DequeueMessage(ctx, nil); err != nil {
		t.Fatalf("DequeueMessage: %v", err)
	}

	props, err := qc.GetProperties(ctx, nil)
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}

	if props.ApproximateMessagesCount == nil || *props.ApproximateMessagesCount != 2 {
		t.Errorf("ApproximateMessagesCount = %v, want 2 (both visible and in-flight)", props.ApproximateMessagesCount)
	}
}

// TestQueueDequeueCountIncrements covers finding #9: DequeueCount reflects the
// real number of receives, not a hardcoded 1.
func TestQueueDequeueCountIncrements(t *testing.T) {
	ctx := context.Background()
	qc := newQueueClient(t, "dcq")

	if _, err := qc.EnqueueMessage(ctx, "m", nil); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	first, err := qc.DequeueMessage(ctx, nil)
	if err != nil {
		t.Fatalf("first DequeueMessage: %v", err)
	}

	if c := first.Messages[0].DequeueCount; c == nil || *c != 1 {
		t.Fatalf("first dequeue count = %v, want 1", c)
	}

	msg := first.Messages[0]

	// Make the message visible again without incrementing its dequeue count.
	if _, err := qc.UpdateMessage(ctx, *msg.MessageID, *msg.PopReceipt, "m",
		&azqueue.UpdateMessageOptions{VisibilityTimeout: to.Ptr(int32(0))}); err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}

	second, err := qc.DequeueMessage(ctx, nil)
	if err != nil {
		t.Fatalf("second DequeueMessage: %v", err)
	}

	if len(second.Messages) != 1 {
		t.Fatalf("second dequeue returned %d messages, want 1", len(second.Messages))
	}

	if c := second.Messages[0].DequeueCount; c == nil || *c != 2 {
		t.Fatalf("second dequeue count = %v, want 2", c)
	}
}
