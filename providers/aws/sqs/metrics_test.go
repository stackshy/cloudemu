package sqs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// gaugeRecorder is a monitoring backend that keeps every datum it is sent.
// onPut, when set, runs inside PutMetricData, so a test can call back into
// the SQS mock the way a CloudWatch alarm -> SNS -> SQS action would.
type gaugeRecorder struct {
	mondriver.Monitoring

	mu    sync.Mutex
	data  []mondriver.MetricDatum
	onPut func()
}

func (r *gaugeRecorder) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	r.mu.Lock()
	r.data = append(r.data, data...)
	onPut := r.onPut
	r.mu.Unlock()

	if onPut != nil {
		onPut()
	}

	return nil
}

// last returns the most recent datum of metric for queue.
func (r *gaugeRecorder) last(metric, queue string) (mondriver.MetricDatum, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i := len(r.data) - 1; i >= 0; i-- {
		d := r.data[i]
		if d.MetricName == metric && d.Dimensions["QueueName"] == queue {
			return d, true
		}
	}

	return mondriver.MetricDatum{}, false
}

func (r *gaugeRecorder) reset() {
	r.mu.Lock()
	r.data = nil
	r.mu.Unlock()
}

func newGaugeMock() (*Mock, *gaugeRecorder, func(time.Duration)) {
	m, fc := newTestMock()
	rec := &gaugeRecorder{}
	m.SetMonitoring(rec)

	return m, rec, fc.Advance
}

func requireGauge(t *testing.T, rec *gaugeRecorder, metric, queue string, want float64, unit string) {
	t.Helper()

	d, ok := rec.last(metric, queue)
	if !ok {
		t.Fatalf("%s for %s was never emitted", metric, queue)
	}

	if d.Value != want || d.Unit != unit || d.Namespace != "AWS/SQS" {
		t.Fatalf("%s for %s = %v %s (%s), want %v %s (AWS/SQS)", metric, queue, d.Value, d.Unit, d.Namespace, want, unit)
	}

	if len(d.Dimensions) != 1 {
		t.Fatalf("%s dimensions = %v, want only QueueName", metric, d.Dimensions)
	}
}

func requireNoGauge(t *testing.T, rec *gaugeRecorder, metric, queue string) {
	t.Helper()

	if d, ok := rec.last(metric, queue); ok {
		t.Fatalf("%s for %s emitted %v, want no datum", metric, queue, d.Value)
	}
}

func TestQueueGaugesTrackVisibleInFlightDelayedAndAge(t *testing.T) {
	m, rec, advance := newGaugeMock()
	ctx := context.Background()
	q := createStdQueue(m, "gauges")

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "a"})
	requireNoError(t, err)

	requireGauge(t, rec, metricVisible, "gauges", 1, unitCount)
	requireGauge(t, rec, metricNotVisible, "gauges", 0, unitCount)
	requireGauge(t, rec, metricDelayed, "gauges", 0, unitCount)
	requireGauge(t, rec, metricOldestAge, "gauges", 0, unitSeconds)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{
		QueueURL: q.URL, Body: "delayed", DelaySeconds: 60, DelaySecondsSet: true,
	})
	requireNoError(t, err)

	advance(10 * time.Second)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "c"})
	requireNoError(t, err)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL})
	requireNoError(t, err)
	assertEqual(t, 1, len(msgs))
	assertEqual(t, "a", msgs[0].Body)

	// a is in flight, the delayed message is still delayed, c is visible and a
	// (sent 10s ago) is the oldest message.
	requireGauge(t, rec, metricVisible, "gauges", 1, unitCount)
	requireGauge(t, rec, metricNotVisible, "gauges", 1, unitCount)
	requireGauge(t, rec, metricDelayed, "gauges", 1, unitCount)
	requireGauge(t, rec, metricOldestAge, "gauges", 10, unitSeconds)

	requireNoError(t, m.ChangeVisibility(ctx, q.URL, msgs[0].ReceiptHandle, 0))
	requireGauge(t, rec, metricVisible, "gauges", 2, unitCount)
	requireGauge(t, rec, metricNotVisible, "gauges", 0, unitCount)

	// The delayed message, sent at the start, is now the oldest one left.
	requireNoError(t, m.DeleteMessage(ctx, q.URL, msgs[0].ReceiptHandle))
	requireGauge(t, rec, metricVisible, "gauges", 1, unitCount)
	requireGauge(t, rec, metricOldestAge, "gauges", 10, unitSeconds)

	requireNoError(t, m.PurgeQueue(ctx, q.URL))
	requireGauge(t, rec, metricVisible, "gauges", 0, unitCount)
	requireGauge(t, rec, metricDelayed, "gauges", 0, unitCount)

	// An empty queue reports no age of oldest message.
	rec.reset()
	_, err = m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL})
	requireNoError(t, err)
	requireGauge(t, rec, metricVisible, "gauges", 0, unitCount)
	requireNoGauge(t, rec, metricOldestAge, "gauges")
	requireNoGauge(t, rec, metricGroupsInflight, "gauges")
}

func TestQueueGaugesAgeSkipsStandardPoisonPills(t *testing.T) {
	m, rec, advance := newGaugeMock()
	ctx := context.Background()
	q := createStdQueue(m, "poison")

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "poison"})
	requireNoError(t, err)

	advance(100 * time.Second)

	receiveAgain := func() {
		t.Helper()

		msgs, rerr := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
			QueueURL: q.URL, VisibilityTimeout: 0, VisibilityTimeoutSet: true,
		})
		requireNoError(t, rerr)
		assertEqual(t, 1, len(msgs))
	}

	// Below the threshold the message still counts.
	receiveAgain()
	receiveAgain()
	requireGauge(t, rec, metricOldestAge, "poison", 100, unitSeconds)

	// The third receive makes it a poison pill: with nothing else queued there
	// is no age to report.
	rec.reset()
	receiveAgain()
	requireNoGauge(t, rec, metricOldestAge, "poison")

	// A newer message is now the oldest one counted.
	advance(5 * time.Second)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "fresh"})
	requireNoError(t, err)
	requireGauge(t, rec, metricOldestAge, "poison", 0, unitSeconds)
	requireGauge(t, rec, metricVisible, "poison", 2, unitCount)
}

func TestQueueGaugesFIFOCountsInflightGroups(t *testing.T) {
	m, rec, advance := newGaugeMock()
	ctx := context.Background()

	q, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "orders.fifo", FIFO: true, ContentBasedDeduplication: true})
	requireNoError(t, err)

	for i, g := range []string{"g1", "g1", "g2", "g3"} {
		_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: fmt.Sprintf("%s-%d", g, i), GroupID: g})
		requireNoError(t, err)
	}

	advance(30 * time.Second)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	requireNoError(t, err)
	assertEqual(t, 4, len(msgs))

	requireGauge(t, rec, metricGroupsInflight, "orders.fifo", 3, unitCount)
	requireGauge(t, rec, metricNotVisible, "orders.fifo", 4, unitCount)
	requireGauge(t, rec, metricVisible, "orders.fifo", 0, unitCount)
	requireGauge(t, rec, metricOldestAge, "orders.fifo", 30, unitSeconds)

	for _, msg := range msgs[:2] {
		requireNoError(t, m.DeleteMessage(ctx, q.URL, msg.ReceiptHandle))
	}

	requireGauge(t, rec, metricGroupsInflight, "orders.fifo", 2, unitCount)
	requireGauge(t, rec, metricNotVisible, "orders.fifo", 2, unitCount)
}

func TestQueueGaugesFollowDeadLetterRedriveAndMoveTask(t *testing.T) {
	m, rec, advance := newGaugeMock()
	ctx := context.Background()

	dlq := createStdQueue(m, "dlq")
	src, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "src", RedrivePolicy: redrivePolicy(dlq.ARN, 1)})
	requireNoError(t, err)

	sendN(t, m, src.URL, 1)
	advance(50 * time.Second)

	receive := func() {
		t.Helper()

		_, rerr := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: src.URL, VisibilityTimeout: 1})
		requireNoError(t, rerr)
	}

	receive()
	advance(2 * time.Second)
	receive() // exceeds maxReceiveCount=1: the message moves to the DLQ

	requireGauge(t, rec, metricVisible, "src", 0, unitCount)
	requireGauge(t, rec, metricVisible, "dlq", 1, unitCount)
	// The age resets when a message is moved to a DLQ.
	requireGauge(t, rec, metricOldestAge, "dlq", 0, unitSeconds)

	advance(7 * time.Second)

	_, err = m.StartMessageMoveTask(ctx, dlq.ARN, "", 0)
	requireNoError(t, err)

	requireGauge(t, rec, metricVisible, "dlq", 0, unitCount)
	requireGauge(t, rec, metricVisible, "src", 1, unitCount)
}

func TestQueueGaugesFollowLambdaESMDeadLetterRedrive(t *testing.T) {
	m, rec, _ := newGaugeMock()
	ctx := context.Background()

	dlq := createStdQueue(m, "esm-dlq")
	src, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "esm-src", RedrivePolicy: redrivePolicy(dlq.ARN, 2)})
	requireNoError(t, err)

	m.SetEventSourceInvoker(failingInvoker{})

	sendN(t, m, src.URL, 1)

	requireGauge(t, rec, metricVisible, "esm-dlq", 1, unitCount)
	requireGauge(t, rec, metricVisible, "esm-src", 0, unitCount)
	requireGauge(t, rec, metricNotVisible, "esm-src", 0, unitCount)
}

var errHandlerFailed = errors.New("handler failed")

// failingInvoker is an event-source mapping whose handler always fails.
type failingInvoker struct{}

func (failingInvoker) DeliverEventSourceBatch(context.Context, string, []byte) (bool, error) {
	return true, errHandlerFailed
}

// TestQueueGaugesPublishOutsideQueueLock calls back into the same queue from
// inside PutMetricData, as a CloudWatch alarm whose SNS action delivers to the
// queue would. Publishing while holding the queue lock would deadlock here.
func TestQueueGaugesPublishOutsideQueueLock(t *testing.T) {
	m, rec, _ := newGaugeMock()
	ctx := context.Background()
	q := createStdQueue(m, "reentrant")

	rec.onPut = func() {
		_, _ = m.GetQueueAttributes(ctx, q.URL)
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		sendN(t, m, q.URL, 1)

		msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL})
		if err != nil || len(msgs) != 1 {
			t.Errorf("receive = %v, %v", msgs, err)
			return
		}

		_ = m.ChangeVisibility(ctx, q.URL, msgs[0].ReceiptHandle, 0)
		_, _ = m.ReceiveMessagesWithOptions(ctx, q.URL, driver.ReceiveOptions{})
		_ = m.DeleteMessage(ctx, q.URL, msgs[0].ReceiptHandle)
		_ = m.PurgeQueue(ctx, q.URL)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publishing a queue gauge deadlocked on the queue lock")
	}
}

func TestQueueGaugesNotEmittedWithoutMonitoring(t *testing.T) {
	m, _ := newTestMock()
	q := createStdQueue(m, "quiet")

	// No backend wired: every message operation still succeeds.
	sendN(t, m, q.URL, 1)

	_, err := m.ReceiveMessages(context.Background(), driver.ReceiveMessageInput{QueueURL: q.URL})
	requireNoError(t, err)
}

func TestSenderIDFromCallerElseAccount(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()
	q := createStdQueue(m, "sender")

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "user", SenderID: "AIDAEXAMPLE123ABC"})
	requireNoError(t, err)

	res, err := m.SendMessageBatch(ctx, q.URL, []driver.BatchSendEntry{
		{ID: "1", Body: "role", SenderID: "AROAEXAMPLE123ABC:session"},
	})
	requireNoError(t, err)
	assertEqual(t, 1, len(res.Successful))

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "library"})
	requireNoError(t, err)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10})
	requireNoError(t, err)
	assertEqual(t, 3, len(msgs))

	got := map[string]string{}
	for _, msg := range msgs {
		got[msg.Body] = msg.SystemAttributes["SenderId"]
	}

	assertEqual(t, "AIDAEXAMPLE123ABC", got["user"])
	assertEqual(t, "AROAEXAMPLE123ABC:session", got["role"])
	assertEqual(t, m.opts.AccountID, got["library"])
}
