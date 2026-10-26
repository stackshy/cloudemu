package cloudwatchlogs

import (
	"context"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
)

// validRetentionInDays is the closed set of retentionInDays values CloudWatch
// Logs accepts; any other value is rejected with InvalidParameterException.
//
//nolint:gochecknoglobals // fixed lookup table for a closed enum.
var validRetentionInDays = map[int]bool{
	1: true, 3: true, 5: true, 7: true, 14: true, 30: true, 60: true, 90: true,
	120: true, 150: true, 180: true, 365: true, 400: true, 545: true, 731: true,
	1096: true, 1827: true, 2192: true, 2557: true, 2922: true, 3288: true, 3653: true,
}

// PutRetentionPolicy sets how many days a log group keeps its events. Events
// already past the new retention are purged straight away.
func (m *Mock) PutRetentionPolicy(_ context.Context, name string, retentionDays int) error {
	if !validRetentionInDays[retentionDays] {
		return errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value '%d' at 'retentionInDays' failed to satisfy constraint: "+
				"Member must satisfy enum value set: [1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, "+
				"731, 1096, 1827, 2192, 2557, 2922, 3288, 3653]", retentionDays)
	}

	if !m.groups.Update(name, func(g *logGroup) *logGroup {
		g.info.RetentionDays = retentionDays
		return g
	}) {
		return errors.Newf(errors.NotFound, "log group %q not found", name)
	}

	if g, ok := m.groups.Get(name); ok {
		m.purgeExpired(name, g)
	}

	return nil
}

// DeleteRetentionPolicy removes a log group's retention policy so its events
// never expire again.
func (m *Mock) DeleteRetentionPolicy(_ context.Context, name string) error {
	if !m.groups.Update(name, func(g *logGroup) *logGroup {
		g.info.RetentionDays = 0
		return g
	}) {
		return errors.Newf(errors.NotFound, "log group %q not found", name)
	}

	return nil
}

// retentionCutoff is the instant before which a group's events have expired,
// or the zero time when the group never expires.
func (m *Mock) retentionCutoff(retentionDays int) time.Time {
	if retentionDays <= 0 {
		return time.Time{}
	}

	return m.opts.Clock.Now().AddDate(0, 0, -retentionDays)
}

// purgeExpired deletes every event older than the group's retention and
// takes their bytes off storedBytes, so every read API sees the same data.
//
// Simplification: real CloudWatch Logs decouples billing from physical
// deletion. An event stops counting toward storedBytes once it passes
// retention, but the background deletion that removes it from read APIs can
// lag up to 72 hours, with no documented lower bound. This emulator collapses
// both into one synchronous purge at the retention boundary, which is
// deterministic under FakeClock. It does NOT model the deletion lag.
//
// Locking: only one stream lock is held at a time, and never together with
// the groups store lock. storedBytes is adjusted through groups.Update after
// every stream lock is released, the same order PutLogEvents uses. Purging
// makes no calls into other services.
func (m *Mock) purgeExpired(name string, g *logGroup) {
	cutoff := m.retentionCutoff(g.info.RetentionDays)
	if cutoff.IsZero() {
		return
	}

	var purged int64

	for _, s := range g.streams.All() {
		purged += s.purgeBefore(cutoff)
	}

	if purged == 0 {
		return
	}

	m.groups.Update(name, func(lg *logGroup) *logGroup {
		lg.info.StoredBytes = max(lg.info.StoredBytes-purged, 0)
		return lg
	})
}

// purgeBefore drops the stream's events older than cutoff and returns the
// bytes they held. Events are kept sorted ascending, so the expired ones are a
// prefix. The stream's first/last event timestamps are left alone: AWS keeps
// reporting them after the events themselves expire.
func (s *logStream) purgeBefore(cutoff time.Time) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := sort.Search(len(s.events), func(i int) bool {
		return !s.events[i].Timestamp.Before(cutoff)
	})
	if n == 0 {
		return 0
	}

	var bytes int64
	for i := range n {
		bytes += int64(len(s.events[i].Message))
	}

	s.events = append(s.events[:0:0], s.events[n:]...)

	return bytes
}
