package azure

import (
	"context"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azqueue"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestAzuremessagequeueCompat drives an Azure Queue Storage lifecycle through
// the real azure-sdk-for-go azqueue client. Azure queues map onto the portable
// "messagequeue" driver, so operation names match SQS's in
// docs/coverage/coverage.json. azqueue supports anonymous access, so the
// emulator is reached over the plain test-server transport with retries off.
func TestAzuremessagequeueCompat(t *testing.T) {
	provider := cloudemu.NewAzure()
	sess := compat.BootAzure(t, azureserver.Drivers{QueueStorage: provider.QueueStorage})

	ctx := context.Background()

	const (
		svc     = "messagequeue"
		queue   = "compat-queue"
		payload = "hello cloudemu"
	)

	clientOpts := &azqueue.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: sess.Transport(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	svcClient, err := azqueue.NewServiceClientWithNoCredential(sess.Endpoint()+"/", clientOpts)
	if err != nil {
		t.Fatalf("service client: %v", err)
	}

	qClient, err := azqueue.NewQueueClientWithNoCredential(sess.Endpoint()+"/"+queue, clientOpts)
	if err != nil {
		t.Fatalf("queue client: %v", err)
	}

	sess.Op(svc, "CreateQueue", func() error {
		_, err := svcClient.CreateQueue(ctx, queue, nil)
		return err
	})

	sess.Op(svc, "ListQueues", func() error {
		pager := svcClient.NewListQueuesPager(nil)

		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return err
			}

			for _, q := range page.Queues {
				if q.Name != nil && *q.Name == queue {
					return nil
				}
			}
		}

		return fmt.Errorf("queue %q not found in list", queue)
	})

	sess.Op(svc, "SendMessage", func() error {
		_, err := qClient.EnqueueMessage(ctx, payload, nil)
		return err
	})

	var (
		messageID  string
		popReceipt string
	)

	sess.Op(svc, "ReceiveMessages", func() error {
		resp, err := qClient.DequeueMessage(ctx, nil)
		if err != nil {
			return err
		}

		if len(resp.Messages) != 1 {
			return fmt.Errorf("expected 1 message, got %d", len(resp.Messages))
		}

		msg := resp.Messages[0]
		if msg.MessageText == nil || *msg.MessageText != payload {
			return fmt.Errorf("message text mismatch: got %v want %q", msg.MessageText, payload)
		}

		if msg.MessageID == nil || msg.PopReceipt == nil {
			return fmt.Errorf("dequeued message missing id/popreceipt: %+v", msg)
		}

		messageID = *msg.MessageID
		popReceipt = *msg.PopReceipt

		return nil
	})

	sess.Op(svc, "DeleteMessage", func() error {
		_, err := qClient.DeleteMessage(ctx, messageID, popReceipt, nil)
		return err
	})

	// Enqueue again so PurgeQueue has something to clear.
	if _, err := qClient.EnqueueMessage(ctx, payload, nil); err != nil {
		t.Fatalf("re-enqueue before purge: %v", err)
	}

	sess.Op(svc, "PurgeQueue", func() error {
		_, err := qClient.ClearMessages(ctx, nil)
		return err
	})

	sess.Op(svc, "DeleteQueue", func() error {
		_, err := qClient.Delete(ctx, nil)
		return err
	})
}
