package sqs_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
)

const (
	fifoAttr               = "FifoQueue"
	attrTrueValue          = "true"
	shortSleep             = 300 * time.Millisecond
	receiveWaitSecs        = 2
	maxReceiveMessagesTest = 10
)

func createFIFOQueueSDK(t *testing.T, client *awssqs.Client, name string) string {
	t.Helper()

	out, err := client.CreateQueue(context.Background(), &awssqs.CreateQueueInput{
		QueueName:  aws.String(name),
		Attributes: map[string]string{fifoAttr: attrTrueValue},
	})
	if err != nil {
		t.Fatalf("CreateQueue(%s): %v", name, err)
	}

	return aws.ToString(out.QueueUrl)
}

func sendFIFO(t *testing.T, client *awssqs.Client, queueURL, body, group, dedup string) {
	t.Helper()

	if _, err := client.SendMessage(context.Background(), &awssqs.SendMessageInput{
		QueueUrl:               aws.String(queueURL),
		MessageBody:            aws.String(body),
		MessageGroupId:         aws.String(group),
		MessageDeduplicationId: aws.String(dedup),
	}); err != nil {
		t.Fatalf("SendMessage(%s): %v", body, err)
	}
}

// receiveBodies receives up to 10 messages and returns a body->receiptHandle map.
func receiveBodies(t *testing.T, client *awssqs.Client, queueURL string) map[string]string {
	t.Helper()

	out, err := client.ReceiveMessage(context.Background(), &awssqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueURL),
		MaxNumberOfMessages: maxReceiveMessagesTest,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	handles := make(map[string]string, len(out.Messages))
	for i := range out.Messages {
		handles[aws.ToString(out.Messages[i].Body)] = aws.ToString(out.Messages[i].ReceiptHandle)
	}

	return handles
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

func deleteHandle(t *testing.T, client *awssqs.Client, queueURL, handle string) {
	t.Helper()

	if _, err := client.DeleteMessage(context.Background(), &awssqs.DeleteMessageInput{
		QueueUrl:      aws.String(queueURL),
		ReceiptHandle: aws.String(handle),
	}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
}

// TestSDKFIFOInFlightGroupBlocking verifies that a FIFO group with an in-flight
// message does not deliver its next message until the in-flight one is deleted,
// while a different group is delivered in parallel.
func TestSDKFIFOInFlightGroupBlocking(t *testing.T) {
	client, _ := newSDKClient(t)
	queueURL := createFIFOQueueSDK(t, client, "orders.fifo")

	sendFIFO(t, client, queueURL, "a", "G1", "d-a")
	sendFIFO(t, client, queueURL, "b", "G1", "d-b")
	sendFIFO(t, client, queueURL, "x", "G2", "d-x")

	// First receive: head of each group (a, x) delivered in parallel, never b
	// while a of the same group is still in-flight.
	first := receiveBodies(t, client, queueURL)
	if got := sortedKeys(first); len(got) != 2 || got[0] != "a" || got[1] != "x" {
		t.Fatalf("first receive = %v, want [a x] (b must stay blocked behind a)", got)
	}

	// Deleting a (the in-flight head of G1) must unblock b.
	deleteHandle(t, client, queueURL, first["a"])

	next := receiveBodies(t, client, queueURL)
	if got := sortedKeys(next); len(got) != 1 || got[0] != "b" {
		t.Fatalf("after deleting a, receive = %v, want [b]", got)
	}
}

// TestSDKLongPollPicksUpMidWindow verifies that a WaitTimeSeconds receive on an
// empty queue waits and returns a message a concurrent producer sends mid-window.
func TestSDKLongPollPicksUpMidWindow(t *testing.T) {
	client, _ := newSDKClient(t)

	out, err := client.CreateQueue(context.Background(), &awssqs.CreateQueueInput{
		QueueName: aws.String("longpoll"),
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	queueURL := aws.ToString(out.QueueUrl)

	go func() {
		time.Sleep(shortSleep)

		_, _ = client.SendMessage(context.Background(), &awssqs.SendMessageInput{
			QueueUrl:    aws.String(queueURL),
			MessageBody: aws.String("delayed"),
		})
	}()

	start := time.Now()

	rcv, err := client.ReceiveMessage(context.Background(), &awssqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueURL),
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     receiveWaitSecs,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	elapsed := time.Since(start)

	if len(rcv.Messages) != 1 || aws.ToString(rcv.Messages[0].Body) != "delayed" {
		t.Fatalf("long poll got %d messages, want the mid-window message", len(rcv.Messages))
	}

	if elapsed < shortSleep/2 {
		t.Fatalf("long poll returned too early (%v); it did not actually wait", elapsed)
	}

	if elapsed > (receiveWaitSecs-1)*time.Second {
		t.Fatalf("long poll returned at %v; should have returned soon after the mid-window send", elapsed)
	}
}

// TestSDKLongPollEmptyWaitsFullWindow verifies that with no producer, a
// WaitTimeSeconds receive waits ~the full window and then returns empty.
func TestSDKLongPollEmptyWaitsFullWindow(t *testing.T) {
	client, _ := newSDKClient(t)

	out, err := client.CreateQueue(context.Background(), &awssqs.CreateQueueInput{
		QueueName: aws.String("longpoll-empty"),
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	start := time.Now()

	rcv, err := client.ReceiveMessage(context.Background(), &awssqs.ReceiveMessageInput{
		QueueUrl:            out.QueueUrl,
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     receiveWaitSecs,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	elapsed := time.Since(start)

	if len(rcv.Messages) != 0 {
		t.Fatalf("empty long poll got %d messages, want 0", len(rcv.Messages))
	}

	if elapsed < (receiveWaitSecs-1)*time.Second {
		t.Fatalf("empty long poll returned after %v; it should wait near the full %ds window", elapsed, receiveWaitSecs)
	}
}

// TestSDKLongPollRespectsContext verifies that a canceled request context aborts
// the long-poll wait promptly instead of blocking for the full WaitTimeSeconds.
func TestSDKLongPollRespectsContext(t *testing.T) {
	client, _ := newSDKClient(t)

	out, err := client.CreateQueue(context.Background(), &awssqs.CreateQueueInput{
		QueueName: aws.String("longpoll-ctx"),
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), shortSleep)
	defer cancel()

	start := time.Now()

	_, err = client.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:            out.QueueUrl,
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     maxReceiveMessagesTest * 2, // 20s window
	})

	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("canceled long poll returned nil error, want a context error")
	}

	if elapsed > time.Second {
		t.Fatalf("canceled long poll took %v; ctx cancellation was not honored promptly", elapsed)
	}
}
