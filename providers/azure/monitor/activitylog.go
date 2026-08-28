package monitor

import (
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	// maxActivityLogEvents bounds the in-memory Activity Log. Real Azure keeps 90
	// days of events; the emulator keeps the most recent maxActivityLogEvents and
	// drops the oldest, so a long-lived process stays bounded.
	maxActivityLogEvents = 2000
	// defaultActivityLogRecords caps a query with no explicit MaxRecords.
	defaultActivityLogRecords = 1000
)

// Compile-time check that Mock implements the optional recorder capability.
var _ driver.ActivityLogRecorder = (*Mock)(nil)

// RecordActivityLogEvent appends a management event to the bounded Activity Log
// the Activity Log API reads. The wire server calls it as ARM activity flows
// through it, so the emulator's activity log reflects real API calls. It is
// part of the Azure-only ActivityLogRecorder capability (see the driver pkg).
func (m *Mock) RecordActivityLogEvent(e *driver.ActivityLogEvent) {
	if e == nil {
		return
	}

	// The recorder owns the timestamp and ids so the clock (FakeClock in tests)
	// stays in the provider.
	e.EventTimestamp = m.opts.Clock.Now()
	if e.EventID == "" {
		e.EventID = idgen.UUID()
	}

	if e.CorrelationID == "" {
		e.CorrelationID = idgen.UUID()
	}

	if e.Status == "" {
		e.Status = "Succeeded"
	}

	if e.Level == "" {
		e.Level = "Informational"
	}

	m.activityMu.Lock()
	defer m.activityMu.Unlock()

	m.activityLog = append(m.activityLog, *e)
	// Amortized trim: re-slice only once the log reaches the 2x high-water mark,
	// then drop back to cap, keeping the common append O(1).
	if len(m.activityLog) > 2*maxActivityLogEvents {
		m.activityLog = append([]driver.ActivityLogEvent(nil), m.activityLog[len(m.activityLog)-maxActivityLogEvents:]...)
	}
}

// ListActivityLogEvents returns recorded events matching the query's time and
// resource filters, newest first. A zero query returns every event.
//
//nolint:gocritic // q is a small filter struct; by-value matches the driver capability signature.
func (m *Mock) ListActivityLogEvents(q driver.ActivityLogQuery) []driver.ActivityLogEvent {
	m.activityMu.Lock()
	all := append([]driver.ActivityLogEvent(nil), m.activityLog...)
	m.activityMu.Unlock()

	// Reverse into newest-inserted-first order, then stable-sort by timestamp
	// descending. A stable sort keeps insertion order for equal timestamps, so
	// events recorded in the same instant (e.g. under a FakeClock) still return
	// deterministically newest-first.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}

	sort.SliceStable(all, func(i, j int) bool {
		return all[i].EventTimestamp.After(all[j].EventTimestamp)
	})

	limit := q.MaxRecords
	if limit <= 0 {
		limit = defaultActivityLogRecords
	}

	matched := make([]driver.ActivityLogEvent, 0, len(all))

	for i := range all {
		if activityMatches(&all[i], &q) {
			matched = append(matched, all[i])
			if len(matched) >= limit {
				break
			}
		}
	}

	return matched
}

// activityMatches reports whether an event satisfies the query's time window
// and resource filters (Azure matches on the filters supplied).
func activityMatches(e *driver.ActivityLogEvent, q *driver.ActivityLogQuery) bool {
	if !q.StartTime.IsZero() && e.EventTimestamp.Before(q.StartTime) {
		return false
	}

	if !q.EndTime.IsZero() && e.EventTimestamp.After(q.EndTime) {
		return false
	}

	if q.ResourceGroup != "" && !strings.EqualFold(e.ResourceGroup, q.ResourceGroup) {
		return false
	}

	if q.ResourceID != "" && !strings.EqualFold(e.ResourceID, q.ResourceID) {
		return false
	}

	return true
}
