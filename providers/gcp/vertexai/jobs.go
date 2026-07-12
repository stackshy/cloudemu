package vertexai

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/vertexai/driver"
)

// cancelJob copy-then-Sets a job's State to canceled, or returns NotFound.
func cancelJob[T any](store *memstore.Store[*T], name string, setState func(*T)) error {
	j, ok := store.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "job %q not found", name)
	}

	updated := *j
	setState(&updated)
	store.Set(name, &updated)

	return nil
}

// --- Custom jobs ---

//nolint:dupl // synchronous-create job shape recurs across job families.
func (m *Mock) CreateCustomJob(_ context.Context, cfg driver.CustomJobConfig) (*driver.CustomJob, error) {
	now := m.now()
	name := m.resName(cfg.Location, "customJobs", m.newID())
	job := &driver.CustomJob{Name: name, DisplayName: cfg.DisplayName, State: driver.JobStateSucceeded, CreateTime: now, EndTime: now}
	m.customJobs.Set(name, job)
	m.emitMetric("custom_job/count", 1, map[string]string{"location": orLocation(cfg.Location)})

	out := *job

	return &out, nil
}

func (m *Mock) GetCustomJob(_ context.Context, name string) (*driver.CustomJob, error) {
	j, ok := m.customJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "custom job %q not found", name)
	}

	out := *j

	return &out, nil
}

func (m *Mock) ListCustomJobs(_ context.Context, location string) ([]driver.CustomJob, error) {
	out := make([]driver.CustomJob, 0)

	for _, j := range m.customJobs.All() {
		if location == "" || locationOf(j.Name) == location {
			out = append(out, *j)
		}
	}

	return out, nil
}

func (m *Mock) CancelCustomJob(_ context.Context, name string) error {
	return cancelJob(m.customJobs, name, func(j *driver.CustomJob) { j.State = driver.JobStateCancelled })
}

func (m *Mock) DeleteCustomJob(_ context.Context, name string) (*driver.Operation, error) {
	if !m.customJobs.Has(name) {
		return nil, errors.Newf(errors.NotFound, "custom job %q not found", name)
	}

	m.customJobs.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

// --- Batch prediction jobs ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateBatchPredictionJob(_ context.Context, cfg driver.BatchPredictionJobConfig) (*driver.BatchPredictionJob, error) {
	now := m.now()
	name := m.resName(cfg.Location, "batchPredictionJobs", m.newID())
	job := &driver.BatchPredictionJob{
		Name: name, DisplayName: cfg.DisplayName, Model: cfg.Model,
		State: driver.JobStateSucceeded, CreateTime: now, EndTime: now,
	}
	m.batchJobs.Set(name, job)
	m.emitMetric("batch_prediction_job/count", 1, map[string]string{"location": orLocation(cfg.Location)})

	out := *job

	return &out, nil
}

func (m *Mock) GetBatchPredictionJob(_ context.Context, name string) (*driver.BatchPredictionJob, error) {
	j, ok := m.batchJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "batch prediction job %q not found", name)
	}

	out := *j

	return &out, nil
}

func (m *Mock) ListBatchPredictionJobs(_ context.Context, location string) ([]driver.BatchPredictionJob, error) {
	out := make([]driver.BatchPredictionJob, 0)

	for _, j := range m.batchJobs.All() {
		if location == "" || locationOf(j.Name) == location {
			out = append(out, *j)
		}
	}

	return out, nil
}

func (m *Mock) CancelBatchPredictionJob(_ context.Context, name string) error {
	return cancelJob(m.batchJobs, name, func(j *driver.BatchPredictionJob) { j.State = driver.JobStateCancelled })
}

func (m *Mock) DeleteBatchPredictionJob(_ context.Context, name string) (*driver.Operation, error) {
	if !m.batchJobs.Has(name) {
		return nil, errors.Newf(errors.NotFound, "batch prediction job %q not found", name)
	}

	m.batchJobs.Delete(name)

	return m.doneOp(locationOf(name), name), nil
}

// --- Hyperparameter tuning jobs ---

func (m *Mock) CreateHyperparameterTuningJob(
	_ context.Context, cfg driver.HyperparameterTuningJobConfig,
) (*driver.HyperparameterTuningJob, error) {
	now := m.now()
	name := m.resName(cfg.Location, "hyperparameterTuningJobs", m.newID())
	job := &driver.HyperparameterTuningJob{
		Name: name, DisplayName: cfg.DisplayName, State: driver.JobStateSucceeded,
		MaxTrialCount: cfg.MaxTrialCount, CreateTime: now, EndTime: now,
	}
	m.hpoJobs.Set(name, job)

	out := *job

	return &out, nil
}

func (m *Mock) GetHyperparameterTuningJob(_ context.Context, name string) (*driver.HyperparameterTuningJob, error) {
	j, ok := m.hpoJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "hyperparameter tuning job %q not found", name)
	}

	out := *j

	return &out, nil
}

func (m *Mock) ListHyperparameterTuningJobs(_ context.Context, location string) ([]driver.HyperparameterTuningJob, error) {
	out := make([]driver.HyperparameterTuningJob, 0)

	for _, j := range m.hpoJobs.All() {
		if location == "" || locationOf(j.Name) == location {
			out = append(out, *j)
		}
	}

	return out, nil
}

func (m *Mock) CancelHyperparameterTuningJob(_ context.Context, name string) error {
	return cancelJob(m.hpoJobs, name, func(j *driver.HyperparameterTuningJob) { j.State = driver.JobStateCancelled })
}
