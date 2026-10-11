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
// Real SQS pushes these once a minute for every active queue, where any action
// on the queue (or any message in it) keeps it active. The emulator has no
// background publisher, so it samples the gauges whenever an action changes a
// queue's message state: a send, a receive, a delete, a visibility change, a
// purge, a dead-letter redrive, and a message move task.
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

// emitQueueGauges publishes the current state gauges of qd. It takes qd.mu
// itself and publishes after releasing it, because a CloudWatch alarm on the
// gauge may notify an SNS topic that delivers straight back into this queue.
// Callers must not hold qd.mu. A no-op without a monitoring backend.
func (m *Mock) emitQueueGauges(qd *queueData) {
	if m.monitoring == nil || qd == nil {
		return
	}

	now := m.opts.Clock.Now()

	qd.mu.Lock()
	g := sampleGauges(qd, now)
	name, fifo := qd.info.Name, qd.info.FIFO
	qd.mu.Unlock()

	dims := map[string]string{"QueueName": name}
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

	_ = m.monitoring.PutMetricData(context.Background(), data)
}

// emitQueueGaugesByURL is emitQueueGauges for the queue at url, if it exists.
func (m *Mock) emitQueueGaugesByURL(url string) {
	if m.monitoring == nil || url == "" {
		return
	}

	if qd, ok := m.queues.Get(url); ok {
		m.emitQueueGauges(qd)
	}
}
