package batch

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

// Status reasons the emulator reports. "Essential container in task exited" is
// what Batch reports once a job's container exits; "Dependent Job failed" is
// the reason a job fails because one of its dependencies failed.
const (
	reasonContainerExited = "Essential container in task exited"
	reasonDependentFailed = "Dependent Job failed"
)

// Phase offsets, in units of a job's phase duration, measured from the instant
// it became ready (dependencies met): RUNNABLE for one phase, STARTING for the
// next, RUNNING for the third, then SUCCEEDED.
const (
	phasesToStarting  = 1
	phasesToRunning   = 2
	phasesToSucceeded = 3
)

// jobStop records a CancelJob/TerminateJob that took effect on a job (or one
// array child) at At. StartedAt is when that unit had started RUNNING (zero if
// it never did), so a terminated running job keeps its startedAt.
type jobStop struct {
	At        time.Time `json:"at"`
	Reason    string    `json:"reason"`
	Terminate bool      `json:"terminate"`
	StartedAt time.Time `json:"startedAt,omitempty"`
}

// jobRecord is a stored job. Job holds the static detail captured at
// submission; status, statusReason and the started/stopped times are derived
// on every read from SubmittedAt, Phase and the job's dependencies, the way the
// settle overlay derives other services' transient states: no goroutines or
// timers, advancing the clock is what moves a job through its lifecycle.
//
// Phase is the dwell time in each non-terminal phase; zero (AsyncSettle off)
// means the job is SUCCEEDED as soon as its dependencies are. Array children
// are not stored individually: child i's state is derived from the parent
// record, with ChildStops (keyed by the decimal index) holding any child that
// was canceled or terminated.
type jobRecord struct {
	Job         driver.Job         `json:"job"`
	Seq         int64              `json:"seq"`
	SubmittedAt time.Time          `json:"submittedAt"`
	Phase       time.Duration      `json:"phase"`
	Sequential  bool               `json:"sequential,omitempty"`
	Stop        *jobStop           `json:"stop,omitempty"`
	ChildStops  map[string]jobStop `json:"childStops,omitempty"`
	LastEmitted string             `json:"lastEmitted,omitempty"`
}

//nolint:gocritic // clone intentionally takes a value to return an independent copy-on-write copy
func cloneJobRecord(r jobRecord) jobRecord {
	r.Job = cloneJob(r.Job)

	if r.Stop != nil {
		s := *r.Stop
		r.Stop = &s
	}

	if r.ChildStops != nil {
		cs := make(map[string]jobStop, len(r.ChildStops))
		for k, v := range r.ChildStops {
			cs[k] = v
		}

		r.ChildStops = cs
	}

	return r
}

//nolint:gocritic // clone intentionally takes a value to return an independent copy-on-write copy
func cloneJob(j driver.Job) driver.Job {
	if j.DependsOn != nil {
		deps := make([]driver.JobDependency, len(j.DependsOn))
		copy(deps, j.DependsOn)
		j.DependsOn = deps
	}

	if j.ArrayProperties != nil {
		ap := *j.ArrayProperties
		ap.Index = copyInt32(ap.Index)

		if ap.StatusSummary != nil {
			ss := make(map[string]int32, len(ap.StatusSummary))
			for k, v := range ap.StatusSummary {
				ss[k] = v
			}

			ap.StatusSummary = ss
		}

		j.ArrayProperties = &ap
	}

	j.Parameters = copyTags(j.Parameters)
	j.Container = copyRaw(j.Container)
	j.NodeProperties = copyRaw(j.NodeProperties)
	j.EcsProperties = copyRaw(j.EcsProperties)
	j.EksProperties = copyRaw(j.EksProperties)
	j.RetryStrategy = copyRaw(j.RetryStrategy)
	j.Timeout = copyRaw(j.Timeout)
	j.PlatformCapabilities = copyStrings(j.PlatformCapabilities)
	j.PropagateTags = copyBool(j.PropagateTags)
	j.SchedulingPriority = copyInt32(j.SchedulingPriority)
	j.Tags = copyTags(j.Tags)

	return j
}

func (r *jobRecord) arraySize() int {
	if r.Job.ArrayProperties == nil {
		return 0
	}

	return int(r.Job.ArrayProperties.Size)
}

// jobView is a job's (or array child's) state at one observation instant.
type jobView struct {
	Status    string
	Reason    string
	StartedAt time.Time
	StoppedAt time.Time
	Stop      *jobStop
}

func isTerminal(status string) bool {
	return status == driver.JobStatusSucceeded || status == driver.JobStatusFailed
}

// cancellable reports whether CancelJob affects a job in status: only jobs that
// haven't reached STARTING are canceled.
func cancellable(status string) bool {
	switch status {
	case driver.JobStatusSubmitted, driver.JobStatusPending, driver.JobStatusRunnable:
		return true
	default:
		return false
	}
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}

	return a
}

func stoppedView(s *jobStop) jobView {
	return jobView{Status: driver.JobStatusFailed, Reason: s.Reason, StartedAt: s.StartedAt, StoppedAt: s.At, Stop: s}
}

// observer derives job states at one instant, memoizing each job (and each
// array's children) so a dependency chain or a large array is evaluated once
// per call. It reads the job store without locks of its own; callers that
// mutate records hold jobsMu.
type observer struct {
	m        *Mock
	now      time.Time
	views    map[string]jobView
	children map[string][]jobView
}

func (m *Mock) newObserver(now time.Time) *observer {
	return &observer{m: m, now: now, views: map[string]jobView{}, children: map[string][]jobView{}}
}

// splitChildID splits "parent:index" into its parts.
func splitChildID(id string) (parent string, idx int, ok bool) {
	i := strings.LastIndex(id, ":")
	if i <= 0 {
		return "", 0, false
	}

	// Parse with a 32-bit bound: the index ends up in the int32
	// ArrayProperties.Index field.
	n, err := strconv.ParseInt(id[i+1:], 10, 32)
	if err != nil || n < 0 {
		return "", 0, false
	}

	return id[:i], int(n), true
}

// lookup resolves a job ID to its record and, for an array child, its index
// (-1 for a top-level job).
func (m *Mock) lookup(id string) (rec jobRecord, idx int, ok bool) {
	if r, found := m.jobs.Get(id); found {
		return r, -1, true
	}

	parent, i, isChild := splitChildID(id)
	if !isChild {
		return jobRecord{}, 0, false
	}

	r, found := m.jobs.Get(parent)
	if !found || i >= r.arraySize() {
		return jobRecord{}, 0, false
	}

	return r, i, true
}

// viewID returns the state of the job (or array child) with the given ID.
func (o *observer) viewID(id string) (jobView, bool) {
	rec, idx, ok := o.m.lookup(id)
	if !ok {
		return jobView{}, false
	}

	if idx >= 0 {
		return o.childViews(&rec)[idx], true
	}

	return o.view(&rec), true
}

// view returns the state of a top-level job or array parent.
func (o *observer) view(rec *jobRecord) jobView {
	if v, ok := o.views[rec.Job.JobID]; ok {
		return v
	}

	var v jobView

	switch {
	case rec.arraySize() > 0:
		v = o.parentView(rec)
	case rec.Stop != nil:
		v = stoppedView(rec.Stop)
	default:
		v = o.natural(rec, -1, nil)
	}

	o.views[rec.Job.JobID] = v

	return v
}

// childViews returns the state of every child of an array job, in index order.
func (o *observer) childViews(rec *jobRecord) []jobView {
	if cv, ok := o.children[rec.Job.JobID]; ok {
		return cv
	}

	n := rec.arraySize()
	cv := make([]jobView, n)

	for i := 0; i < n; i++ {
		if s, ok := rec.ChildStops[strconv.Itoa(i)]; ok {
			cv[i] = stoppedView(&s)

			continue
		}

		var prev *jobView
		if rec.Sequential && i > 0 {
			prev = &cv[i-1]
		}

		cv[i] = o.natural(rec, i, prev)
	}

	o.children[rec.Job.JobID] = cv

	return cv
}

// childAggregate summarizes an array's children.
type childAggregate struct {
	started, stopped time.Time
	failed, open     bool
}

func aggregateChildren(cv []jobView) childAggregate {
	var a childAggregate

	for i := range cv {
		c := &cv[i]
		if !isTerminal(c.Status) {
			a.open = true

			continue
		}

		a.failed = a.failed || c.Status == driver.JobStatusFailed

		if !c.StartedAt.IsZero() && (a.started.IsZero() || c.StartedAt.Before(a.started)) {
			a.started = c.StartedAt
		}

		a.stopped = later(a.stopped, c.StoppedAt)
	}

	return a
}

// parentView aggregates an array parent from its children: the parent stays
// PENDING while any child is unfinished, then SUCCEEDED if every child
// succeeded and FAILED otherwise.
func (o *observer) parentView(rec *jobRecord) jobView {
	a := aggregateChildren(o.childViews(rec))

	if a.open {
		if o.now.Before(rec.SubmittedAt.Add(rec.Phase)) {
			return jobView{Status: driver.JobStatusSubmitted}
		}

		return jobView{Status: driver.JobStatusPending}
	}

	v := jobView{Status: driver.JobStatusSucceeded, StartedAt: a.started, StoppedAt: a.stopped, Stop: rec.Stop}
	if a.failed {
		v.Status = driver.JobStatusFailed
	}

	if rec.Stop != nil {
		v.Reason = rec.Stop.Reason
	}

	return v
}

// depState folds one dependency into the readiness computation: it returns the
// later ready time, whether the job must keep waiting, and whether the
// dependency failed.
func depState(dv *jobView, ready time.Time) (next time.Time, waiting, failed bool) {
	switch dv.Status {
	case driver.JobStatusSucceeded:
		return later(ready, dv.StoppedAt), false, false
	case driver.JobStatusFailed:
		return later(ready, dv.StoppedAt), false, true
	default:
		return ready, true, false
	}
}

// dependencyID returns the job a dependency waits on for array child idx: an
// N_TO_N dependency on an array job pairs child idx with the dependency's child
// idx; anything else waits on the dependency job as a whole.
func (o *observer) dependencyID(dep driver.JobDependency, idx int) string {
	if dep.Type != driver.DependencyNToN || idx < 0 {
		return dep.JobID
	}

	if r, ok := o.m.jobs.Get(dep.JobID); ok && idx < r.arraySize() {
		return dep.JobID + ":" + strconv.Itoa(idx)
	}

	return dep.JobID
}

// natural derives the state of a job (idx -1) or array child (idx >= 0) that
// was not canceled or terminated. prev is the previous child of a SEQUENTIAL
// array. Each non-terminal phase lasts rec.Phase: SUBMITTED from submission,
// PENDING while a dependency is unfinished, then RUNNABLE, STARTING and RUNNING
// before SUCCEEDED.
func (o *observer) natural(rec *jobRecord, idx int, prev *jobView) jobView {
	ready := rec.SubmittedAt.Add(rec.Phase)
	if o.now.Before(ready) {
		return jobView{Status: driver.JobStatusSubmitted}
	}

	ready, blocked, done := o.readiness(rec, idx, prev, ready)
	if done {
		return blocked
	}

	return o.phaseView(ready, rec.Phase)
}

// readiness folds the job's dependencies (and a SEQUENTIAL predecessor) into
// the instant it became ready. done reports that the dependencies decide the
// state on their own (PENDING, or FAILED because one failed), returned as view.
func (o *observer) readiness(rec *jobRecord, idx int, prev *jobView, ready time.Time) (time.Time, jobView, bool) {
	deps := make([]jobView, 0, len(rec.Job.DependsOn)+1)

	for _, dep := range rec.Job.DependsOn {
		if dep.JobID == "" {
			continue
		}

		if dv, ok := o.viewID(o.dependencyID(dep, idx)); ok {
			deps = append(deps, dv)
		}
	}

	if prev != nil {
		deps = append(deps, *prev)
	}

	waiting := false

	for i := range deps {
		next, wait, failed := depState(&deps[i], ready)
		if failed {
			return ready, jobView{Status: driver.JobStatusFailed, Reason: reasonDependentFailed, StoppedAt: next}, true
		}

		ready = next
		waiting = waiting || wait
	}

	if waiting {
		return ready, jobView{Status: driver.JobStatusPending}, true
	}

	return ready, jobView{}, false
}

// phaseView places a ready job in its RUNNABLE/STARTING/RUNNING/SUCCEEDED
// phase given the phase duration d.
func (o *observer) phaseView(ready time.Time, d time.Duration) jobView {
	started := ready.Add(phasesToRunning * d)
	stopped := ready.Add(phasesToSucceeded * d)

	switch {
	case o.now.Before(ready.Add(phasesToStarting * d)):
		return jobView{Status: driver.JobStatusRunnable}
	case o.now.Before(started):
		return jobView{Status: driver.JobStatusStarting}
	case o.now.Before(stopped):
		return jobView{Status: driver.JobStatusRunning, StartedAt: started}
	default:
		return jobView{
			Status: driver.JobStatusSucceeded, Reason: reasonContainerExited,
			StartedAt: started, StoppedAt: stopped,
		}
	}
}

func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.UnixMilli()
}

// statusSummary counts an array's children per status, listing every status.
func statusSummary(cv []jobView) map[string]int32 {
	out := map[string]int32{
		driver.JobStatusSubmitted: 0, driver.JobStatusPending: 0, driver.JobStatusRunnable: 0,
		driver.JobStatusStarting: 0, driver.JobStatusRunning: 0, driver.JobStatusSucceeded: 0,
		driver.JobStatusFailed: 0,
	}

	for i := range cv {
		out[cv[i].Status]++
	}

	return out
}

// withExitCode returns the container detail with exitCode 0 added, as Batch
// reports for a job whose container exited successfully.
func withExitCode(container json.RawMessage) json.RawMessage {
	if !isSet(container) {
		return container
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(container, &obj); err != nil {
		return container
	}

	obj["exitCode"] = json.RawMessage("0")

	out, err := json.Marshal(obj)
	if err != nil {
		return container
	}

	return out
}

// render builds the externally visible job (top-level, idx -1, or array child
// idx) as observed by o.
func (o *observer) render(rec *jobRecord, idx int) driver.Job {
	j := cloneJob(rec.Job)

	var v jobView

	if idx >= 0 {
		v = o.childViews(rec)[idx]
		j.JobID = rec.Job.JobID + ":" + strconv.Itoa(idx)
		j.JobARN = rec.Job.JobARN + ":" + strconv.Itoa(idx)
		i := int32(idx)
		j.ArrayProperties = &driver.ArrayProperties{Index: &i}
	} else {
		v = o.view(rec)

		if rec.arraySize() > 0 {
			j.ArrayProperties.StatusSummary = statusSummary(o.childViews(rec))
		}
	}

	j.Status = v.Status
	j.StatusReason = v.Reason
	j.StartedAt = millis(v.StartedAt)
	j.StoppedAt = millis(v.StoppedAt)

	stop := v.Stop
	if idx < 0 && rec.Stop != nil {
		stop = rec.Stop
	}

	if stop != nil {
		j.IsCancelled = !stop.Terminate
		j.IsTerminated = stop.Terminate
	}

	if v.Status == driver.JobStatusSucceeded && (idx >= 0 || rec.arraySize() == 0) {
		j.Container = withExitCode(j.Container)
	}

	return j
}
