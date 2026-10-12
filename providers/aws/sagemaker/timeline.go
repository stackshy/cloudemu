package sagemaker

import (
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// syncStageStep spaces a job's stage start times when --async-settle is off. A
// job is terminal the moment it is created, but its history still has distinct,
// strictly increasing timestamps built from the Clock.
const syncStageStep = time.Millisecond

// secondRound rounds job durations to whole seconds.
const secondRound = time.Second

// stageDef is one step of a job's planned lifecycle: its primary status, its
// secondary status (where the API exposes one) and the status message.
type stageDef struct {
	primary   string
	secondary string
	message   string
}

// jobStopStatuses are the primary statuses of a job being stopped / stopped.
type jobStopStatuses struct{ stopping, stopped string }

var (
	mixedCaseStop = jobStopStatuses{driver.JobStopping, driver.JobStopped}                 //nolint:gochecknoglobals // constant pair
	upperCaseStop = jobStopStatuses{driver.CompilationStopping, driver.CompilationStopped} //nolint:gochecknoglobals // constant pair
)

func trainingStages() []stageDef {
	return []stageDef{
		{driver.JobInProgress, driver.SecondaryStarting, "Preparing the instances for training"},
		{driver.JobInProgress, driver.SecondaryDownloading, "Downloading input data"},
		{driver.JobInProgress, driver.SecondaryTraining, "Training image download completed. Training in progress."},
		{driver.JobInProgress, driver.SecondaryUploading, "Uploading generated training model"},
		{driver.JobCompleted, driver.SecondaryCompleted, "Training job completed"},
	}
}

func simpleStages() []stageDef {
	return []stageDef{{driver.JobInProgress, "", ""}, {driver.JobCompleted, "", ""}}
}

func autoMLStages() []stageDef {
	return []stageDef{
		{driver.JobInProgress, driver.SecondaryStarting, ""},
		{driver.JobInProgress, driver.SecondaryAnalyzingData, ""},
		{driver.JobInProgress, driver.SecondaryFeatureEngineer, ""},
		{driver.JobInProgress, driver.SecondaryModelTuning, ""},
		{driver.JobCompleted, driver.SecondaryCompleted, ""},
	}
}

func labelingStages() []stageDef {
	return []stageDef{
		{driver.LabelingInitializing, "", ""}, {driver.JobInProgress, "", ""}, {driver.JobCompleted, "", ""},
	}
}

func compilationStages() []stageDef {
	return []stageDef{
		{driver.CompilationStarting, "", ""}, {driver.CompilationInProgress, "", ""}, {driver.CompilationCompleted, "", ""},
	}
}

// Job lifecycle outcomes of a projection.
const (
	jobRunning   = "running"
	jobCompleted = "completed"
	jobStopping  = "stopping"
	jobStopped   = "stopped"
)

// projection is a job's state observed at one instant.
type projection struct {
	state    string
	stage    int         // index of the stage the job is in (or was in when stopped)
	starts   []time.Time // start time of every stage up to and including stage
	stopAt   time.Time   // when Stop was accepted (zero if never)
	ended    time.Time   // when the job reached a terminal state (zero while running)
	modified time.Time   // when the job last changed state
}

// terminal reports whether the job has finished (completed or stopped).
func (p projection) terminal() bool { return p.state == jobCompleted || p.state == jobStopped }

// stageStep returns the time a job spends in each stage.
func (m *Mock) stageStep() time.Duration {
	if d := m.opts.SettleDuration(settle.DefaultJobStageSettle); d > 0 {
		return d
	}

	return syncStageStep
}

// project computes where a job created at created, with n stages and an
// optional stop request, is at the Clock's now. Without --async-settle the job
// is observed at the end of its lifecycle.
func (m *Mock) project(created, stopRequested string, n int) projection {
	step := m.stageStep()
	start := parseTime(created)
	async := m.opts.SettleDuration(settle.DefaultJobStageSettle) > 0

	now := start.Add(time.Duration(n) * step) // sync: past the last stage
	if async {
		now = m.opts.Clock.Now()
	}

	if stopRequested != "" {
		return m.projectStopped(start, parseTime(stopRequested), now, step, n)
	}

	idx := int(now.Sub(start) / step)
	if idx < 0 {
		idx = 0
	}

	state := jobRunning

	if idx >= n-1 {
		idx, state = n-1, jobCompleted
	}

	p := projection{state: state, stage: idx, starts: stageStarts(start, step, idx), modified: start.Add(time.Duration(idx) * step)}
	if state == jobCompleted {
		p.ended = p.modified
	}

	return p
}

// projectStopped handles a job with an accepted stop request: its progress
// freezes at the stop time, it is Stopping for a short window, then Stopped.
func (m *Mock) projectStopped(start, stopAt, now time.Time, step time.Duration, n int) projection {
	idx := int(stopAt.Sub(start) / step)
	if idx > n-2 {
		idx = n - 2
	}

	if idx < 0 {
		idx = 0
	}

	stopEnd := stopAt.Add(m.stopWindow())
	p := projection{stage: idx, starts: stageStarts(start, step, idx), stopAt: stopAt}

	if now.Before(stopEnd) {
		p.state, p.modified = jobStopping, stopAt

		return p
	}

	p.state, p.ended, p.modified = jobStopped, stopEnd, stopEnd

	return p
}

// stopWindow is how long a job stays Stopping.
func (m *Mock) stopWindow() time.Duration {
	if d := m.opts.SettleDuration(settle.DefaultJobStopSettle); d > 0 {
		return d
	}

	return syncStageStep
}

func stageStarts(start time.Time, step time.Duration, last int) []time.Time {
	out := make([]time.Time, last+1)
	for i := range out {
		out[i] = start.Add(time.Duration(i) * step)
	}

	return out
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// primary returns the primary status of a projection for the given stages.
func (p projection) primary(stages []stageDef, stop jobStopStatuses) string {
	switch p.state {
	case jobStopping:
		return stop.stopping
	case jobStopped:
		return stop.stopped
	default:
		return stages[p.stage].primary
	}
}

// secondary returns the secondary status of a projection.
func (p projection) secondary(stages []stageDef) string {
	switch p.state {
	case jobStopping:
		return driver.SecondaryStopping
	case jobStopped:
		return driver.SecondaryStopped
	default:
		return stages[p.stage].secondary
	}
}

// transitions builds a training job's SecondaryStatusTransitions: every stage
// reached, closed when the next begins, then Stopping/Stopped after a stop.
func (p projection) transitions(stages []stageDef) []driver.SecondaryStatusTransition {
	out := make([]driver.SecondaryStatusTransition, 0, len(p.starts)+2)

	for i, st := range p.starts {
		t := driver.SecondaryStatusTransition{Status: stages[i].secondary, StartTime: fmtTime(st), StatusMessage: stages[i].message}

		switch {
		case i < p.stage:
			t.EndTime = fmtTime(p.starts[i+1])
		case i == p.stage && p.state == jobCompleted:
			t.EndTime = fmtTime(st)
		case i == p.stage && !p.stopAt.IsZero():
			t.EndTime = fmtTime(p.stopAt)
		}

		out = append(out, t)
	}

	if !p.stopAt.IsZero() {
		stopping := driver.SecondaryTransition(driver.SecondaryStopping, fmtTime(p.stopAt), "Stopping the training job")
		if p.state == jobStopped {
			stopping.EndTime = fmtTime(p.ended)
			out = append(out, stopping,
				driver.SecondaryTransition(driver.SecondaryStopped, fmtTime(p.ended), "Training job stopped"))
			out[len(out)-1].EndTime = fmtTime(p.ended)
		} else {
			out = append(out, stopping)
		}
	}

	return out
}

// stopAccess lets the generic stop work on any job type.
type stopAccess[T any] struct {
	created       func(*T) string
	stopRequested func(*T) *string
	stages        []stageDef
}

// stopJob requests a stop of an in-progress job. Stopping a job that is already
// Completed, Stopping or Stopped is a successful no-op and never changes its
// state; a missing job is NotFound. The check and the write are one atomic
// store update.
func stopJob[T any](m *Mock, store *memstore.Store[*T], name, kind string, acc stopAccess[T]) error {
	_, err := stopJobAccepted(m, store, name, kind, acc)

	return err
}

// stopJobAccepted is stopJob that also reports whether the stop was newly
// accepted (false for the no-op cases), so callers publish a state-change
// event only for a real transition.
func stopJobAccepted[T any](m *Mock, store *memstore.Store[*T], name, kind string, acc stopAccess[T]) (bool, error) {
	accepted := false

	found := store.Update(name, func(j *T) *T {
		p := m.project(acc.created(j), *acc.stopRequested(j), len(acc.stages))
		if p.terminal() || p.state == jobStopping {
			return j
		}

		accepted = true
		updated := *j
		*acc.stopRequested(&updated) = fmtTime(m.opts.Clock.Now())

		return &updated
	})
	if !found {
		return false, errors.Newf(errors.NotFound, "%s %q not found", kind, name)
	}

	return accepted, nil
}

// listObserved lists a store through an observe function, so filters and sort
// apply to each job's observed state.
func listObserved[T any](store *memstore.Store[*T], observe func(*T) *T, spec listSpec[T], opts driver.ListOptions) (driver.ListPage[T], error) {
	stored := store.SortedValues()
	observed := make([]*T, 0, len(stored))

	for _, j := range stored {
		observed = append(observed, observe(j))
	}

	return listItems(observed, spec, opts)
}
