package fis

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/fis/driver"
)

// StartExperiment materializes an experiment from a template, copying its
// actions, targets, stop conditions, role and log configuration verbatim, and
// places it directly in the running state. There is no data plane, so the
// experiment stays running (a stable state) until StopExperiment moves it to the
// stopped terminal state; id, arn, state, creationTime and startTime are minted
// once and stable across reads.
func (m *Mock) StartExperiment(_ context.Context, in *driver.StartExperimentInput) (*driver.Experiment, error) {
	if in.ExperimentTemplateID == "" {
		return nil, validation("experimentTemplateId is required")
	}

	t, ok := m.templates.Get(in.ExperimentTemplateID)
	if !ok {
		return nil, notFound("experiment template %s not found", in.ExperimentTemplateID)
	}

	now := m.now()
	id := experimentID()

	opts := t.ExperimentOptions
	opts.ActionsMode = actionsModeRunAll

	if in.ActionsMode != "" {
		opts.ActionsMode = in.ActionsMode
	}

	e := driver.Experiment{
		ID:                   id,
		Arn:                  m.experimentARN(id),
		ExperimentTemplateID: in.ExperimentTemplateID,
		RoleArn:              t.RoleArn,
		State:                driver.ExperimentState{Status: statusRunning, Reason: reasonStarted},
		Actions:              experimentActionsFromTemplate(t.Actions, now),
		Targets:              copyTargets(t.Targets),
		StopConditions:       copyStopConditions(t.StopConditions),
		LogConfiguration:     copyLogConfiguration(t.LogConfiguration),
		ExperimentOptions:    opts,
		Tags:                 copyTags(in.Tags),
		CreationTime:         now,
		StartTime:            now,
	}

	m.experiments.Set(id, e)

	out := copyExperiment(&e)

	return &out, nil
}

// experimentActionsFromTemplate snapshots the template actions into running
// experiment actions.
func experimentActionsFromTemplate(in map[string]driver.Action, startTime time.Time) map[string]driver.ExperimentAction {
	if in == nil {
		return nil
	}

	out := make(map[string]driver.ExperimentAction, len(in))
	for name, a := range in {
		out[name] = driver.ExperimentAction{
			ActionID:    a.ActionID,
			Description: a.Description,
			Parameters:  copyTags(a.Parameters),
			Targets:     copyTags(a.Targets),
			StartAfter:  copyStrings(a.StartAfter),
			State:       driver.ExperimentActionState{Status: statusRunning, Reason: reasonStarted},
			StartTime:   startTime,
		}
	}

	return out
}

// StopExperiment moves a running experiment to the stopped terminal state. An
// experiment that is already in a terminal state yields a ConflictException,
// mirroring the real API.
func (m *Mock) StopExperiment(_ context.Context, id string) (*driver.Experiment, error) {
	e, ok := m.experiments.Get(id)
	if !ok {
		return nil, notFound("experiment %s not found", id)
	}

	if e.State.Status != statusRunning {
		return nil, conflict("experiment %s is in state %s and cannot be stopped", id, e.State.Status)
	}

	now := m.now()
	e.State = driver.ExperimentState{Status: statusStopped, Reason: reasonStopped}
	e.EndTime = now

	for name := range e.Actions {
		a := e.Actions[name]
		a.State = driver.ExperimentActionState{Status: statusStopped, Reason: reasonStopped}
		a.EndTime = now
		e.Actions[name] = a
	}

	m.experiments.Set(id, e)

	out := copyExperiment(&e)

	return &out, nil
}

// GetExperiment returns a copy of the experiment.
func (m *Mock) GetExperiment(_ context.Context, id string) (*driver.Experiment, error) {
	e, ok := m.experiments.Get(id)
	if !ok {
		return nil, notFound("experiment %s not found", id)
	}

	out := copyExperiment(&e)

	return &out, nil
}

// ListExperiments returns a deterministic page of experiments ordered by id.
func (m *Mock) ListExperiments(_ context.Context, page driver.Page) ([]*driver.Experiment, string, error) {
	stored := m.experiments.SortedValues()
	start, end, next := paginate(len(stored), page)
	out := make([]*driver.Experiment, 0, end-start)

	for i := start; i < end; i++ {
		e := copyExperiment(&stored[i])
		out = append(out, &e)
	}

	return out, next, nil
}
