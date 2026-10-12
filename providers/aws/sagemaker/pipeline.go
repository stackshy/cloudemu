package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// --- Pipeline ---

func (m *Mock) CreatePipeline(_ context.Context, cfg driver.PipelineSpec) (*driver.Pipeline, error) {
	if cfg.PipelineName == "" {
		return nil, errors.New(errors.InvalidArgument, "pipelineName is required")
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
	if !m.pipelines.SetIfAbsent(cfg.PipelineName, p) {
		return nil, errors.Newf(errors.AlreadyExists, "pipeline %q already exists", cfg.PipelineName)
	}
	m.setTags(arn, cfg.Tags)

	return clonePipeline(p), nil
}

func (m *Mock) DescribePipeline(_ context.Context, name string) (*driver.Pipeline, error) {
	p, ok := m.pipelines.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "pipeline %q not found", name)
	}

	return clonePipeline(p), nil
}

func (m *Mock) ListPipelines(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.Pipeline], error) {
	return listFrom(m.pipelines, listSpec[driver.Pipeline]{
		clone:    func(t *driver.Pipeline) driver.Pipeline { return *clonePipeline(t) },
		name:     func(t *driver.Pipeline) string { return t.PipelineName },
		status:   func(t *driver.Pipeline) string { return t.Status },
		created:  func(t *driver.Pipeline) string { return t.CreationTime },
		modified: func(t *driver.Pipeline) string { return t.LastModifiedTime },
	}, opts)
}

func (m *Mock) UpdatePipeline(_ context.Context, name, definition string) (*driver.Pipeline, error) {
	p, ok := m.pipelines.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "pipeline %q not found", name)
	}

	updated := *p
	if definition != "" {
		updated.Definition = definition
	}

	updated.LastModifiedTime = m.now()
	m.pipelines.Set(name, &updated)

	return clonePipeline(&updated), nil
}

func (m *Mock) DeletePipeline(_ context.Context, name string) error {
	if !m.pipelines.Has(name) {
		return errors.Newf(errors.NotFound, "pipeline %q not found", name)
	}

	m.pipelines.Delete(name)
	m.dropTags(m.arn("pipeline/" + name))

	return nil
}

// --- Pipeline execution ---

func (m *Mock) StartPipelineExecution(ctx context.Context, pipelineName string) (*driver.PipelineExecution, error) {
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
	m.emitPipelineMetrics(ctx, pipelineName, true)
	m.emitPipelineExecutionEvents(ctx, ex, driver.ExecutionExecuting, driver.ExecutionSucceeded)

	out := *ex

	return &out, nil
}

func (m *Mock) DescribePipelineExecution(_ context.Context, executionARN string) (*driver.PipelineExecution, error) {
	if err := validateARN("pipelineExecutionArn", executionARN); err != nil {
		return nil, err
	}

	ex, ok := m.executions.Get(executionARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "pipeline execution %q not found", executionARN)
	}

	out := *ex

	return &out, nil
}

func (m *Mock) ListPipelineExecutions(_ context.Context, pipelineName string, opts driver.ListOptions) (driver.ListPage[driver.PipelineExecution], error) {
	return listFrom(m.executions, listSpec[driver.PipelineExecution]{
		name:    func(t *driver.PipelineExecution) string { return t.ExecutionARN },
		status:  func(t *driver.PipelineExecution) string { return t.Status },
		created: func(t *driver.PipelineExecution) string { return t.StartTime },
		extra: func(t *driver.PipelineExecution, _ map[string]string) bool {
			return pipelineName == "" || t.PipelineName == pipelineName
		},
	}, opts)
}

func (m *Mock) StopPipelineExecution(ctx context.Context, executionARN string) error {
	if err := validateARN("pipelineExecutionArn", executionARN); err != nil {
		return err
	}

	ex, ok := m.executions.Get(executionARN)
	if !ok {
		return errors.Newf(errors.NotFound, "pipeline execution %q not found", executionARN)
	}

	updated := *ex
	updated.Status = driver.ExecutionStopped
	updated.EndTime = m.now()
	m.executions.Set(executionARN, &updated)
	m.emitPipelineExecutionEvents(ctx, &updated, driver.ExecutionStopping, driver.ExecutionStopped)

	return nil
}

// --- Experiment ---

func (m *Mock) CreateExperiment(_ context.Context, cfg driver.ExperimentSpec) (*driver.Experiment, error) {
	if cfg.ExperimentName == "" {
		return nil, errors.New(errors.InvalidArgument, "experimentName is required")
	}

	arn := m.arn("experiment/" + cfg.ExperimentName)
	e := &driver.Experiment{
		ExperimentName: cfg.ExperimentName,
		ExperimentARN:  arn,
		Description:    cfg.Description,
		CreationTime:   m.now(),
		Tags:           copyTags(cfg.Tags),
	}
	if !m.experiments.SetIfAbsent(cfg.ExperimentName, e) {
		return nil, errors.Newf(errors.AlreadyExists, "experiment %q already exists", cfg.ExperimentName)
	}
	m.setTags(arn, cfg.Tags)

	return cloneExperiment(e), nil
}

func (m *Mock) DescribeExperiment(_ context.Context, name string) (*driver.Experiment, error) {
	e, ok := m.experiments.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "experiment %q not found", name)
	}

	return cloneExperiment(e), nil
}

func (m *Mock) ListExperiments(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.Experiment], error) {
	return listFrom(m.experiments, listSpec[driver.Experiment]{
		clone:   func(t *driver.Experiment) driver.Experiment { return *cloneExperiment(t) },
		name:    func(t *driver.Experiment) string { return t.ExperimentName },
		created: func(t *driver.Experiment) string { return t.CreationTime },
	}, opts)
}

// DeleteExperiment deletes an experiment that has no trials left (trials must
// be deleted first) and removes its lineage associations.
func (m *Mock) DeleteExperiment(_ context.Context, name string) error {
	m.entityMu.Lock()
	defer m.entityMu.Unlock()

	e, ok := m.experiments.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "experiment %q not found", name)
	}

	for _, t := range m.trials.All() {
		if t.ExperimentName == name {
			return errors.Newf(errors.FailedPrecondition, "experiment %q still has trials; delete them first", name)
		}
	}

	m.experiments.Delete(name)
	m.dropTags(m.arn("experiment/" + name))
	m.removeAssociationsOf(e.ExperimentARN)

	return nil
}

// --- Trial ---

func (m *Mock) CreateTrial(_ context.Context, cfg driver.TrialSpec) (*driver.Trial, error) {
	if cfg.TrialName == "" {
		return nil, errors.New(errors.InvalidArgument, "trialName is required")
	}

	// entityMu orders the experiment check against DeleteExperiment, so a trial
	// is never created under an experiment that is being deleted.
	m.entityMu.Lock()
	defer m.entityMu.Unlock()

	if cfg.ExperimentName != "" && !m.experiments.Has(cfg.ExperimentName) {
		return nil, errors.Newf(errors.NotFound, "experiment %q not found", cfg.ExperimentName)
	}

	arn := m.arn("experiment-trial/" + cfg.TrialName)
	t := &driver.Trial{
		TrialName:      cfg.TrialName,
		TrialARN:       arn,
		ExperimentName: cfg.ExperimentName,
		CreationTime:   m.now(),
		Tags:           copyTags(cfg.Tags),
	}
	if !m.trials.SetIfAbsent(cfg.TrialName, t) {
		return nil, errors.Newf(errors.AlreadyExists, "trial %q already exists", cfg.TrialName)
	}
	m.setTags(arn, cfg.Tags)

	return cloneTrial(t), nil
}

func cloneTrial(in *driver.Trial) *driver.Trial {
	out := *in
	out.Tags = copyTags(in.Tags)
	out.TrialComponentNames = append([]string(nil), in.TrialComponentNames...)

	return &out
}

func (m *Mock) DescribeTrial(_ context.Context, name string) (*driver.Trial, error) {
	t, ok := m.trials.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "trial %q not found", name)
	}

	return cloneTrial(t), nil
}

func (m *Mock) ListTrials(_ context.Context, experimentName string, opts driver.ListOptions) (driver.ListPage[driver.Trial], error) {
	return listFrom(m.trials, listSpec[driver.Trial]{
		name:    func(t *driver.Trial) string { return t.TrialName },
		created: func(t *driver.Trial) string { return t.CreationTime },
		extra: func(t *driver.Trial, _ map[string]string) bool {
			return experimentName == "" || t.ExperimentName == experimentName
		},
		clone: func(t *driver.Trial) driver.Trial { return *cloneTrial(t) },
	}, opts)
}

// DeleteTrial deletes a trial that has no trial components left (they must be
// disassociated and deleted first).
func (m *Mock) DeleteTrial(_ context.Context, name string) error {
	m.entityMu.Lock()
	defer m.entityMu.Unlock()

	t, ok := m.trials.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "trial %q not found", name)
	}

	if len(t.TrialComponentNames) > 0 {
		return errors.Newf(errors.FailedPrecondition, "trial %q still has trial components; disassociate them first", name)
	}

	m.trials.Delete(name)
	m.dropTags(m.arn("experiment-trial/" + name))

	return nil
}

func clonePipeline(in *driver.Pipeline) *driver.Pipeline {
	out := *in
	out.Tags = copyTags(in.Tags)

	return &out
}

func cloneExperiment(in *driver.Experiment) *driver.Experiment {
	out := *in
	out.Tags = copyTags(in.Tags)

	return &out
}
