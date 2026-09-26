package serverkit

import (
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

// tickSource returns the Tickables to call on one tick. It runs on every tick,
// so a reset that swaps providers or a newly built AWS region is picked up.
type tickSource func() []config.Tickable

// tickEntry is one registered source and the interval it runs on.
type tickEntry struct {
	interval time.Duration
	source   tickSource
}

// scheduler runs the serve background ticks. Each entry gets its own
// goroutine and ticker. It is only started by Serve, so library mode never
// ticks.
type scheduler struct {
	clock    config.Clock
	onChange func() // called when any Tickable reports a change

	entries []tickEntry

	stopCh chan struct{}
	wg     sync.WaitGroup
}

func newScheduler(clock config.Clock, onChange func()) *scheduler {
	return &scheduler{clock: clock, onChange: onChange}
}

// add registers a source. An interval of zero or less turns the entry off.
// Call it before start.
func (s *scheduler) add(interval time.Duration, source tickSource) {
	if interval <= 0 {
		return
	}

	s.entries = append(s.entries, tickEntry{interval: interval, source: source})
}

// start launches one goroutine per entry. It is a no-op when already running.
func (s *scheduler) start() {
	if s.stopCh != nil {
		return
	}

	s.stopCh = make(chan struct{})

	for _, e := range s.entries {
		s.wg.Add(1)

		go s.loop(e)
	}
}

func (s *scheduler) loop(e tickEntry) {
	defer s.wg.Done()

	t := time.NewTicker(e.interval)
	defer t.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			if s.runOnce(e.source) && s.onChange != nil {
				s.onChange()
			}
		}
	}
}

// runOnce ticks every Tickable the source returns and reports whether any of
// them changed state.
func (s *scheduler) runOnce(source tickSource) bool {
	now := s.clock.Now()
	changed := false

	for _, t := range source() {
		if t != nil && t.Tick(now) {
			changed = true
		}
	}

	return changed
}

// stop halts every entry and waits for its goroutine to exit. It is safe to
// call more than once and when start never ran.
func (s *scheduler) stop() {
	if s.stopCh == nil {
		return
	}

	close(s.stopCh)
	s.wg.Wait()
	s.stopCh = nil
}

// tickFunc adapts a plain function to config.Tickable.
type tickFunc func(now time.Time) bool

func (f tickFunc) Tick(now time.Time) bool { return f(now) }
