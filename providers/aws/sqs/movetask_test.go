package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

func sendN(t *testing.T, m *Mock, url string, n int) {
	t.Helper()

	for i := 0; i < n; i++ {
		if _, err := m.SendMessage(context.Background(), driver.SendMessageInput{
			QueueURL: url, Body: fmt.Sprintf("body-%d", i),
		}); err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	}
}

func visibleCount(t *testing.T, m *Mock, url string) int {
	t.Helper()

	attrs, err := m.GetQueueAttributes(context.Background(), url)
	requireNoError(t, err)

	return attrs.ApproximateMessageCount
}

func redrivePolicy(dlqARN string, maxReceive int) string {
	b, _ := json.Marshal(map[string]any{
		"deadLetterTargetArn": dlqARN,
		"maxReceiveCount":     maxReceive,
	})

	return string(b)
}

func TestStartMessageMoveTaskExplicitDestination(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	dlq := createStdQueue(m, "dlq")
	dest := createStdQueue(m, "dest")
	sendN(t, m, dlq.URL, 3)

	handle, err := m.StartMessageMoveTask(ctx, dlq.ARN, dest.ARN, 0)
	requireNoError(t, err)
	assertNotEmpty(t, handle)

	assertEqual(t, 0, visibleCount(t, m, dlq.URL))
	assertEqual(t, 3, visibleCount(t, m, dest.URL))

	tasks, err := m.ListMessageMoveTasks(ctx, dlq.ARN, 0)
	requireNoError(t, err)
	assertEqual(t, 1, len(tasks))
	assertEqual(t, moveTaskCompleted, tasks[0].Status)
	assertEqual(t, int64(3), tasks[0].ApproxMessagesMoved)
	assertEqual(t, int64(3), tasks[0].ApproxMessagesToMove)
	assertEqual(t, dest.ARN, tasks[0].DestinationARN)
}

func TestStartMessageMoveTaskRedriveToConfiguredSource(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	dlq := createStdQueue(m, "dlq")
	src, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "src", RedrivePolicy: redrivePolicy(dlq.ARN, 3)})
	requireNoError(t, err)

	sendN(t, m, dlq.URL, 2)

	_, err = m.StartMessageMoveTask(ctx, dlq.ARN, "", 0)
	requireNoError(t, err)

	assertEqual(t, 0, visibleCount(t, m, dlq.URL))
	assertEqual(t, 2, visibleCount(t, m, src.URL))
}

func TestStartMessageMoveTaskRedriveToOriginAfterDLQ(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()

	dlq := createStdQueue(m, "dlq")
	src, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "src", RedrivePolicy: redrivePolicy(dlq.ARN, 1)})
	requireNoError(t, err)

	sendN(t, m, src.URL, 1)

	// Receive twice so the message exceeds maxReceiveCount=1 and lands in the DLQ,
	// carrying its origin queue URL. Advance past the visibility timeout so the
	// message is receivable again on the second call.
	if _, err = m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: src.URL, VisibilityTimeout: 1}); err != nil {
		t.Fatalf("first receive: %v", err)
	}

	fc.Advance(2 * time.Second)

	if _, err = m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: src.URL, VisibilityTimeout: 1}); err != nil {
		t.Fatalf("second receive: %v", err)
	}

	assertEqual(t, 1, visibleCount(t, m, dlq.URL))

	_, err = m.StartMessageMoveTask(ctx, dlq.ARN, "", 0)
	requireNoError(t, err)

	assertEqual(t, 0, visibleCount(t, m, dlq.URL))
	assertEqual(t, 1, visibleCount(t, m, src.URL))
}

func TestStartMessageMoveTaskUnknownArns(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	dlq := createStdQueue(m, "dlq")

	if _, err := m.StartMessageMoveTask(ctx, "arn:aws:sqs:us-east-1:123456789012:nope", "", 0); err == nil {
		t.Fatal("StartMessageMoveTask with unknown source: want error, got nil")
	}

	if _, err := m.StartMessageMoveTask(ctx, dlq.ARN, "arn:aws:sqs:us-east-1:123456789012:nope", 0); err == nil {
		t.Fatal("StartMessageMoveTask with unknown destination: want error, got nil")
	}
}

func TestListMessageMoveTasksNewestFirstBounded(t *testing.T) {
	m, fc := newTestMock()
	ctx := context.Background()

	dlq := createStdQueue(m, "dlq")
	dest := createStdQueue(m, "dest")

	var handles []string

	for i := 0; i < 3; i++ {
		sendN(t, m, dlq.URL, 1)

		h, err := m.StartMessageMoveTask(ctx, dlq.ARN, dest.ARN, 0)
		requireNoError(t, err)

		handles = append(handles, h)
		fc.Advance(time.Second) // so StartedTimestamps are strictly increasing
	}

	all, err := m.ListMessageMoveTasks(ctx, dlq.ARN, 10)
	requireNoError(t, err)
	assertEqual(t, 3, len(all))
	// Newest first: the last-created handle comes first.
	assertEqual(t, handles[2], all[0].TaskHandle)
	assertEqual(t, handles[0], all[2].TaskHandle)

	bounded, err := m.ListMessageMoveTasks(ctx, dlq.ARN, 1)
	requireNoError(t, err)
	assertEqual(t, 1, len(bounded))
	assertEqual(t, handles[2], bounded[0].TaskHandle)

	// Default (MaxResults=0) returns a single most-recent task.
	def, err := m.ListMessageMoveTasks(ctx, dlq.ARN, 0)
	requireNoError(t, err)
	assertEqual(t, 1, len(def))
}

func TestListMessageMoveTasksUnknownSource(t *testing.T) {
	m, _ := newTestMock()

	if _, err := m.ListMessageMoveTasks(context.Background(), "arn:aws:sqs:us-east-1:123456789012:nope", 0); err == nil {
		t.Fatal("ListMessageMoveTasks with unknown source: want error, got nil")
	}
}

func TestCancelMessageMoveTaskUnknownOrCompleted(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	dlq := createStdQueue(m, "dlq")
	dest := createStdQueue(m, "dest")
	sendN(t, m, dlq.URL, 1)

	handle, err := m.StartMessageMoveTask(ctx, dlq.ARN, dest.ARN, 0)
	requireNoError(t, err)

	// Task completed synchronously, so cancel finds no RUNNING task.
	if _, err := m.CancelMessageMoveTask(ctx, handle); err == nil {
		t.Fatal("CancelMessageMoveTask on completed task: want error, got nil")
	}

	if _, err := m.CancelMessageMoveTask(ctx, "unknown-handle"); err == nil {
		t.Fatal("CancelMessageMoveTask on unknown handle: want error, got nil")
	}
}
