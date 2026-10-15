package sqs

import (
	"context"
	"math"
	"time"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// AWS/SQS queue-state gauges, published on the single QueueName dimension
// (https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-available-cloudwatch-metrics.html).
//
// Real SQS pushes these once a minute for every active queue: one that holds
// messages, or that an action accessed in the last six hours
// (https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/monitoring-using-cloudwatch.html).
// Tick does the same on the shared serve scheduler, so a queue whose consumers
// stopped keeps reporting a growing backlog and age. The counters
// (NumberOfMessagesSent and friends) stay on the request paths.
const (
	metricsNamespace = "AWS/SQS"
	unitCount        = "Count"
	unitSeconds      = "Seconds"

	metricVisible        = "ApproximateNumberOfMessagesVisible"
	metricNotVisible     = "ApproximateNumberOfMessagesNotVisible"
	metricDelayed        = "ApproximateNumberOfMessagesDelayed"
	metricOldestAge      = "ApproximateAgeOfOldestMessage"
	metricGroupsInflight = "ApproximateNumberOfGroupsWithInflightMessages"

	// poisonPillReceiveCount is the receive count from which a standard queue
	// moves an undeleted message to the back of the queue and leaves it out of
	// ApproximateAgeOfOldestMessage until it is processed.
	poisonPillReceiveCount = 3

	// gaugeInterval is how often an active queue publishes its gauges.
	gaugeInterval = time.Minute
	// activeWindow is how long an action keeps an empty queue active.
	activeWindow = 6 * time.Hour
)

// queueGauges is one sample of a queue's state gauges.
type queueGauges struct {
	visible, notVisible, delayed, groupsInflight int
	// oldestAge is the age in whole seconds of the oldest message counted by
	// ApproximateAgeOfOldestMessage; hasOldest is false when there is none.
	oldestAge float64
	hasOldest bool
}

// sampleGauges reads the queue's state gauges as of now. Caller holds qd.mu.
func sampleGauges(qd *queueData, now time.Time) queueGauges {
	var (
		g      queueGauges
		oldest time.Time
		groups map[string]struct{}
	)

	for _, msg := range qd.messages {
		if g.countState(msg, now) && qd.info.FIFO && msg.GroupID != "" {
			if groups == nil {
				groups = make(map[string]struct{})
			}

			groups[msg.GroupID] = struct{}{}
		}

		if countsForAge(qd.info.FIFO, msg) && (!g.hasOldest || msg.SentAt.Before(oldest)) {
			oldest = msg.SentAt
			g.hasOldest = true
		}
	}

	g.groupsInflight = len(groups)

	if g.hasOldest {
		g.oldestAge = math.Max(0, math.Floor(now.Sub(oldest).Seconds()))
	}

	return g
}

// countState adds msg to the visible, delayed or in-flight count and reports
// whether it is in flight. A message that has never been received and whose
// visibility lies in the future is delayed; one that has been received and is
// still hidden is in flight.
func (g *queueGauges) countState(msg *sqsMessage, now time.Time) (inFlight bool) {
	switch {
	case !msg.VisibleAt.After(now):
		g.visible++
	case msg.ReceiveCount == 0:
		g.delayed++
	default:
		g.notVisible++
		return true
	}

	return false
}

// countsForAge reports whether msg counts toward ApproximateAgeOfOldestMessage.
// A standard queue leaves out poison-pill messages (received three or more
// times without being deleted); FIFO queues keep order, so every message
// counts.
func countsForAge(fifo bool, msg *sqsMessage) bool {
	return fifo || msg.ReceiveCount < poisonPillReceiveCount
}

// Tick publishes one sample of the state gauges for every active queue whose
// last sample is at least gaugeInterval old at now, and reports whether it
// published anything. It has the config.Tickable signature of the shared serve
// scheduler; library mode never calls it, so tests drive it with a FakeClock.
// Each queue is sampled under its own lock and everything is published after
// all locks are released, because a CloudWatch alarm on a gauge may notify an
// SNS topic that delivers straight back into a queue.
func (m *Mock) Tick(now time.Time) bool {
	if m.monitoring == nil {
		return false
	}

	var data []mondriver.MetricDatum

	for _, qd := range m.queues.SortedValues() {
		data = append(data, dueGauges(qd, now)...)
	}

	if len(data) == 0 {
		return false
	}

	_ = m.monitoring.PutMetricData(context.Background(), data)

	return true
}

// dueGauges samples qd when it is active and its gauges are due at now, and
// records the sample time. It returns nil otherwise. Callers must not hold
// qd.mu.
func dueGauges(qd *queueData, now time.Time) []mondriver.MetricDatum {
	qd.mu.Lock()
	defer qd.mu.Unlock()

	if !qd.lastSampleAt.IsZero() && now.Sub(qd.lastSampleAt) < gaugeInterval {
		return nil
	}

	if len(qd.messages) == 0 && now.Sub(qd.lastAccessedAt()) >= activeWindow {
		return nil
	}

	qd.lastSampleAt = now

	return gaugeData(qd.info.Name, qd.info.FIFO, sampleGauges(qd, now), now)
}

// gaugeData builds the datums of one gauge sample.
func gaugeData(queue string, fifo bool, g queueGauges, now time.Time) []mondriver.MetricDatum {
	dims := map[string]string{"QueueName": queue}
	datum := func(metric string, value float64, unit string) mondriver.MetricDatum {
		return mondriver.MetricDatum{
			Namespace: metricsNamespace, MetricName: metric, Value: value, Unit: unit,
			Dimensions: dims, Timestamp: now,
		}
	}

	data := []mondriver.MetricDatum{
		datum(metricVisible, float64(g.visible), unitCount),
		datum(metricNotVisible, float64(g.notVisible), unitCount),
		datum(metricDelayed, float64(g.delayed), unitCount),
	}

	// The age is reported only while the queue holds a message it counts.
	if g.hasOldest {
		data = append(data, datum(metricOldestAge, g.oldestAge, unitSeconds))
	}

	if fifo {
		data = append(data, datum(metricGroupsInflight, float64(g.groupsInflight), unitCount))
	}

	return data
}

// touch records an action on the queue at now.
func (qd *queueData) touch(now time.Time) {
	qd.lastAccessed.Store(now.UnixNano())
}

// lastAccessedAt is the time of the last action on the queue, or the zero
// time when none was recorded.
func (qd *queueData) lastAccessedAt() time.Time {
	n := qd.lastAccessed.Load()
	if n == 0 {
		return time.Time{}
	}

	return time.Unix(0, n).UTC()
}

// accessQueue looks up the queue at url for an API action and records the
// access, which keeps the queue active for its CloudWatch gauges.
func (m *Mock) accessQueue(url string) (*queueData, bool) {
	qd, ok := m.queues.Get(url)
	if ok {
		qd.touch(m.opts.Clock.Now())
	}

	return qd, ok
}
