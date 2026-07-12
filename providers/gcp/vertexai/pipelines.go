package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

// --- Training pipelines ---

func (m *Mock) CreateTrainingPipeline(_ context.Context, cfg driver.TrainingPipelineConfig) (*driver.TrainingPipeline, error) {
	now := m.now()
	name := m.resName(cfg.Location, "trainingPipelines", m.newID())
	tp := &driver.TrainingPipeline{
		Name: name, DisplayName: cfg.DisplayName,
		State: driver.PipelineStateSucceeded, CreateTime: now, EndTime: now,
	}
	m.trainPipes.Set(name, tp)

	out := *tp

	return &out, nil
}

func (m *Mock) GetTrainingPipeline(_ context.Context, name string) (*driver.TrainingPipeline, error) {
	tp, ok := m.trainPipes.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "training pipeline %q not found", name)
	}

	out := *tp

	return &out, nil
}

func (m *Mock) ListTrainingPipelines(_ context.Context, location string) ([]driver.TrainingPipeline, error) {
	out := make([]driver.TrainingPipeline, 0)

	for _, tp := range m.trainPipes.All() {
		if location == "" || locationOf(tp.Name) == location {
			out = append(out, *tp)
		}
	}

	return out, nil
}

func (m *Mock) CancelTrainingPipeline(_ context.Context, name string) error {
	tp, ok := m.trainPipes.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "training pipeline %q not found", name)
	}

	updated := *tp
	updated.State = driver.PipelineStateCancelled
	m.trainPipes.Set(name, &updated)

	return nil
}

func (m *Mock) DeleteTrainingPipeline(_ context.Context, name string) (*driver.Operation, error) {
	if !m.trainPipes.Has(name) {
		return nil, errors.Newf(errors.NotFound, "training pipeline %q not found", name)
	}

	m.trainPipes.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

// --- Pipeline jobs ---

//nolint:dupl // synchronous-create job shape recurs across job families.
func (m *Mock) CreatePipelineJob(_ context.Context, cfg driver.PipelineJobConfig) (*driver.PipelineJob, error) {
	now := m.now()
	name := m.resName(cfg.Location, "pipelineJobs", m.newID())
	pj := &driver.PipelineJob{Name: name, DisplayName: cfg.DisplayName, State: driver.PipelineStateSucceeded, CreateTime: now, EndTime: now}
	m.pipelineJobs.Set(name, pj)
	m.emitMetric("pipeline_job/count", 1, map[string]string{"location": orLocation(cfg.Location)})

	out := *pj

	return &out, nil
}

func (m *Mock) GetPipelineJob(_ context.Context, name string) (*driver.PipelineJob, error) {
	pj, ok := m.pipelineJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "pipeline job %q not found", name)
	}

	out := *pj

	return &out, nil
}

func (m *Mock) ListPipelineJobs(_ context.Context, location string) ([]driver.PipelineJob, error) {
	out := make([]driver.PipelineJob, 0)

	for _, pj := range m.pipelineJobs.All() {
		if location == "" || locationOf(pj.Name) == location {
			out = append(out, *pj)
		}
	}

	return out, nil
}

func (m *Mock) CancelPipelineJob(_ context.Context, name string) error {
	pj, ok := m.pipelineJobs.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "pipeline job %q not found", name)
	}

	updated := *pj
	updated.State = driver.PipelineStateCancelled
	m.pipelineJobs.Set(name, &updated)

	return nil
}

func (m *Mock) DeletePipelineJob(_ context.Context, name string) (*driver.Operation, error) {
	if !m.pipelineJobs.Has(name) {
		return nil, errors.Newf(errors.NotFound, "pipeline job %q not found", name)
	}

	m.pipelineJobs.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}
