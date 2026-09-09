package streamanalytics

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Valid outputStartMode values for a streaming-job start request.
const (
	outputStartModeJobStartTime        = "JobStartTime"
	outputStartModeCustomTime          = "CustomTime"
	outputStartModeLastOutputEventTime = "LastOutputEventTime"
)

// validateStartMode enforces the outputStartMode enum and the CustomTime ->
// outputStartTime dependency that real Stream Analytics requires (an unknown
// mode, or CustomTime without outputStartTime, is a 400 BadRequest).
func validateStartMode(mode, startTime string) error {
	switch mode {
	case "", outputStartModeJobStartTime, outputStartModeLastOutputEventTime:
		return nil
	case outputStartModeCustomTime:
		if startTime == "" {
			return cerrors.New(cerrors.InvalidArgument,
				"outputStartTime is required when outputStartMode is CustomTime")
		}

		return nil
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "invalid outputStartMode %q", mode)
	}
}

// StartJob transitions a job into the Running state. In the synchronous
// emulator model the job settles to Running immediately (real Azure runs this
// as a long-running Starting -> Running transition). A start is legal only from
// Created, Stopped or Failed; starting a job that is already Running (or
// mid-transition) is rejected with a FailedPrecondition, which the wire layer
// maps to the 409 real ARM returns. outputStartMode / outputStartTime are
// recorded on the job so a subsequent read reflects the start parameters.
func (m *Mock) StartJob(
	_ context.Context, sub, rg, name, outputStartMode, outputStartTime string,
) (StreamingJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateStartMode(outputStartMode, outputStartTime); err != nil {
		return StreamingJob{}, err
	}

	k := jobKey(sub, rg, name)

	j, ok := m.jobs.Get(k)
	if !ok {
		return StreamingJob{}, cerrors.Newf(cerrors.NotFound, "streaming job %q not found", name)
	}

	if !isStartable(j.JobState) {
		return StreamingJob{}, cerrors.Newf(cerrors.FailedPrecondition,
			"streaming job %q cannot be started from state %q", name, j.JobState)
	}

	updated := *j

	if outputStartMode != "" {
		updated.OutputStartMode = outputStartMode
	}

	if outputStartTime != "" {
		updated.OutputStartTime = outputStartTime
	}

	updated.JobState = JobStateRunning
	m.jobs.Set(k, &updated)

	return cloneJob(&updated), nil
}

// StopJob transitions a running job into the Stopped state. In the synchronous
// emulator model the job settles to Stopped immediately (real Azure runs this
// as a long-running Stopping -> Stopped transition). A stop is legal only from
// Running or Degraded; stopping a job that is already Stopped or still in
// Created is rejected with a FailedPrecondition, which the wire layer maps to
// the 409 real ARM returns.
func (m *Mock) StopJob(_ context.Context, sub, rg, name string) (StreamingJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := jobKey(sub, rg, name)

	j, ok := m.jobs.Get(k)
	if !ok {
		return StreamingJob{}, cerrors.Newf(cerrors.NotFound, "streaming job %q not found", name)
	}

	if !isStoppable(j.JobState) {
		return StreamingJob{}, cerrors.Newf(cerrors.FailedPrecondition,
			"streaming job %q cannot be stopped from state %q", name, j.JobState)
	}

	updated := *j
	updated.JobState = JobStateStopped
	m.jobs.Set(k, &updated)

	return cloneJob(&updated), nil
}

// ScaleJob adjusts the streaming units of a running job's transformation. Real
// Azure runs this as a long-running Scaling transition; the emulator settles it
// immediately, leaving the job Running. Scaling is legal only from Running or
// Degraded. A missing transformation is tolerated: the scale is a no-op on the
// job's state and returns the job unchanged.
func (m *Mock) ScaleJob(_ context.Context, sub, rg, name string, streamingUnits *int) (StreamingJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := jobKey(sub, rg, name)

	j, ok := m.jobs.Get(k)
	if !ok {
		return StreamingJob{}, cerrors.Newf(cerrors.NotFound, "streaming job %q not found", name)
	}

	if !isStoppable(j.JobState) {
		return StreamingJob{}, cerrors.Newf(cerrors.FailedPrecondition,
			"streaming job %q cannot be scaled from state %q", name, j.JobState)
	}

	if streamingUnits != nil {
		m.scaleTransformation(sub, rg, name, *streamingUnits)
	}

	return cloneJob(j), nil
}

// isStartable reports whether a job in the given jobState may be started.
func isStartable(state string) bool {
	switch state {
	case JobStateCreated, JobStateStopped, JobStateFailed:
		return true
	default:
		return false
	}
}

// isStoppable reports whether a job in the given jobState may be stopped or
// scaled.
func isStoppable(state string) bool {
	switch state {
	case JobStateRunning, JobStateDegraded:
		return true
	default:
		return false
	}
}
