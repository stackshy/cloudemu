package fis

import (
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/fis/driver"
)

// Experiment lifecycle.
//
// Real FIS moves an experiment through initiating -> running -> completed (or
// stopping -> stopped when StopExperiment is called). The emulator derives the
// observed state from the clock at read time instead of storing each step:
//
//   - initiating lasts for the settle window after CreationTime. It is zero
//     unless AsyncSettle is enabled, so by default StartExperiment already
//     reports running.
//   - running lasts for the experiment's run length: the longest action chain
//     (each action's "duration" / "startInstancesAfterDuration" parameter,
//     ordered by startAfter), and never less than minExperimentRun, so an
//     experiment can always be stopped right after it starts.
//   - completed is reported once the run length has elapsed.
//
// Only the stopped terminal state is written to the store (by StopExperiment);
// every other state is recomputed on each read, so reads are stable for a given
// clock and survive snapshot/restore unchanged.

// Additional experiment and action state values.
const (
	statusInitiating = "initiating"
	statusPending    = "pending"
	statusCompleted  = "completed"
	statusCancelled  = "cancelled" //nolint:misspell // AWS FIS API status literal (British spelling).
	statusSkipped    = "skipped"
	statusFailed     = "failed"

	reasonInitiating = "Experiment is initiating."
	reasonCompleted  = "Experiment completed."
	reasonActionDone = "Action completed."
	reasonPending    = "Action pending."
	reasonCancelled  = "Action cancelled." //nolint:misspell // matches the FIS status literal.
	reasonSkipped    = "Action skipped."

	actionsModeSkipAll = "skip-all"
)

// minExperimentRun is the shortest time an experiment spends running. Real
// experiments take at least this long to resolve targets and run even
// instantaneous actions, and it keeps the start-then-stop pattern working.
const minExperimentRun = 30 * time.Second

// Action parameters that bound how long an action runs, as ISO 8601 durations.
var durationParams = [...]string{"duration", "startInstancesAfterDuration"} //nolint:gochecknoglobals // immutable lookup table

// actionWindow is an action's run interval as offsets from the run start.
type actionWindow struct {
	start, end time.Duration
}

// isTerminal reports whether an experiment status can no longer change.
func isTerminal(status string) bool {
	return status == statusStopped || status == statusCompleted || status == statusFailed
}

// initiateWindow is how long an experiment reports initiating; zero unless
// asynchronous settling is enabled.
func (m *Mock) initiateWindow() time.Duration {
	return m.opts.SettleDuration(settle.DefaultExperimentInitiateSettle)
}

// observe rewrites e (an alias-free copy) to the state it has at now.
func (m *Mock) observe(e *driver.Experiment, now time.Time) {
	if isTerminal(e.State.Status) {
		return
	}

	runStart := e.CreationTime.Add(m.initiateWindow())
	if now.Before(runStart) {
		e.State = driver.ExperimentState{Status: statusInitiating, Reason: reasonInitiating}

		for name := range e.Actions {
			a := e.Actions[name]
			a.State = driver.ExperimentActionState{Status: statusPending, Reason: reasonPending}
			a.StartTime, a.EndTime = time.Time{}, time.Time{}
			e.Actions[name] = a
		}

		return
	}

	windows := actionWindows(e.Actions)
	skipAll := e.ExperimentOptions.ActionsMode == actionsModeSkipAll
	runEnd := runStart.Add(runLength(windows, skipAll))

	e.StartTime = runStart
	e.State = driver.ExperimentState{Status: statusRunning, Reason: reasonStarted}

	if !now.Before(runEnd) {
		e.State = driver.ExperimentState{Status: statusCompleted, Reason: reasonCompleted}
		e.EndTime = runEnd
	}

	for name := range e.Actions {
		a := e.Actions[name]
		observeAction(&a, windows[name], runStart, now, skipAll)
		e.Actions[name] = a
	}
}

// observeAction sets one action's state at now from its window.
func observeAction(a *driver.ExperimentAction, w actionWindow, runStart, now time.Time, skipAll bool) {
	start, end := runStart.Add(w.start), runStart.Add(w.end)
	a.StartTime, a.EndTime = time.Time{}, time.Time{}

	switch {
	case skipAll:
		a.State = driver.ExperimentActionState{Status: statusSkipped, Reason: reasonSkipped}
	case now.Before(start):
		a.State = driver.ExperimentActionState{Status: statusPending, Reason: reasonPending}
	case now.Before(end):
		a.State = driver.ExperimentActionState{Status: statusRunning, Reason: reasonStarted}
		a.StartTime = start
	default:
		a.State = driver.ExperimentActionState{Status: statusCompleted, Reason: reasonActionDone}
		a.StartTime, a.EndTime = start, end
	}
}

// stop moves an observed, non-terminal experiment to stopped at now: running
// actions stop, actions that never started are canceled, and finished or
// skipped actions keep their outcome.
func stop(e *driver.Experiment, now time.Time) {
	e.State = driver.ExperimentState{Status: statusStopped, Reason: reasonStopped}
	e.EndTime = now

	for name := range e.Actions {
		a := e.Actions[name]

		switch a.State.Status {
		case statusRunning:
			a.State = driver.ExperimentActionState{Status: statusStopped, Reason: reasonStopped}
			a.EndTime = now
		case statusPending:
			a.State = driver.ExperimentActionState{Status: statusCancelled, Reason: reasonCancelled}
		}

		e.Actions[name] = a
	}
}

// runLength is how long the experiment runs: its longest action chain, but no
// less than minExperimentRun.
func runLength(windows map[string]actionWindow, skipAll bool) time.Duration {
	longest := minExperimentRun
	if skipAll {
		return longest
	}

	for _, w := range windows {
		longest = max(longest, w.end)
	}

	return longest
}

// actionWindows schedules every action: an action starts once all of its
// startAfter predecessors have ended and runs for its duration parameter.
func actionWindows(actions map[string]driver.ExperimentAction) map[string]actionWindow {
	out := make(map[string]actionWindow, len(actions))
	visiting := make(map[string]bool, len(actions))

	var schedule func(name string) time.Duration

	schedule = func(name string) time.Duration {
		if w, ok := out[name]; ok {
			return w.end
		}

		a, ok := actions[name]
		if !ok || visiting[name] {
			return 0 // unknown predecessor or a cycle: do not wait on it
		}

		visiting[name] = true

		var start time.Duration
		for _, pred := range a.StartAfter {
			start = max(start, schedule(pred))
		}

		visiting[name] = false
		out[name] = actionWindow{start: start, end: start + actionDuration(a.Parameters)}

		return out[name].end
	}

	for name := range actions {
		schedule(name)
	}

	return out
}

// actionDuration is the longest duration parameter on an action (zero when it
// has none, i.e. an instantaneous action).
func actionDuration(params map[string]string) time.Duration {
	var d time.Duration

	for _, key := range durationParams {
		d = max(d, parseISODuration(params[key]))
	}

	return d
}

// parseISODuration parses the time-only ISO 8601 durations FIS action
// parameters use (PTnH, PTnM, PTnS and combinations such as PT1H30M). It
// returns zero for an empty or malformed value.
func parseISODuration(s string) time.Duration {
	const prefix = "PT"

	if len(s) <= len(prefix) || s[:len(prefix)] != prefix {
		return 0
	}

	units := map[byte]time.Duration{'H': time.Hour, 'M': time.Minute, 'S': time.Second}

	var total time.Duration

	num := ""

	for i := len(prefix); i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			num += string(c)

			continue
		}

		unit, ok := units[c]
		if !ok || num == "" {
			return 0
		}

		n, err := strconv.Atoi(num)
		if err != nil {
			return 0
		}

		total += time.Duration(n) * unit
		num = ""
	}

	if num != "" {
		return 0
	}

	return total
}
