package fis

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/fis/driver"
)

// CreateExperimentTemplate provisions a new experiment template synchronously
// with stable computed fields (id, arn, creationTime, lastUpdateTime). The
// actions, targets and stop conditions are stored verbatim.
func (m *Mock) CreateExperimentTemplate(
	_ context.Context, in *driver.CreateExperimentTemplateInput,
) (*driver.ExperimentTemplate, error) {
	if in.Description == "" {
		return nil, validation("description is required")
	}

	if in.RoleArn == "" {
		return nil, validation("roleArn is required")
	}

	if len(in.StopConditions) == 0 {
		return nil, validation("at least one stop condition is required")
	}

	if len(in.Actions) == 0 {
		return nil, validation("at least one action is required")
	}

	now := m.now()
	id := templateID()

	t := driver.ExperimentTemplate{
		ID:                id,
		Arn:               m.templateARN(id),
		Description:       in.Description,
		RoleArn:           in.RoleArn,
		Actions:           copyActions(in.Actions),
		Targets:           copyTargets(in.Targets),
		StopConditions:    copyStopConditions(in.StopConditions),
		LogConfiguration:  copyLogConfiguration(in.LogConfiguration),
		ExperimentOptions: resolveExperimentOptions(in.ExperimentOptions),
		Tags:              copyTags(in.Tags),
		CreationTime:      now,
		LastUpdateTime:    now,
	}

	m.templates.Set(id, t)

	out := copyTemplate(&t)

	return &out, nil
}

// resolveExperimentOptions applies the real-service defaults for an experiment
// template's options when the caller supplies none or leaves a field blank.
func resolveExperimentOptions(in *driver.ExperimentOptions) driver.ExperimentOptions {
	out := driver.ExperimentOptions{
		AccountTargeting:          accountTargetingSingle,
		EmptyTargetResolutionMode: emptyResolutionFail,
	}

	if in == nil {
		return out
	}

	if in.AccountTargeting != "" {
		out.AccountTargeting = in.AccountTargeting
	}

	if in.EmptyTargetResolutionMode != "" {
		out.EmptyTargetResolutionMode = in.EmptyTargetResolutionMode
	}

	return out
}

// GetExperimentTemplate returns a copy of the template.
func (m *Mock) GetExperimentTemplate(_ context.Context, id string) (*driver.ExperimentTemplate, error) {
	t, ok := m.templates.Get(id)
	if !ok {
		return nil, notFound("experiment template %s not found", id)
	}

	out := copyTemplate(&t)

	return &out, nil
}

// UpdateExperimentTemplate replaces the members present in the request and
// advances lastUpdateTime. id, arn and creationTime are stable.
func (m *Mock) UpdateExperimentTemplate(
	_ context.Context, in *driver.UpdateExperimentTemplateInput,
) (*driver.ExperimentTemplate, error) {
	t, ok := m.templates.Get(in.ID)
	if !ok {
		return nil, notFound("experiment template %s not found", in.ID)
	}

	if in.Description != nil {
		t.Description = *in.Description
	}

	if in.RoleArn != nil {
		t.RoleArn = *in.RoleArn
	}

	if in.Actions != nil {
		t.Actions = copyActions(*in.Actions)
	}

	if in.Targets != nil {
		t.Targets = copyTargets(*in.Targets)
	}

	if in.StopConditions != nil {
		t.StopConditions = copyStopConditions(*in.StopConditions)
	}

	if in.LogConfiguration != nil {
		t.LogConfiguration = copyLogConfiguration(in.LogConfiguration)
	}

	if in.ExperimentOptions != nil {
		t.ExperimentOptions = mergeExperimentOptions(t.ExperimentOptions, in.ExperimentOptions)
	}

	t.LastUpdateTime = m.now()

	m.templates.Set(in.ID, t)

	out := copyTemplate(&t)

	return &out, nil
}

// mergeExperimentOptions overlays the non-empty fields of an update onto the
// stored options; the real API's UpdateExperimentTemplate only touches supplied
// members.
func mergeExperimentOptions(cur driver.ExperimentOptions, upd *driver.ExperimentOptions) driver.ExperimentOptions {
	if upd.AccountTargeting != "" {
		cur.AccountTargeting = upd.AccountTargeting
	}

	if upd.EmptyTargetResolutionMode != "" {
		cur.EmptyTargetResolutionMode = upd.EmptyTargetResolutionMode
	}

	return cur
}

// DeleteExperimentTemplate removes a template and returns its last description.
func (m *Mock) DeleteExperimentTemplate(_ context.Context, id string) (*driver.ExperimentTemplate, error) {
	t, ok := m.templates.Get(id)
	if !ok {
		return nil, notFound("experiment template %s not found", id)
	}

	out := copyTemplate(&t)

	m.templates.Delete(id)

	return &out, nil
}

// ListExperimentTemplates returns a deterministic page of templates ordered by id.
func (m *Mock) ListExperimentTemplates(
	_ context.Context, page driver.Page,
) ([]*driver.ExperimentTemplate, string, error) {
	stored := m.templates.SortedValues()
	start, end, next := paginate(len(stored), page)
	out := make([]*driver.ExperimentTemplate, 0, end-start)

	for i := start; i < end; i++ {
		t := copyTemplate(&stored[i])
		out = append(out, &t)
	}

	return out, next, nil
}
