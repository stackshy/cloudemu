package cloudwatchlogs

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs/filterpattern"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// Live Tail limits, per the StartLiveTail API reference and the CloudWatch
// Logs quotas.
const (
	// liveTailSessionTimeout: a session ends with SessionTimeoutException
	// after it has been open for three hours.
	liveTailSessionTimeout = 3 * time.Hour
	// liveTailUpdateInterval: a sessionUpdate is sent every second, empty when
	// nothing new matched.
	liveTailUpdateInterval = time.Second
	// liveTailMaxEventsPerUpdate: one update carries at most 500 events; more
	// are sampled down to 500.
	liveTailMaxEventsPerUpdate = 500
	// liveTailBufferedUpdates and liveTailBufferedEvents: for a slow client,
	// up to 10 updates or 5000 events are buffered, then the oldest drop.
	liveTailBufferedUpdates = 10
	liveTailBufferedEvents  = 5000
	maxLiveTailLogGroups    = 10
	maxLiveTailStreams      = 100
	maxLiveTailStreamName   = 512
	maxLiveTailPatternLen   = 1024
	// maxLiveTailSessions is the account quota of concurrent sessions.
	maxLiveTailSessions = 15
)

// liveTail is one running session. The fan-out in PutLogEvents only appends
// to pending under mu; the session goroutine owns out.
type liveTail struct {
	id       string
	groups   map[string]string // log group name -> identifier as requested
	streams  map[string]bool   // nil means every stream
	prefixes []string
	pattern  *filterpattern.Pattern
	started  time.Time

	out       chan driver.LiveTailUpdate
	done      chan struct{}
	closeOnce sync.Once
	timedOut  atomic.Bool

	mu      sync.Mutex
	pending []driver.LiveTailEvent
	matched int
}

func (t *liveTail) SessionID() string                     { return t.id }
func (t *liveTail) Updates() <-chan driver.LiveTailUpdate { return t.out }
func (t *liveTail) TimedOut() bool                        { return t.timedOut.Load() }

// Close ends the session. It is safe to call more than once.
func (t *liveTail) Close() {
	t.closeOnce.Do(func() { close(t.done) })
}

// StartLiveTail opens a Live Tail session. Events ingested after it starts,
// in the selected groups and streams and matching the filter pattern, are
// delivered in one update per second. Masked events stay masked.
func (m *Mock) StartLiveTail(ctx context.Context, in *driver.StartLiveTailInput) (driver.LiveTailSession, error) {
	t, err := m.newLiveTail(in)
	if err != nil {
		return nil, err
	}

	m.liveTailMu.Lock()

	if len(m.liveTails) >= maxLiveTailSessions {
		m.liveTailMu.Unlock()

		return nil, errors.Newf(errors.ResourceExhausted,
			"The maximum number of concurrent Live Tail sessions (%d) has been reached", maxLiveTailSessions)
	}

	m.liveTails[t.id] = t
	m.liveTailMu.Unlock()

	go m.runLiveTail(ctx, t)

	return t, nil
}

// newLiveTail validates a request and builds its session.
func (m *Mock) newLiveTail(in *driver.StartLiveTailInput) (*liveTail, error) {
	if err := validateLiveTail(in); err != nil {
		return nil, err
	}

	pattern, err := filterpattern.Parse(in.LogEventFilterPattern)
	if err != nil {
		return nil, err
	}

	groups := make(map[string]string, len(in.LogGroupIdentifiers))

	for _, id := range in.LogGroupIdentifiers {
		name := logGroupFromIdentifier(id)
		if !m.groups.Has(name) {
			return nil, errors.Newf(errors.NotFound, "The specified log group does not exist: %s", id)
		}

		groups[name] = id
	}

	t := &liveTail{
		id:       idgen.UUID(),
		groups:   groups,
		prefixes: in.LogStreamNamePrefixes,
		pattern:  pattern,
		started:  m.opts.Clock.Now(),
		out:      make(chan driver.LiveTailUpdate, liveTailBufferedUpdates),
		done:     make(chan struct{}),
	}

	if len(in.LogStreamNames) > 0 {
		t.streams = make(map[string]bool, len(in.LogStreamNames))
		for _, s := range in.LogStreamNames {
			t.streams[s] = true
		}
	}

	return t, nil
}

func validateLiveTail(in *driver.StartLiveTailInput) error {
	if err := validateLiveTailShape(in); err != nil {
		return err
	}

	for _, id := range in.LogGroupIdentifiers {
		if err := validateLiveTailGroup(id); err != nil {
			return err
		}
	}

	if err := validateLiveTailStreams("logStreamNames", in.LogStreamNames); err != nil {
		return err
	}

	return validateLiveTailStreams("logStreamNamePrefixes", in.LogStreamNamePrefixes)
}

// validateLiveTailShape checks the item counts, the stream selector rules and
// the pattern length.
func validateLiveTailShape(in *driver.StartLiveTailInput) error {
	switch {
	case len(in.LogGroupIdentifiers) == 0 || len(in.LogGroupIdentifiers) > maxLiveTailLogGroups:
		return errors.Newf(errors.InvalidArgument,
			"logGroupIdentifiers must contain between 1 and %d log group ARNs", maxLiveTailLogGroups)
	case len(in.LogStreamNames) > 0 && len(in.LogStreamNamePrefixes) > 0:
		return errors.New(errors.InvalidArgument,
			"logStreamNames and logStreamNamePrefixes cannot both be specified")
	case (len(in.LogStreamNames) > 0 || len(in.LogStreamNamePrefixes) > 0) && len(in.LogGroupIdentifiers) != 1:
		return errors.New(errors.InvalidArgument,
			"logStreamNames and logStreamNamePrefixes can only be specified with exactly one log group")
	case len(in.LogEventFilterPattern) > maxLiveTailPatternLen:
		return errors.Newf(errors.InvalidArgument,
			"logEventFilterPattern must be at most %d characters", maxLiveTailPatternLen)
	}

	return nil
}

// validateLiveTailGroup checks one log group identifier: an ARN that does not
// end with an asterisk.
func validateLiveTailGroup(id string) error {
	if len(id) > maxLogGroupIdentifierLength || !logGroupIdentifierRE.MatchString(id) {
		return errors.New(errors.InvalidArgument,
			"1 validation error detected: Value at 'logGroupIdentifiers' failed to satisfy constraint: "+
				`Member must satisfy regular expression pattern: [\w#+=/:,.@-]*`)
	}

	if !strings.HasPrefix(id, "arn:") || !strings.Contains(id, logGroupARNMarker) {
		return errors.Newf(errors.InvalidArgument, "Specify each log group by its ARN: %s", id)
	}

	if strings.HasSuffix(id, "*") {
		return errors.Newf(errors.InvalidArgument, "The log group ARN cannot end with an asterisk: %s", id)
	}

	return nil
}

func validateLiveTailStreams(field string, names []string) error {
	if len(names) > maxLiveTailStreams {
		return errors.Newf(errors.InvalidArgument, "%s can contain at most %d items", field, maxLiveTailStreams)
	}

	for _, n := range names {
		if n == "" || len(n) > maxLiveTailStreamName || strings.ContainsAny(n, ":*") {
			return errors.Newf(errors.InvalidArgument,
				"1 validation error detected: Value at '%s' failed to satisfy constraint: "+
					"Member must have length between 1 and %d and satisfy regular expression pattern: [^:*]*",
				field, maxLiveTailStreamName)
		}
	}

	return nil
}

// runLiveTail sends one update per interval until the session's context
// ends, it is closed, or it reaches the session timeout. Session age is read
// from opts.Clock; the cadence is wall-clock time, since a fake clock's After
// fires at once.
func (m *Mock) runLiveTail(ctx context.Context, t *liveTail) {
	ticker := time.NewTicker(liveTailUpdateInterval)

	defer func() {
		ticker.Stop()
		m.liveTailMu.Lock()
		delete(m.liveTails, t.id)
		m.liveTailMu.Unlock()
		close(t.out)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.done:
			return
		case <-ticker.C:
			if m.opts.Clock.Since(t.started) >= liveTailSessionTimeout {
				t.timedOut.Store(true)

				return
			}

			t.send(t.drain())
		}
	}
}

// send queues an update. When the client has fallen 10 updates behind, the
// oldest queued update is dropped. Only the session goroutine sends, so the
// second attempt always has room.
func (t *liveTail) send(u driver.LiveTailUpdate) {
	select {
	case t.out <- u:
		return
	default:
	}

	select {
	case <-t.out:
	default:
	}

	select {
	case t.out <- u:
	default:
	}
}

// drain takes the events matched since the last update, sampled down to the
// per-update limit.
func (t *liveTail) drain() driver.LiveTailUpdate {
	t.mu.Lock()
	events, matched := t.pending, t.matched
	t.pending, t.matched = nil, 0
	t.mu.Unlock()

	u := driver.LiveTailUpdate{Results: events, Sampled: matched > liveTailMaxEventsPerUpdate}
	if len(events) <= liveTailMaxEventsPerUpdate {
		if u.Results == nil {
			u.Results = []driver.LiveTailEvent{}
		}

		return u
	}

	sampled := make([]driver.LiveTailEvent, liveTailMaxEventsPerUpdate)
	for i := range sampled {
		sampled[i] = events[i*len(events)/liveTailMaxEventsPerUpdate]
	}

	u.Results = sampled

	return u
}

// wants reports whether the session tails the given stream of a group, and
// the group identifier it reports events under.
func (t *liveTail) wants(group, stream string) (string, bool) {
	id, ok := t.groups[group]
	if !ok {
		return "", false
	}

	if t.streams != nil {
		return id, t.streams[stream]
	}

	if len(t.prefixes) == 0 {
		return id, true
	}

	for _, p := range t.prefixes {
		if strings.HasPrefix(stream, p) {
			return id, true
		}
	}

	return "", false
}

// offer adds the matching events of one stored batch to the session's
// pending buffer, keeping at most the newest liveTailBufferedEvents.
func (t *liveTail) offer(group, stream string, events []driver.LogEvent) {
	id, ok := t.wants(group, stream)
	if !ok {
		return
	}

	var hits []driver.LiveTailEvent

	for i := range events {
		if !t.pattern.Match(events[i].Message) {
			continue
		}

		hits = append(hits, driver.LiveTailEvent{
			LogGroupIdentifier: id,
			LogStreamName:      stream,
			Message:            events[i].Message,
			Timestamp:          events[i].Timestamp,
			IngestionTime:      events[i].IngestionTime,
		})
	}

	if len(hits) == 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.matched += len(hits)
	t.pending = append(t.pending, hits...)

	if over := len(t.pending) - liveTailBufferedEvents; over > 0 {
		t.pending = append(t.pending[:0:0], t.pending[over:]...)
	}
}

// publishLiveTail fans a stored batch out to the running sessions. The
// registry lock is held only to copy the session list; offer takes each
// session's own lock briefly and never blocks on a client.
func (m *Mock) publishLiveTail(group, stream string, events []driver.LogEvent) {
	m.liveTailMu.Lock()

	sessions := make([]*liveTail, 0, len(m.liveTails))
	for _, t := range m.liveTails {
		sessions = append(sessions, t)
	}

	m.liveTailMu.Unlock()

	for _, t := range sessions {
		t.offer(group, stream, events)
	}
}
