package queue_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azqueue"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKQueueRoundTrip drives Azure Queue Storage operations with the real
// azqueue client against our handler. azqueue supports anonymous access, so
// the test doesn't have to forge SharedKey signatures.
func TestSDKQueueRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{QueueStorage: cloudP.QueueStorage})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	svcOpts := &azqueue.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	svcClient, err := azqueue.NewServiceClientWithNoCredential(ts.URL+"/", svcOpts)
	if err != nil {
		t.Fatalf("NewServiceClientWithNoCredential: %v", err)
	}

	// Create queue.
	if _, err := svcClient.CreateQueue(ctx, "q1", nil); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	// List queues and confirm q1 is present.
	{
		pager := svcClient.NewListQueuesPager(nil)

		seen := map[string]bool{}
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				t.Fatalf("ListQueues: %v", err)
			}

			for _, q := range page.Queues {
				if q.Name != nil {
					seen[*q.Name] = true
				}
			}
		}

		if !seen["q1"] {
			t.Errorf("q1 not in queue list: %v", seen)
		}
	}

	qClient, err := azqueue.NewQueueClientWithNoCredential(ts.URL+"/q1", &azqueue.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		t.Fatalf("NewQueueClientWithNoCredential: %v", err)
	}

	// Enqueue a message.
	const payload = "hello, azure queue"

	enq, err := qClient.EnqueueMessage(ctx, payload, nil)
	if err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	if len(enq.Messages) == 0 || enq.Messages[0].MessageID == nil {
		t.Fatalf("EnqueueMessage returned no message id: %+v", enq)
	}

	// Dequeue and verify the message text round-trips.
	deq, err := qClient.DequeueMessage(ctx, nil)
	if err != nil {
		t.Fatalf("DequeueMessage: %v", err)
	}

	if len(deq.Messages) != 1 {
		t.Fatalf("DequeueMessage returned %d messages, want 1", len(deq.Messages))
	}

	msg := deq.Messages[0]
	if msg.MessageText == nil || *msg.MessageText != payload {
		t.Errorf("message text mismatch: got=%v want=%q", msg.MessageText, payload)
	}

	if msg.MessageID == nil || msg.PopReceipt == nil {
		t.Fatalf("dequeued message missing id/popreceipt: %+v", msg)
	}

	// Delete the message using its id + pop receipt.
	if _, err := qClient.DeleteMessage(ctx, *msg.MessageID, *msg.PopReceipt, nil); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}

	// Delete the queue.
	if _, err := qClient.Delete(ctx, nil); err != nil {
		t.Fatalf("Delete queue: %v", err)
	}

	// Confirm the queue is gone: enqueue should now fail with QueueNotFound.
	if _, err := qClient.EnqueueMessage(ctx, "x", nil); err == nil ||
		!strings.Contains(err.Error(), "QueueNotFound") {
		t.Errorf("expected QueueNotFound after delete, got %v", err)
	}
}
