package sqs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
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

// samples returns every datum of metric for queue, oldest first.
func (r *gaugeRecorder) samples(metric, queue string) []mondriver.MetricDatum {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []mondriver.MetricDatum

	for _, d := range r.data {
		if d.MetricName == metric && d.Dimensions["QueueName"] == queue {
			out = append(out, d)
		}
	}

	return out
}

// last returns the most recent datum of metric for queue.
func (r *gaugeRecorder) last(metric, queue string) (mondriver.MetricDatum, bool) {
	s := r.samples(metric, queue)
	if len(s) == 0 {
		return mondriver.MetricDatum{}, false
	}

	return s[len(s)-1], true
}

func (r *gaugeRecorder) reset() {
	r.mu.Lock()
	r.data = nil
	r.mu.Unlock()
}

func newGaugeMock() (*Mock, *gaugeRecorder, *config.FakeClock) {
	m, fc := newTestMock()
	rec := &gaugeRecorder{}
	m.SetMonitoring(rec)

	return m, rec, fc
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

func TestRequestsPublishCountersButNoGauges(t *testing.T) {
	m, rec, _ := newGaugeMock()
	ctx := context.Background()
	q := createStdQueue(m, "quiet-paths")

	sendN(t, m, q.URL, 3)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL})
	requireNoError(t, err)
	requireNoError(t, m.ChangeVisibility(ctx, q.URL, msgs[0].ReceiptHandle, 0))
	requireNoError(t, m.DeleteMessage(ctx, q.URL, msgs[0].ReceiptHandle))
	requireNoError(t, m.PurgeQueue(ctx, q.URL))

	for _, g := range []string{metricVisible, metricNotVisible, metricDelayed, metricOldestAge} {
		requireNoGauge(t, rec, g, "quiet-paths")
	}

	if got := len(rec.samples("NumberOfMessagesSent", "quiet-paths")); got != 3 {
		t.Fatalf("NumberOfMessagesSent datums = %d, want 3", got)
	}
}

func TestTickPublishesOneSamplePerMinute(t *testing.T) {
	m, rec, fc := newGaugeMock()
	q := createStdQueue(m, "per-minute")

	// Six sends in the same minute: one sample of 6, not one per send.
	sendN(t, m, q.URL, 6)

	if !m.Tick(fc.Now()) {
		t.Fatal("first Tick published nothing")
	}

	requireGauge(t, rec, metricVisible, "per-minute", 6, unitCount)

	for _, step := range []time.Duration{time.Second, 30 * time.Second, 28 * time.Second} {
		fc.Advance(step)

		if m.Tick(fc.Now()) {
			t.Fatalf("Tick %v after the sample published again inside the minute", fc.Now())
		}
	}

	fc.Advance(time.Second) // a full minute since the first sample

	if !m.Tick(fc.Now()) {
		t.Fatal("Tick a minute after the last sample published nothing")
	}

	if got := len(rec.samples(metricVisible, "per-minute")); got != 2 {
		t.Fatalf("Visible samples = %d, want 2 (one per minute)", got)
	}
}

func TestTickTracksVisibleInFlightDelayedAndAge(t *testing.T) {
	m, rec, fc := newGaugeMock()
	ctx := context.Background()
	q := createStdQueue(m, "gauges")

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "a"})
	requireNoError(t, err)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{
		QueueURL: q.URL, Body: "delayed", DelaySeconds: 600, DelaySecondsSet: true,
	})
	requireNoError(t, err)

	fc.Advance(10 * time.Second)

	_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "c"})
	requireNoError(t, err)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, VisibilityTimeout: 300})
	requireNoError(t, err)
	assertEqual(t, "a", msgs[0].Body)

	m.Tick(fc.Now())

	// a is in flight, one message is delayed, c is visible, and the oldest
	// message was sent 10s ago.
	requireGauge(t, rec, metricVisible, "gauges", 1, unitCount)
	requireGauge(t, rec, metricNotVisible, "gauges", 1, unitCount)
	requireGauge(t, rec, metricDelayed, "gauges", 1, unitCount)
	requireGauge(t, rec, metricOldestAge, "gauges", 10, unitSeconds)
	requireNoGauge(t, rec, metricGroupsInflight, "gauges")

	// GetQueueAttributes agrees with the gauges: in flight excludes delayed.
	attrs, err := m.GetQueueAttributes(ctx, q.URL)
	requireNoError(t, err)
	assertEqual(t, 1, attrs.ApproximateMessageCount)
	assertEqual(t, 1, attrs.ApproximateNotVisibleCount)
	assertEqual(t, 1, attrs.ApproximateDelayedCount)
}

// TestTickStuckQueueKeepsReportingAndAges is the stopped-consumer case: after
// a single delayed send nothing touches the queue, and the gauges still show
// the message becoming visible and getting older every minute.
func TestTickStuckQueueKeepsReportingAndAges(t *testing.T) {
	m, rec, fc := newGaugeMock()
	ctx := context.Background()
	q := createStdQueue(m, "stuck")

	_, err := m.SendMessage(ctx, driver.SendMessageInput{
		QueueURL: q.URL, Body: "late", DelaySeconds: 5, DelaySecondsSet: true,
	})
	requireNoError(t, err)

	m.Tick(fc.Now())
	requireGauge(t, rec, metricVisible, "stuck", 0, unitCount)
	requireGauge(t, rec, metricDelayed, "stuck", 1, unitCount)
	requireGauge(t, rec, metricOldestAge, "stuck", 0, unitSeconds)

	fc.Advance(time.Minute)
	m.Tick(fc.Now())
	requireGauge(t, rec, metricVisible, "stuck", 1, unitCount)
	requireGauge(t, rec, metricDelayed, "stuck", 0, unitCount)
	requireGauge(t, rec, metricOldestAge, "stuck", 60, unitSeconds)

	// A queue holding messages stays active long past the six-hour window.
	for i := 0; i < 8*60; i++ {
		fc.Advance(time.Minute)
		m.Tick(fc.Now())
	}

	requireGauge(t, rec, metricOldestAge, "stuck", (8*60+1)*60, unitSeconds)

	if got := len(rec.samples(metricVisible, "stuck")); got != 8*60+2 {
		t.Fatalf("Visible samples = %d, want %d (one per minute)", got, 8*60+2)
	}
}

func TestTickStopsForEmptyQueueIdleSixHours(t *testing.T) {
	m, rec, fc := newGaugeMock()
	ctx := context.Background()
	q := createStdQueue(m, "idle")

	if !m.Tick(fc.Now()) {
		t.Fatal("a just-created queue is active and should publish")
	}

	fc.Advance(activeWindow - time.Minute)

	if !m.Tick(fc.Now()) {
		t.Fatal("an empty queue accessed under six hours ago should still publish")
	}

	fc.Advance(time.Minute)
	rec.reset()

	if m.Tick(fc.Now()) {
		t.Fatal("an empty queue idle for six hours should stop publishing")
	}

	// Any action wakes it up again.
	_, err := m.GetQueueAttributes(ctx, q.URL)
	requireNoError(t, err)

	fc.Advance(time.Minute)

	if !m.Tick(fc.Now()) {
		t.Fatal("an accessed queue should publish again")
	}

	requireGauge(t, rec, metricVisible, "idle", 0, unitCount)
	requireNoGauge(t, rec, metricOldestAge, "idle")
}

func TestTickAgeSkipsStandardPoisonPills(t *testing.T) {
	m, rec, fc := newGaugeMock()
	ctx := context.Background()
	q := createStdQueue(m, "poison")

	_, err := m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "poison"})
	requireNoError(t, err)

	fc.Advance(100 * time.Second)

	receive := func() {
		t.Helper()

		msgs, rerr := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{
			QueueURL: q.URL, VisibilityTimeout: 0, VisibilityTimeoutSet: true,
		})
		requireNoError(t, rerr)
		assertEqual(t, 1, len(msgs))
	}

	receive()
	receive()
	m.Tick(fc.Now())
	requireGauge(t, rec, metricOldestAge, "poison", 100, unitSeconds)

	// The third receive makes it a poison pill: nothing else is queued, so
	// there is no age to report.
	receive()
	rec.reset()
	fc.Advance(time.Minute)
	m.Tick(fc.Now())
	requireGauge(t, rec, metricVisible, "poison", 1, unitCount)
	requireNoGauge(t, rec, metricOldestAge, "poison")
}

func TestTickFIFOCountsInflightGroups(t *testing.T) {
	m, rec, fc := newGaugeMock()
	ctx := context.Background()

	q, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "orders.fifo", FIFO: true, ContentBasedDeduplication: true})
	requireNoError(t, err)

	for i, g := range []string{"g1", "g1", "g2", "g3"} {
		_, err = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: fmt.Sprintf("%s-%d", g, i), GroupID: g})
		requireNoError(t, err)
	}

	fc.Advance(30 * time.Second)

	msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL, MaxMessages: 10, VisibilityTimeout: 300})
	requireNoError(t, err)
	assertEqual(t, 4, len(msgs))

	m.Tick(fc.Now())
	requireGauge(t, rec, metricGroupsInflight, "orders.fifo", 3, unitCount)
	requireGauge(t, rec, metricNotVisible, "orders.fifo", 4, unitCount)
	requireGauge(t, rec, metricOldestAge, "orders.fifo", 30, unitSeconds)

	for _, msg := range msgs[:2] {
		requireNoError(t, m.DeleteMessage(ctx, q.URL, msg.ReceiptHandle))
	}

	fc.Advance(time.Minute)
	m.Tick(fc.Now())
	requireGauge(t, rec, metricGroupsInflight, "orders.fifo", 2, unitCount)
	requireGauge(t, rec, metricNotVisible, "orders.fifo", 2, unitCount)
}

func TestTickFollowsDeadLetterRedriveAndMoveTask(t *testing.T) {
	m, rec, fc := newGaugeMock()
	ctx := context.Background()

	dlq := createStdQueue(m, "dlq")
	src, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "src", RedrivePolicy: redrivePolicy(dlq.ARN, 1)})
	requireNoError(t, err)

	sendN(t, m, src.URL, 1)
	fc.Advance(50 * time.Second)

	receive := func() {
		t.Helper()

		_, rerr := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: src.URL, VisibilityTimeout: 1})
		requireNoError(t, rerr)
	}

	receive()
	fc.Advance(2 * time.Second)
	receive() // exceeds maxReceiveCount=1: the message moves to the DLQ

	m.Tick(fc.Now())
	requireGauge(t, rec, metricVisible, "src", 0, unitCount)
	requireGauge(t, rec, metricVisible, "dlq", 1, unitCount)
	// The age resets when a message is moved to a DLQ.
	requireGauge(t, rec, metricOldestAge, "dlq", 0, unitSeconds)

	fc.Advance(time.Minute)
	requireGaugeAfterMove(t, m, rec, fc, dlq.ARN)
}

func requireGaugeAfterMove(t *testing.T, m *Mock, rec *gaugeRecorder, fc *config.FakeClock, dlqARN string) {
	t.Helper()

	_, err := m.StartMessageMoveTask(context.Background(), dlqARN, "", 0)
	requireNoError(t, err)

	m.Tick(fc.Now())
	requireGauge(t, rec, metricVisible, "dlq", 0, unitCount)
	requireGauge(t, rec, metricVisible, "src", 1, unitCount)
}

// TestTickPublishesOutsideQueueLocks calls back into the queues from inside
// PutMetricData, as a CloudWatch alarm whose SNS action delivers to a queue
// would. Publishing while holding a queue lock would deadlock here.
func TestTickPublishesOutsideQueueLocks(t *testing.T) {
	m, rec, fc := newGaugeMock()
	ctx := context.Background()
	q := createStdQueue(m, "reentrant")
	sendN(t, m, q.URL, 1)

	var called atomic.Bool

	// Only the first publish calls back, or the SendMessage counter it emits
	// would call back again forever.
	rec.onPut = func() {
		if called.CompareAndSwap(false, true) {
			_, _ = m.GetQueueAttributes(ctx, q.URL)
			_, _ = m.SendMessage(ctx, driver.SendMessageInput{QueueURL: q.URL, Body: "from-alarm"})
		}
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		m.Tick(fc.Now())

		msgs, err := m.ReceiveMessages(ctx, driver.ReceiveMessageInput{QueueURL: q.URL})
		if err == nil && len(msgs) == 1 {
			_ = m.DeleteMessage(ctx, q.URL, msgs[0].ReceiptHandle)
		}

		_, _ = m.ReceiveMessagesWithOptions(ctx, q.URL, driver.ReceiveOptions{})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publishing a metric deadlocked on a queue lock")
	}
}

func TestTickWithoutMonitoringIsNoOp(t *testing.T) {
	m, fc := newTestMock()
	q := createStdQueue(m, "no-backend")
	sendN(t, m, q.URL, 1)

	if m.Tick(fc.Now()) {
		t.Fatal("Tick without a monitoring backend reported a change")
	}
}

func TestSnapshotKeepsGaugeSchedule(t *testing.T) {
	ctx := context.Background()
	src, rec, fc := newGaugeMock()
	createStdQueue(src, "sched")

	src.Tick(fc.Now())
	requireGauge(t, rec, metricVisible, "sched", 0, unitCount)

	data, err := src.Snapshot(ctx, false)
	requireNoError(t, err)

	dst, dstRec, _ := newGaugeMock()
	requireNoError(t, dst.Restore(ctx, data))

	// The restored queue sampled at fc.Now(), so it is not due again yet.
	if dst.Tick(fc.Now().Add(30 * time.Second)) {
		t.Fatal("restored queue lost its last sample time")
	}

	// It was accessed at fc.Now(), so it is still active a minute later.
	if !dst.Tick(fc.Now().Add(time.Minute)) {
		t.Fatal("restored queue lost its last access time")
	}

	requireGauge(t, dstRec, metricVisible, "sched", 0, unitCount)

	// Six hours after that access the empty queue is idle.
	if dst.Tick(fc.Now().Add(activeWindow + time.Minute)) {
		t.Fatal("restored empty queue should be idle after six hours")
	}
}

var errHandlerFailed = errors.New("handler failed")

// failingInvoker is an event-source mapping whose handler always fails.
type failingInvoker struct{}

func (failingInvoker) DeliverEventSourceBatch(context.Context, string, []byte) (bool, error) {
	return true, errHandlerFailed
}

func TestTickFollowsLambdaESMDeadLetterRedrive(t *testing.T) {
	m, rec, fc := newGaugeMock()
	ctx := context.Background()

	dlq := createStdQueue(m, "esm-dlq")
	src, err := m.CreateQueue(ctx, driver.QueueConfig{Name: "esm-src", RedrivePolicy: redrivePolicy(dlq.ARN, 2)})
	requireNoError(t, err)

	m.SetEventSourceInvoker(failingInvoker{})

	sendN(t, m, src.URL, 1)
	m.Tick(fc.Now())

	requireGauge(t, rec, metricVisible, "esm-dlq", 1, unitCount)
	requireGauge(t, rec, metricVisible, "esm-src", 0, unitCount)
	requireGauge(t, rec, metricNotVisible, "esm-src", 0, unitCount)
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
