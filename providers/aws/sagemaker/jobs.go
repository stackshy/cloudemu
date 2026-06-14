package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/memstore"
	"github.com/stackshy/cloudemu/sagemaker/driver"
)

// --- Training jobs ---

// CreateTrainingJob creates a training job that completes synchronously.
//
//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateTrainingJob(_ context.Context, cfg driver.TrainingJobConfig) (*driver.TrainingJob, error) {
	if cfg.JobName == "" {
		return nil, errors.New(errors.InvalidArgument, "trainingJobName is required")
	}

	if m.trainingJobs.Has(cfg.JobName) {
		return nil, errors.Newf(errors.AlreadyExists, "training job %q already exists", cfg.JobName)
	}

	now := m.now()
	arn := m.arn("training-job/" + cfg.JobName)
	job := &driver.TrainingJob{
		JobName:              cfg.JobName,
		JobARN:               arn,
		RoleARN:              cfg.RoleARN,
		AlgorithmImage:       cfg.AlgorithmImage,
		HyperParameters:      copyStrMap(cfg.HyperParameters),
		InputChannels:        cfg.InputChannels,
		OutputS3URI:          cfg.OutputS3URI,
		Resources:            cfg.Resources,
		Status:               driver.JobCompleted,
		SecondaryStatus:      driver.SecondaryCompleted,
		SecondaryTransitions: trainingTransitions(now),
		ModelArtifactS3URI:   cfg.OutputS3URI + "/output/model.tar.gz",
		CreationTime:         now,
		TrainingStartTime:    now,
		TrainingEndTime:      now,
		LastModifiedTime:     now,
		Tags:                 copyTags(cfg.Tags),
	}
	m.trainingJobs.Set(cfg.JobName, job)
	m.setTags(arn, cfg.Tags)
	m.emitJobMetric("TrainingJob")

	out := *job

	return &out, nil
}

func trainingTransitions(now string) []driver.SecondaryStatusTransition {
	stages := []string{
		driver.SecondaryStarting, driver.SecondaryDownloading,
		driver.SecondaryTraining, driver.SecondaryUploading, driver.SecondaryCompleted,
	}
	out := make([]driver.SecondaryStatusTransition, 0, len(stages))

	for _, s := range stages {
		out = append(out, driver.SecondaryStatusTransition{Status: s, StartTime: now, EndTime: now})
	}

	return out
}

// DescribeTrainingJob returns a training job by name.
func (m *Mock) DescribeTrainingJob(_ context.Context, name string) (*driver.TrainingJob, error) {
	job, ok := m.trainingJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "training job %q not found", name)
	}

	out := *job

	return &out, nil
}

// ListTrainingJobs lists all training jobs.
func (m *Mock) ListTrainingJobs(_ context.Context) ([]driver.TrainingJob, error) {
	all := m.trainingJobs.All()
	out := make([]driver.TrainingJob, 0, len(all))

	for _, j := range all {
		out = append(out, *j)
	}

	return out, nil
}

// StopTrainingJob marks a training job stopped.
func (m *Mock) StopTrainingJob(_ context.Context, name string) error {
	return stopJob(m.trainingJobs, name, "training job", func(j *driver.TrainingJob) {
		j.Status, j.SecondaryStatus = driver.JobStopped, driver.JobStopped
	})
}

// --- Processing jobs ---

//nolint:gocritic,dupl // cfg matches the driver signature; the create-job shape recurs across job kinds.
func (m *Mock) CreateProcessingJob(_ context.Context, cfg driver.ProcessingJobConfig) (*driver.ProcessingJob, error) {
	if cfg.JobName == "" {
		return nil, errors.New(errors.InvalidArgument, "processingJobName is required")
	}

	if m.processingJobs.Has(cfg.JobName) {
		return nil, errors.Newf(errors.AlreadyExists, "processing job %q already exists", cfg.JobName)
	}

	now := m.now()
	arn := m.arn("processing-job/" + cfg.JobName)
	job := &driver.ProcessingJob{
		JobName:           cfg.JobName,
		JobARN:            arn,
		RoleARN:           cfg.RoleARN,
		AppImage:          cfg.AppImage,
		Inputs:            cfg.Inputs,
		OutputS3URI:       cfg.OutputS3URI,
		Resources:         cfg.Resources,
		Status:            driver.JobCompleted,
		CreationTime:      now,
		ProcessingEndTime: now,
		LastModifiedTime:  now,
		Tags:              copyTags(cfg.Tags),
	}
	m.processingJobs.Set(cfg.JobName, job)
	m.setTags(arn, cfg.Tags)
	m.emitJobMetric("ProcessingJob")

	out := *job

	return &out, nil
}

func (m *Mock) DescribeProcessingJob(_ context.Context, name string) (*driver.ProcessingJob, error) {
	job, ok := m.processingJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "processing job %q not found", name)
	}

	out := *job

	return &out, nil
}

func (m *Mock) ListProcessingJobs(_ context.Context) ([]driver.ProcessingJob, error) {
	all := m.processingJobs.All()
	out := make([]driver.ProcessingJob, 0, len(all))

	for _, j := range all {
		out = append(out, *j)
	}

	return out, nil
}

func (m *Mock) StopProcessingJob(_ context.Context, name string) error {
	return stopJob(m.processingJobs, name, "processing job", func(j *driver.ProcessingJob) {
		j.Status = driver.JobStopped
	})
}

// --- Transform jobs ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateTransformJob(_ context.Context, cfg driver.TransformJobConfig) (*driver.TransformJob, error) {
	if cfg.JobName == "" {
		return nil, errors.New(errors.InvalidArgument, "transformJobName is required")
	}

	if cfg.ModelName == "" {
		return nil, errors.New(errors.InvalidArgument, "modelName is required")
	}

	if m.transformJobs.Has(cfg.JobName) {
		return nil, errors.Newf(errors.AlreadyExists, "transform job %q already exists", cfg.JobName)
	}

	now := m.now()
	arn := m.arn("transform-job/" + cfg.JobName)
	job := &driver.TransformJob{
		JobName:          cfg.JobName,
		JobARN:           arn,
		ModelName:        cfg.ModelName,
		InputS3URI:       cfg.InputS3URI,
		OutputS3URI:      cfg.OutputS3URI,
		InstanceType:     cfg.InstanceType,
		InstanceCount:    cfg.InstanceCount,
		Status:           driver.JobCompleted,
		CreationTime:     now,
		TransformEndTime: now,
		LastModifiedTime: now,
		Tags:             copyTags(cfg.Tags),
	}
	m.transformJobs.Set(cfg.JobName, job)
	m.setTags(arn, cfg.Tags)
	m.emitJobMetric("TransformJob")

	out := *job

	return &out, nil
}

func (m *Mock) DescribeTransformJob(_ context.Context, name string) (*driver.TransformJob, error) {
	job, ok := m.transformJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "transform job %q not found", name)
	}

	out := *job

	return &out, nil
}

func (m *Mock) ListTransformJobs(_ context.Context) ([]driver.TransformJob, error) {
	all := m.transformJobs.All()
	out := make([]driver.TransformJob, 0, len(all))

	for _, j := range all {
		out = append(out, *j)
	}

	return out, nil
}

func (m *Mock) StopTransformJob(_ context.Context, name string) error {
	return stopJob(m.transformJobs, name, "transform job", func(j *driver.TransformJob) {
		j.Status = driver.JobStopped
	})
}

// --- Hyperparameter tuning jobs ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateHyperParameterTuningJob(
	_ context.Context, cfg driver.HyperParameterTuningJobConfig,
) (*driver.HyperParameterTuningJob, error) {
	if cfg.JobName == "" {
		return nil, errors.New(errors.InvalidArgument, "hyperParameterTuningJobName is required")
	}

	if m.tuningJobs.Has(cfg.JobName) {
		return nil, errors.Newf(errors.AlreadyExists, "tuning job %q already exists", cfg.JobName)
	}

	now := m.now()
	arn := m.arn("hyper-parameter-tuning-job/" + cfg.JobName)
	best := cfg.JobName + "-001"
	job := &driver.HyperParameterTuningJob{
		JobName:           cfg.JobName,
		JobARN:            arn,
		Strategy:          orDefault(cfg.Strategy, "Bayesian"),
		MaxJobs:           cfg.MaxJobs,
		MaxParallelJobs:   cfg.MaxParallelJobs,
		ObjectiveMetric:   cfg.ObjectiveMetric,
		ObjectiveType:     orDefault(cfg.ObjectiveType, "Maximize"),
		Status:            driver.JobCompleted,
		BestTrainingJob:   best,
		TrainingJobCounts: map[string]int{"Completed": maxInt(cfg.MaxJobs, 1)},
		CreationTime:      now,
		HPOJobEndTime:     now,
		LastModifiedTime:  now,
		Tags:              copyTags(cfg.Tags),
	}
	m.tuningJobs.Set(cfg.JobName, job)
	m.setTags(arn, cfg.Tags)
	m.emitJobMetric("HyperParameterTuningJob")

	out := *job

	return &out, nil
}

func (m *Mock) DescribeHyperParameterTuningJob(_ context.Context, name string) (*driver.HyperParameterTuningJob, error) {
	job, ok := m.tuningJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "tuning job %q not found", name)
	}

	out := *job

	return &out, nil
}

func (m *Mock) ListHyperParameterTuningJobs(_ context.Context) ([]driver.HyperParameterTuningJob, error) {
	all := m.tuningJobs.All()
	out := make([]driver.HyperParameterTuningJob, 0, len(all))

	for _, j := range all {
		out = append(out, *j)
	}

	return out, nil
}

func (m *Mock) StopHyperParameterTuningJob(_ context.Context, name string) error {
	return stopJob(m.tuningJobs, name, "tuning job", func(j *driver.HyperParameterTuningJob) {
		j.Status = driver.JobStopped
	})
}

// --- AutoML jobs ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateAutoMLJobV2(_ context.Context, cfg driver.AutoMLJobConfig) (*driver.AutoMLJob, error) {
	if cfg.JobName == "" {
		return nil, errors.New(errors.InvalidArgument, "autoMLJobName is required")
	}

	if m.autoMLJobs.Has(cfg.JobName) {
		return nil, errors.Newf(errors.AlreadyExists, "AutoML job %q already exists", cfg.JobName)
	}

	now := m.now()
	arn := m.arn("automl-job/" + cfg.JobName)
	job := &driver.AutoMLJob{
		JobName:           cfg.JobName,
		JobARN:            arn,
		RoleARN:           cfg.RoleARN,
		InputS3URI:        cfg.InputS3URI,
		OutputS3URI:       cfg.OutputS3URI,
		ProblemType:       cfg.ProblemType,
		TargetColumn:      cfg.TargetColumn,
		Status:            driver.JobCompleted,
		SecondaryStatus:   "Completed",
		BestCandidateName: cfg.JobName + "-best",
		CreationTime:      now,
		AutoMLJobEndTime:  now,
		LastModifiedTime:  now,
		Tags:              copyTags(cfg.Tags),
	}
	m.autoMLJobs.Set(cfg.JobName, job)
	m.setTags(arn, cfg.Tags)
	m.emitJobMetric("AutoMLJob")

	out := *job

	return &out, nil
}

func (m *Mock) DescribeAutoMLJobV2(_ context.Context, name string) (*driver.AutoMLJob, error) {
	job, ok := m.autoMLJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "AutoML job %q not found", name)
	}

	out := *job

	return &out, nil
}

func (m *Mock) ListAutoMLJobs(_ context.Context) ([]driver.AutoMLJob, error) {
	all := m.autoMLJobs.All()
	out := make([]driver.AutoMLJob, 0, len(all))

	for _, j := range all {
		out = append(out, *j)
	}

	return out, nil
}

func (m *Mock) StopAutoMLJob(_ context.Context, name string) error {
	return stopJob(m.autoMLJobs, name, "AutoML job", func(j *driver.AutoMLJob) {
		j.Status, j.SecondaryStatus = driver.JobStopped, driver.JobStopped
	})
}

// --- Labeling jobs ---

//nolint:gocritic,dupl // cfg matches the driver signature; the create-job shape recurs across job kinds.
func (m *Mock) CreateLabelingJob(_ context.Context, cfg driver.LabelingJobConfig) (*driver.LabelingJob, error) {
	if cfg.JobName == "" {
		return nil, errors.New(errors.InvalidArgument, "labelingJobName is required")
	}

	if m.labelingJobs.Has(cfg.JobName) {
		return nil, errors.Newf(errors.AlreadyExists, "labeling job %q already exists", cfg.JobName)
	}

	now := m.now()
	arn := m.arn("labeling-job/" + cfg.JobName)
	job := &driver.LabelingJob{
		JobName:          cfg.JobName,
		JobARN:           arn,
		RoleARN:          cfg.RoleARN,
		InputS3URI:       cfg.InputS3URI,
		OutputS3URI:      cfg.OutputS3URI,
		LabelAttribute:   cfg.LabelAttribute,
		WorkteamARN:      cfg.WorkteamARN,
		Status:           driver.JobCompleted,
		CreationTime:     now,
		LabelingEndTime:  now,
		LastModifiedTime: now,
		Tags:             copyTags(cfg.Tags),
	}
	m.labelingJobs.Set(cfg.JobName, job)
	m.setTags(arn, cfg.Tags)
	m.emitJobMetric("LabelingJob")

	out := *job

	return &out, nil
}

func (m *Mock) DescribeLabelingJob(_ context.Context, name string) (*driver.LabelingJob, error) {
	job, ok := m.labelingJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "labeling job %q not found", name)
	}

	out := *job

	return &out, nil
}

func (m *Mock) ListLabelingJobs(_ context.Context) ([]driver.LabelingJob, error) {
	all := m.labelingJobs.All()
	out := make([]driver.LabelingJob, 0, len(all))

	for _, j := range all {
		out = append(out, *j)
	}

	return out, nil
}

func (m *Mock) StopLabelingJob(_ context.Context, name string) error {
	return stopJob(m.labelingJobs, name, "labeling job", func(j *driver.LabelingJob) {
		j.Status = driver.JobStopped
	})
}

// --- Compilation jobs ---

//nolint:gocritic,dupl // cfg matches the driver signature; the create-job shape recurs across job kinds.
func (m *Mock) CreateCompilationJob(_ context.Context, cfg driver.CompilationJobConfig) (*driver.CompilationJob, error) {
	if cfg.JobName == "" {
		return nil, errors.New(errors.InvalidArgument, "compilationJobName is required")
	}

	if m.compilationJobs.Has(cfg.JobName) {
		return nil, errors.Newf(errors.AlreadyExists, "compilation job %q already exists", cfg.JobName)
	}

	now := m.now()
	arn := m.arn("compilation-job/" + cfg.JobName)
	job := &driver.CompilationJob{
		JobName:            cfg.JobName,
		JobARN:             arn,
		RoleARN:            cfg.RoleARN,
		InputS3URI:         cfg.InputS3URI,
		OutputS3URI:        cfg.OutputS3URI,
		TargetDevice:       cfg.TargetDevice,
		Framework:          cfg.Framework,
		Status:             driver.CompilationCompleted,
		CreationTime:       now,
		CompilationEndTime: now,
		LastModifiedTime:   now,
		Tags:               copyTags(cfg.Tags),
	}
	m.compilationJobs.Set(cfg.JobName, job)
	m.setTags(arn, cfg.Tags)
	m.emitJobMetric("CompilationJob")

	out := *job

	return &out, nil
}

func (m *Mock) DescribeCompilationJob(_ context.Context, name string) (*driver.CompilationJob, error) {
	job, ok := m.compilationJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "compilation job %q not found", name)
	}

	out := *job

	return &out, nil
}

func (m *Mock) ListCompilationJobs(_ context.Context) ([]driver.CompilationJob, error) {
	all := m.compilationJobs.All()
	out := make([]driver.CompilationJob, 0, len(all))

	for _, j := range all {
		out = append(out, *j)
	}

	return out, nil
}

func (m *Mock) StopCompilationJob(_ context.Context, name string) error {
	return stopJob(m.compilationJobs, name, "compilation job", func(j *driver.CompilationJob) {
		j.Status = driver.CompilationStopped
	})
}

// --- shared helpers ---

// stopJob applies mutate to a COPY of a stored job and re-stores it, or returns
// NotFound. Copy-then-Set keeps concurrent Describe*/List* readers race-free:
// they observe either the old or the new pointer, never an in-place write.
func stopJob[T any](store *memstore.Store[*T], name, kind string, mutate func(*T)) error {
	job, ok := store.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "%s %q not found", kind, name)
	}

	updated := *job
	mutate(&updated)
	store.Set(name, &updated)

	return nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}
