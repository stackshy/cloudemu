package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/sagemaker/driver"
)

// --- Pipeline ---

func (m *Mock) CreatePipeline(_ context.Context, cfg driver.PipelineSpec) (*driver.Pipeline, error) {
	if cfg.PipelineName == "" {
		return nil, errors.New(errors.InvalidArgument, "pipelineName is required")
	}

	if m.pipelines.Has(cfg.PipelineName) {
		return nil, errors.Newf(errors.AlreadyExists, "pipeline %q already exists", cfg.PipelineName)
	}

	now := m.now()
	arn := m.arn("pipeline/" + cfg.PipelineName)
	p := &driver.Pipeline{
		PipelineName:     cfg.PipelineName,
		PipelineARN:      arn,
		RoleARN:          cfg.RoleARN,
		Definition:       cfg.Definition,
		Status:           driver.PipelineActive,
		CreationTime:     now,
		LastModifiedTime: now,
		Tags:             copyTags(cfg.Tags),
	}
	m.pipelines.Set(cfg.PipelineName, p)
	m.setTags(arn, cfg.Tags)
	m.emitResourceCreated("Pipeline")

	out := *p

	return &out, nil
}

func (m *Mock) DescribePipeline(_ context.Context, name string) (*driver.Pipeline, error) {
	p, ok := m.pipelines.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "pipeline %q not found", name)
	}

	out := *p

	return &out, nil
}

func (m *Mock) ListPipelines(_ context.Context) ([]driver.Pipeline, error) {
	all := m.pipelines.All()
	out := make([]driver.Pipeline, 0, len(all))

	for _, v := range all {
		out = append(out, *v)
	}

	return out, nil
}

func (m *Mock) UpdatePipeline(_ context.Context, name, definition string) (*driver.Pipeline, error) {
	p, ok := m.pipelines.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "pipeline %q not found", name)
	}

	if definition != "" {
		p.Definition = definition
	}

	p.LastModifiedTime = m.now()

	out := *p

	return &out, nil
}

func (m *Mock) DeletePipeline(_ context.Context, name string) error {
	if !m.pipelines.Has(name) {
		return errors.Newf(errors.NotFound, "pipeline %q not found", name)
	}

	m.pipelines.Delete(name)

	return nil
}

// --- Pipeline execution ---

func (m *Mock) StartPipelineExecution(_ context.Context, pipelineName string) (*driver.PipelineExecution, error) {
	if !m.pipelines.Has(pipelineName) {
		return nil, errors.Newf(errors.NotFound, "pipeline %q not found", pipelineName)
	}

	now := m.now()
	arn := m.arn("pipeline/" + pipelineName + "/execution/" + idgen.GenerateID(""))
	ex := &driver.PipelineExecution{
		ExecutionARN: arn,
		PipelineName: pipelineName,
		Status:       driver.ExecutionSucceeded, // synchronous Executing -> Succeeded
		StartTime:    now,
		EndTime:      now,
	}
	m.executions.Set(arn, ex)

	out := *ex

	return &out, nil
}

func (m *Mock) DescribePipelineExecution(_ context.Context, executionARN string) (*driver.PipelineExecution, error) {
	ex, ok := m.executions.Get(executionARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "pipeline execution %q not found", executionARN)
	}

	out := *ex

	return &out, nil
}

func (m *Mock) ListPipelineExecutions(_ context.Context, pipelineName string) ([]driver.PipelineExecution, error) {
	out := make([]driver.PipelineExecution, 0)

	for _, ex := range m.executions.All() {
		if pipelineName == "" || ex.PipelineName == pipelineName {
			out = append(out, *ex)
		}
	}

	return out, nil
}

func (m *Mock) StopPipelineExecution(_ context.Context, executionARN string) error {
	ex, ok := m.executions.Get(executionARN)
	if !ok {
		return errors.Newf(errors.NotFound, "pipeline execution %q not found", executionARN)
	}

	ex.Status = driver.ExecutionStopped
	ex.EndTime = m.now()

	return nil
}

// --- Experiment ---

func (m *Mock) CreateExperiment(_ context.Context, cfg driver.ExperimentSpec) (*driver.Experiment, error) {
	if cfg.ExperimentName == "" {
		return nil, errors.New(errors.InvalidArgument, "experimentName is required")
	}

	if m.experiments.Has(cfg.ExperimentName) {
		return nil, errors.Newf(errors.AlreadyExists, "experiment %q already exists", cfg.ExperimentName)
	}

	arn := m.arn("experiment/" + cfg.ExperimentName)
	e := &driver.Experiment{
		ExperimentName: cfg.ExperimentName,
		ExperimentARN:  arn,
		Description:    cfg.Description,
		CreationTime:   m.now(),
		Tags:           copyTags(cfg.Tags),
	}
	m.experiments.Set(cfg.ExperimentName, e)
	m.setTags(arn, cfg.Tags)

	out := *e

	return &out, nil
}

func (m *Mock) DescribeExperiment(_ context.Context, name string) (*driver.Experiment, error) {
	e, ok := m.experiments.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "experiment %q not found", name)
	}

	out := *e

	return &out, nil
}

func (m *Mock) ListExperiments(_ context.Context) ([]driver.Experiment, error) {
	all := m.experiments.All()
	out := make([]driver.Experiment, 0, len(all))

	for _, v := range all {
		out = append(out, *v)
	}

	return out, nil
}

func (m *Mock) DeleteExperiment(_ context.Context, name string) error {
	if !m.experiments.Has(name) {
		return errors.Newf(errors.NotFound, "experiment %q not found", name)
	}

	m.experiments.Delete(name)

	return nil
}

// --- Trial ---

func (m *Mock) CreateTrial(_ context.Context, cfg driver.TrialSpec) (*driver.Trial, error) {
	if cfg.TrialName == "" {
		return nil, errors.New(errors.InvalidArgument, "trialName is required")
	}

	if cfg.ExperimentName != "" && !m.experiments.Has(cfg.ExperimentName) {
		return nil, errors.Newf(errors.InvalidArgument, "experiment %q not found", cfg.ExperimentName)
	}

	if m.trials.Has(cfg.TrialName) {
		return nil, errors.Newf(errors.AlreadyExists, "trial %q already exists", cfg.TrialName)
	}

	arn := m.arn("experiment-trial/" + cfg.TrialName)
	t := &driver.Trial{
		TrialName:      cfg.TrialName,
		TrialARN:       arn,
		ExperimentName: cfg.ExperimentName,
		CreationTime:   m.now(),
		Tags:           copyTags(cfg.Tags),
	}
	m.trials.Set(cfg.TrialName, t)
	m.setTags(arn, cfg.Tags)

	out := *t

	return &out, nil
}

func (m *Mock) DescribeTrial(_ context.Context, name string) (*driver.Trial, error) {
	t, ok := m.trials.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "trial %q not found", name)
	}

	out := *t

	return &out, nil
}

func (m *Mock) ListTrials(_ context.Context, experimentName string) ([]driver.Trial, error) {
	out := make([]driver.Trial, 0)

	for _, t := range m.trials.All() {
		if experimentName == "" || t.ExperimentName == experimentName {
			out = append(out, *t)
		}
	}

	return out, nil
}

func (m *Mock) DeleteTrial(_ context.Context, name string) error {
	if !m.trials.Has(name) {
		return errors.Newf(errors.NotFound, "trial %q not found", name)
	}

	m.trials.Delete(name)

	return nil
}
