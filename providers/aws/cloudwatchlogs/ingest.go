package cloudwatchlogs

import (
	"context"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// PutLogEvents ingest window and batch limits, per the API reference.
const (
	// putLogEventsMaxAge: events older than 14 days are rejected.
	putLogEventsMaxAge = 14 * 24 * time.Hour
	// putLogEventsMaxFuture: events more than 2 hours in the future are rejected.
	putLogEventsMaxFuture = 2 * time.Hour
	// putLogEventsMaxBatchSpan: the accepted events of one batch cannot span
	// more than 24 hours, otherwise the whole call fails.
	putLogEventsMaxBatchSpan = 24 * time.Hour
)

// sequenceTokenPrefix gives nextSequenceToken the 56-digit shape AWS returns;
// the per-stream upload count fills the low 16 digits.
const sequenceTokenPrefix = "4953670125153982633102568327403296938495"

// PutLogEventsWithResult writes a batch to a stream the way CloudWatch Logs
// does. A batch that is out of order, too large, or whose accepted events span
// more than 24 hours fails as a whole. Otherwise events older than 14 days or
// older than the group's retention, and events more than 2 hours in the
// future, are dropped and reported in Rejected while the rest are stored.
func (m *Mock) PutLogEventsWithResult(
	ctx context.Context, groupName, streamName string, events []driver.LogEvent,
) (*driver.PutLogEventsResult, error) {
	g, ok := m.groups.Get(groupName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "log group %q not found", groupName)
	}

	s, ok := g.streams.Get(streamName)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "log stream %q not found in group %q", streamName, groupName)
	}

	if len(events) == 0 {
		return nil, errors.New(errors.InvalidArgument, "logEvents cannot be empty")
	}

	if err := validatePutLogEventsBatch(events); err != nil {
		return nil, err
	}

	m.purgeExpired(groupName, g)

	accepted, rejected := m.ingestWindow(events, g.info.RetentionDays)

	if len(accepted) > 0 &&
		accepted[len(accepted)-1].Timestamp.Sub(accepted[0].Timestamp) > putLogEventsMaxBatchSpan {
		return nil, errors.New(errors.InvalidArgument,
			"the log events in a single PutLogEvents request cannot span more than 24 hours")
	}

	token := m.storeEvents(ctx, g, s, streamName, accepted)

	return &driver.PutLogEventsResult{NextSequenceToken: token, Rejected: rejected}, nil
}

// validatePutLogEventsBatch applies the checks that fail a whole batch before
// any event is stored: the size limits and chronological order.
func validatePutLogEventsBatch(events []driver.LogEvent) error {
	if err := validatePutLogEventsSize(events); err != nil {
		return err
	}

	for i := 1; i < len(events); i++ {
		if events[i].Timestamp.Before(events[i-1].Timestamp) {
			return errors.New(errors.InvalidArgument,
				"log events in a single PutLogEvents request must be in chronological order")
		}
	}

	return nil
}

// ingestWindow splits a chronological batch into the events CloudWatch Logs
// keeps and a report of the ones it drops. Rejected events are always a prefix
// (too old or past retention) and a suffix (too far in the future), so the
// kept events are the slice between them. rejected is nil when nothing was
// dropped.
func (m *Mock) ingestWindow(events []driver.LogEvent, retentionDays int) ([]driver.LogEvent, *driver.RejectedLogEventsInfo) {
	now := m.opts.Clock.Now()

	tooOld := countBefore(events, now.Add(-putLogEventsMaxAge))
	expired := countBefore(events, m.retentionCutoff(retentionDays))
	tooNew := firstAfter(events, now.Add(putLogEventsMaxFuture))

	start := max(tooOld, expired)
	end := max(tooNew, start)

	if start == 0 && end == len(events) {
		return events, nil
	}

	info := &driver.RejectedLogEventsInfo{}

	if tooOld > 0 {
		info.TooOldLogEventEndIndex = &tooOld
	}

	if expired > 0 {
		info.ExpiredLogEventEndIndex = &expired
	}

	if tooNew < len(events) {
		info.TooNewLogEventStartIndex = &tooNew
	}

	return events[start:end], info
}

// countBefore returns how many leading events of a chronological batch are
// older than cutoff. A zero cutoff matches nothing.
func countBefore(events []driver.LogEvent, cutoff time.Time) int {
	if cutoff.IsZero() {
		return 0
	}

	n := 0
	for n < len(events) && events[n].Timestamp.Before(cutoff) {
		n++
	}

	return n
}

// firstAfter returns the index of the first event of the trailing run newer
// than cutoff, or len(events) when there is none.
func firstAfter(events []driver.LogEvent, cutoff time.Time) int {
	i := len(events)
	for i > 0 && events[i-1].Timestamp.After(cutoff) {
		i--
	}

	return i
}

// storeEvents appends accepted events to the stream, updates the group's
// counters, and runs metric filters and subscriptions. It returns the
// stream's next sequence token. Every upload, even one whose events were all
// rejected, advances the token.
func (m *Mock) storeEvents(
	ctx context.Context, g *logGroup, s *logStream, streamName string, events []driver.LogEvent,
) string {
	ingestedAt := m.opts.Clock.Now().UTC()

	copied := make([]driver.LogEvent, len(events))

	var totalBytes int64

	for i := range events {
		copied[i] = events[i]
		copied[i].IngestionTime = ingestedAt
		totalBytes += int64(len(events[i].Message))
	}

	// The stream lock only guards the stream's own events/metadata. It is
	// released before cross-service delivery (metric filters, subscription
	// filters) so a delivered handler that writes back into CloudWatch Logs,
	// re-entering PutLogEvents, cannot deadlock on this same lock.
	s.mu.Lock()

	s.uploads++
	token := fmt.Sprintf("%s%016d", sequenceTokenPrefix, s.uploads)

	if len(copied) > 0 {
		s.events = append(s.events, copied...)

		// CloudWatch Logs returns events in timestamp order regardless of the
		// order they were ingested, so keep the backing slice sorted on
		// insert. The sort is stable, so equal timestamps keep ingestion order.
		sortLogEventsAscending(s.events)

		// events is chronological, so events[0] is this batch's earliest
		// timestamp and the last element its latest.
		if s.info.FirstEvent == "" {
			s.info.FirstEvent = events[0].Timestamp.UTC().Format(time.RFC3339)
		}

		s.info.LastEvent = events[len(events)-1].Timestamp.UTC().Format(time.RFC3339)
	}

	s.mu.Unlock()

	if len(copied) == 0 {
		return token
	}

	groupName := g.info.Name

	m.groups.Update(groupName, func(lg *logGroup) *logGroup {
		lg.info.StoredBytes += totalBytes
		return lg
	})

	dims := map[string]string{"LogGroupName": groupName}
	m.emitMetric("IncomingLogEvents", float64(len(events)), "Count", dims)
	m.emitMetric("IncomingBytes", float64(totalBytes), "Bytes", dims)

	m.evaluateMetricFilters(g, events)
	m.deliverToSubscriptions(ctx, g, streamName, copied)

	return token
}
