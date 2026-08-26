package sqs_test

import (
	"context"
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

// receiveOrdered receives up to max messages and returns bodies and matching
// receipt handles in the order the server returned them (FIFO delivery order).
func receiveOrdered(t *testing.T, client *awssqs.Client, queueURL string, max int32) ([]string, []string) {
	t.Helper()

	out, err := client.ReceiveMessage(context.Background(), &awssqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueURL),
		MaxNumberOfMessages: max,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage: %v", err)
	}

	bodies := make([]string, 0, len(out.Messages))
	handles := make([]string, 0, len(out.Messages))

	for i := range out.Messages {
		bodies = append(bodies, aws.ToString(out.Messages[i].Body))
		handles = append(handles, aws.ToString(out.Messages[i].ReceiptHandle))
	}

	return bodies, handles
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

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// TestSDKFIFOBatchReturnsConsecutiveSameGroup verifies that a single
// ReceiveMessage(Max=10) over one FIFO group returns ALL its messages, in send
// order — multiple messages from the same group in one call is correct SQS.
func TestSDKFIFOBatchReturnsConsecutiveSameGroup(t *testing.T) {
	client, _ := newSDKClient(t)
	queueURL := createFIFOQueueSDK(t, client, "batch.fifo")

	want := []string{"m1", "m2", "m3", "m4", "m5"}
	for _, body := range want {
		sendFIFO(t, client, queueURL, body, "g1", "d-"+body)
	}

	got, _ := receiveOrdered(t, client, queueURL, maxReceiveMessagesTest)
	if !equalSlices(got, want) {
		t.Fatalf("batch receive = %v, want all 5 in order %v", got, want)
	}
}

// TestSDKFIFOInFlightGroupBlockedAcrossCalls verifies the across-call rule: once
// a message of a group is in-flight, a later receive returns nothing from that
// group until the in-flight message is deleted (then delivery advances in order).
func TestSDKFIFOInFlightGroupBlockedAcrossCalls(t *testing.T) {
	client, _ := newSDKClient(t)
	queueURL := createFIFOQueueSDK(t, client, "serial.fifo")

	for _, body := range []string{"a", "b", "c"} {
		sendFIFO(t, client, queueURL, body, "g1", "d-"+body)
	}

	first, handles := receiveOrdered(t, client, queueURL, 1)
	if !equalSlices(first, []string{"a"}) {
		t.Fatalf("call#1 = %v, want [a]", first)
	}

	// a is in-flight and not deleted: the group is blocked, so call#2 gets nothing.
	blockedRcv, _ := receiveOrdered(t, client, queueURL, 1)
	if len(blockedRcv) != 0 {
		t.Fatalf("call#2 = %v, want [] while a is in-flight", blockedRcv)
	}

	// Deleting a advances the group to its next message.
	deleteHandle(t, client, queueURL, handles[0])

	next, _ := receiveOrdered(t, client, queueURL, 1)
	if !equalSlices(next, []string{"b"}) {
		t.Fatalf("after deleting a, receive = %v, want [b]", next)
	}
}

// TestSDKFIFOTwoGroupsParallel verifies that an in-flight message in one group
// does not block a different group: the two groups make progress independently.
func TestSDKFIFOTwoGroupsParallel(t *testing.T) {
	client, _ := newSDKClient(t)
	queueURL := createFIFOQueueSDK(t, client, "parallel.fifo")

	sendFIFO(t, client, queueURL, "a", "G1", "d-a")
	sendFIFO(t, client, queueURL, "b", "G1", "d-b")
	sendFIFO(t, client, queueURL, "x", "G2", "d-x")

	// call#1 (Max=1) takes G1's head; G1 is now in-flight/blocked.
	first, _ := receiveOrdered(t, client, queueURL, 1)
	if !equalSlices(first, []string{"a"}) {
		t.Fatalf("call#1 = %v, want [a]", first)
	}

	// call#2 must still deliver G2's head even though G1 is blocked.
	second, _ := receiveOrdered(t, client, queueURL, 1)
	if !equalSlices(second, []string{"x"}) {
		t.Fatalf("call#2 = %v, want [x] (G2 unaffected by blocked G1)", second)
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
