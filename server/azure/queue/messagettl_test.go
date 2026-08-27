package queue_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azqueue"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKQueueMessageTTL drives Put Message's messagettl query parameter
// through the real azqueue client, confirming a short-TTL message is lazily
// dropped from Get/Peek Messages once it expires, while a messagettl=-1
// message survives indefinitely.
func TestSDKQueueMessageTTL(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	cloudP := cloudemu.NewAzure(config.WithClock(fc))
	srv := azureserver.New(azureserver.Drivers{QueueStorage: cloudP.QueueStorage})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	opts := &azqueue.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	}

	svcClient, err := azqueue.NewServiceClientWithNoCredential(ts.URL+"/", opts)
	if err != nil {
		t.Fatalf("NewServiceClientWithNoCredential: %v", err)
	}

	const queueName = "ttlq"

	if _, err := svcClient.CreateQueue(ctx, queueName, nil); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	qClient, err := azqueue.NewQueueClientWithNoCredential(ts.URL+"/"+queueName, opts)
	if err != nil {
		t.Fatalf("NewQueueClientWithNoCredential: %v", err)
	}

	shortLived := int32(5)   // expires 5s after enqueue
	neverExpire := int32(-1) // never expires

	if _, err := qClient.EnqueueMessage(ctx, "expires-soon", &azqueue.EnqueueMessageOptions{
		TimeToLive: &shortLived,
	}); err != nil {
		t.Fatalf("EnqueueMessage (short TTL): %v", err)
	}

	if _, err := qClient.EnqueueMessage(ctx, "never-expires", &azqueue.EnqueueMessageOptions{
		TimeToLive: &neverExpire,
	}); err != nil {
		t.Fatalf("EnqueueMessage (never expire): %v", err)
	}

	numMsgs := int32(10)

	// Before the TTL elapses, both messages are visible.
	before, err := qClient.PeekMessages(ctx, &azqueue.PeekMessagesOptions{NumberOfMessages: &numMsgs})
	if err != nil {
		t.Fatalf("PeekMessages (before expiry): %v", err)
	}

	if len(before.Messages) != 2 {
		t.Fatalf("PeekMessages before expiry: got %d messages, want 2", len(before.Messages))
	}

	// Advance well past the short-lived message's TTL.
	fc.Advance(10 * time.Second)

	afterPeek, err := qClient.PeekMessages(ctx, &azqueue.PeekMessagesOptions{NumberOfMessages: &numMsgs})
	if err != nil {
		t.Fatalf("PeekMessages (after expiry): %v", err)
	}

	if len(afterPeek.Messages) != 1 {
		t.Fatalf("PeekMessages after expiry: got %d messages, want 1 (only the never-expiring one)", len(afterPeek.Messages))
	}

	if got := *afterPeek.Messages[0].MessageText; got != "never-expires" {
		t.Errorf("surviving message text = %q, want %q", got, "never-expires")
	}

	afterDequeue, err := qClient.DequeueMessages(ctx, &azqueue.DequeueMessagesOptions{NumberOfMessages: &numMsgs})
	if err != nil {
		t.Fatalf("DequeueMessages (after expiry): %v", err)
	}

	if len(afterDequeue.Messages) != 1 {
		t.Fatalf("DequeueMessages after expiry: got %d messages, want 1", len(afterDequeue.Messages))
	}

	if got := *afterDequeue.Messages[0].MessageText; got != "never-expires" {
		t.Errorf("dequeued message text = %q, want %q", got, "never-expires")
	}
}

// TestSDKQueueMessageTTLInvalid confirms an out-of-range messagettl (0, which
// is neither a positive TTL nor the -1 "never expire" sentinel) is rejected
// with a 400 rather than silently accepted.
func TestSDKQueueMessageTTLInvalid(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{QueueStorage: cloudP.QueueStorage})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	opts := &azqueue.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	}

	svcClient, err := azqueue.NewServiceClientWithNoCredential(ts.URL+"/", opts)
	if err != nil {
		t.Fatalf("NewServiceClientWithNoCredential: %v", err)
	}

	const queueName = "ttlbad"

	if _, err := svcClient.CreateQueue(ctx, queueName, nil); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	qClient, err := azqueue.NewQueueClientWithNoCredential(ts.URL+"/"+queueName, opts)
	if err != nil {
		t.Fatalf("NewQueueClientWithNoCredential: %v", err)
	}

	zero := int32(0)
	if _, err := qClient.EnqueueMessage(ctx, "x", &azqueue.EnqueueMessageOptions{TimeToLive: &zero}); err == nil {
		t.Fatal("EnqueueMessage with messagettl=0 succeeded, want an error")
	}
}
