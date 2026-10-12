package sagemaker

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// jobNamePattern is the name rule shared by every SageMaker job: 1-63
// characters, alphanumerics and hyphens, not starting with a hyphen.
var jobNamePattern = regexp.MustCompile(`^[a-zA-Z0-9](-*[a-zA-Z0-9]){0,62}$`)

// trainingStartStage is the index of the Training stage in trainingStages.
const trainingStartStage = 2

// validateJobName rejects an empty or malformed job name.
func validateJobName(field, name string) error {
	if name == "" {
		return errors.Newf(errors.InvalidArgument, "%s is required", field)
	}

	if !jobNamePattern.MatchString(name) {
		return errors.Newf(errors.InvalidArgument,
			"Value '%s' at '%s' failed to satisfy constraint: Member must satisfy regular expression pattern: "+
				"^[a-zA-Z0-9](-*[a-zA-Z0-9]){0,62}", name, field)
	}

	return nil
}

// cloneEcho copies an echo map (the raw JSON values are immutable).
func cloneEcho(in map[string]json.RawMessage) map[string]json.RawMessage {
	if in == nil {
		return nil
	}

	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyChannels(in []driver.Channel) []driver.Channel {
	if in == nil {
		return nil
	}

	return append([]driver.Channel(nil), in...)
}

// --- Training jobs ---

// CreateTrainingJob creates a training job. Without --async-settle it is
// Completed immediately (with a full, distinctly-timestamped status history);
// with it the job moves through Starting, Downloading, Training and Uploading.
//
//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateTrainingJob(ctx context.Context, cfg driver.TrainingJobConfig) (*driver.TrainingJob, error) {
	if err := validateJobName("trainingJobName", cfg.JobName); err != nil {
		return nil, err
	}

	now := m.now()
	arn := m.arn("training-job/" + cfg.JobName)
	job := &driver.TrainingJob{
		JobName:            cfg.JobName,
		JobARN:             arn,
		RoleARN:            cfg.RoleARN,
		AlgorithmImage:     cfg.AlgorithmImage,
		HyperParameters:    copyStrMap(cfg.HyperParameters),
		InputChannels:      copyChannels(cfg.InputChannels),
		OutputS3URI:        cfg.OutputS3URI,
		Resources:          cfg.Resources,
		Status:             driver.JobCompleted,
		SecondaryStatus:    driver.SecondaryCompleted,
		ModelArtifactS3URI: cfg.OutputS3URI + "/output/model.tar.gz",
		CreationTime:       now,
		LastModifiedTime:   now,
		Tags:               copyTags(cfg.Tags),
		Echo:               cloneEcho(cfg.Echo),
	}

	if !m.trainingJobs.SetIfAbsent(cfg.JobName, job) {
		return nil, errors.Newf(errors.AlreadyExists, "training job %q already exists", cfg.JobName)
	}

	m.setTags(arn, cfg.Tags)
	m.emitJobUtilization("Training", cfg.JobName, cfg.Resources, now)

	out := m.observeTrainingJob(job)
	m.emitTrainingEvents(ctx, out, driver.JobInProgress, driver.JobCompleted)

	return out, nil
}

// observeTrainingJob returns a deep copy of a stored training job with its
// status, history and times as observed now.
func (m *Mock) observeTrainingJob(j *driver.TrainingJob) *driver.TrainingJob {
	out := *j
	out.HyperParameters = copyStrMap(j.HyperParameters)
	out.InputChannels = copyChannels(j.InputChannels)
	out.Tags = copyTags(j.Tags)
	out.Echo = cloneEcho(j.Echo)

	stages := trainingStages()
	p := m.project(j.CreationTime, j.StopRequested, len(stages))

	out.Status = p.primary(stages, mixedCaseStop)
	out.SecondaryStatus = p.secondary(stages)
	out.SecondaryTransitions = p.transitions(stages)
	out.LastModifiedTime = fmtTime(p.modified)
	out.TrainingStartTime, out.TrainingEndTime = "", ""
	out.TrainingTimeSeconds, out.BillableTimeSeconds = 0, 0

	if p.stage >= trainingStartStage {
		out.TrainingStartTime = fmtTime(p.starts[trainingStartStage])
	}

	if p.terminal() {
		out.TrainingEndTime = fmtTime(p.ended)
	}

	if p.terminal() && out.TrainingStartTime != "" {
		secs := maxInt(1, int(p.ended.Sub(p.starts[trainingStartStage]).Round(secondRound).Seconds()))
		out.TrainingTimeSeconds, out.BillableTimeSeconds = secs, secs
	}

	return &out
}

func (m *Mock) DescribeTrainingJob(_ context.Context, name string) (*driver.TrainingJob, error) {
	job, ok := m.trainingJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "training job %q not found", name)
	}

	return m.observeTrainingJob(job), nil
}

// ListTrainingJobs lists training jobs; filters and sort apply to each job's
// observed state.
//
//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape
func (m *Mock) ListTrainingJobs(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.TrainingJob], error) {
	return listObserved(m.trainingJobs, m.observeTrainingJob, listSpec[driver.TrainingJob]{
		name:     func(t *driver.TrainingJob) string { return t.JobName },
		status:   func(t *driver.TrainingJob) string { return t.Status },
		created:  func(t *driver.TrainingJob) string { return t.CreationTime },
		modified: func(t *driver.TrainingJob) string { return t.LastModifiedTime },
	}, opts)
}

// StopTrainingJob requests a stop. See stopJob for the semantics.
func (m *Mock) StopTrainingJob(ctx context.Context, name string) error {
	accepted, err := stopJobAccepted(m, m.trainingJobs, name, "training job", stopAccess[driver.TrainingJob]{
		created:       func(j *driver.TrainingJob) string { return j.CreationTime },
		stopRequested: func(j *driver.TrainingJob) *string { return &j.StopRequested },
		stages:        trainingStages(),
	})
	if err != nil || !accepted {
		return err
	}

	if job, ok := m.trainingJobs.Get(name); ok {
		m.emitTrainingEvents(ctx, m.observeTrainingJob(job), driver.JobStopping, driver.JobStopped)
	}

	return nil
}

// --- Processing jobs ---

//nolint:gocritic,dupl // cfg matches the driver signature; the create-job shape recurs across job kinds.
func (m *Mock) CreateProcessingJob(ctx context.Context, cfg driver.ProcessingJobConfig) (*driver.ProcessingJob, error) {
	if err := validateJobName("processingJobName", cfg.JobName); err != nil {
		return nil, err
	}

	now := m.now()
	arn := m.arn("processing-job/" + cfg.JobName)
	job := &driver.ProcessingJob{
		JobName:          cfg.JobName,
		JobARN:           arn,
		RoleARN:          cfg.RoleARN,
		AppImage:         cfg.AppImage,
		Inputs:           copyChannels(cfg.Inputs),
		OutputS3URI:      cfg.OutputS3URI,
		Resources:        cfg.Resources,
		Status:           driver.JobCompleted,
		CreationTime:     now,
		LastModifiedTime: now,
		Tags:             copyTags(cfg.Tags),
		Echo:             cloneEcho(cfg.Echo),
	}

	if !m.processingJobs.SetIfAbsent(cfg.JobName, job) {
		return nil, errors.Newf(errors.AlreadyExists, "processing job %q already exists", cfg.JobName)
	}

	m.setTags(arn, cfg.Tags)
	m.emitJobUtilization("Processing", cfg.JobName, cfg.Resources, now)

	out := m.observeProcessingJob(job)
	m.emitProcessingEvents(ctx, out, driver.JobInProgress, driver.JobCompleted)

	return out, nil
}

func (m *Mock) observeProcessingJob(j *driver.ProcessingJob) *driver.ProcessingJob {
	out := *j
	out.Inputs = copyChannels(j.Inputs)
	out.Tags = copyTags(j.Tags)
	out.Echo = cloneEcho(j.Echo)

	stages := simpleStages()
	p := m.project(j.CreationTime, j.StopRequested, len(stages))
	out.Status = p.primary(stages, mixedCaseStop)
	out.LastModifiedTime = fmtTime(p.modified)
	out.ProcessingEndTime = endTime(p)

	return &out
}

// endTime is the end of a terminal projection, or "" while it is running.
//
//nolint:gocritic // driver-signature params and SDK-shaped helpers
func endTime(p projection) string {
	if p.terminal() {
		return fmtTime(p.ended)
	}

	return ""
}

func (m *Mock) DescribeProcessingJob(_ context.Context, name string) (*driver.ProcessingJob, error) {
	job, ok := m.processingJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "processing job %q not found", name)
	}

	return m.observeProcessingJob(job), nil
}

//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape
func (m *Mock) ListProcessingJobs(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.ProcessingJob], error) {
	return listObserved(m.processingJobs, m.observeProcessingJob, listSpec[driver.ProcessingJob]{
		name:     func(t *driver.ProcessingJob) string { return t.JobName },
		status:   func(t *driver.ProcessingJob) string { return t.Status },
		created:  func(t *driver.ProcessingJob) string { return t.CreationTime },
		modified: func(t *driver.ProcessingJob) string { return t.LastModifiedTime },
	}, opts)
}

func (m *Mock) StopProcessingJob(ctx context.Context, name string) error {
	accepted, err := stopJobAccepted(m, m.processingJobs, name, "processing job", stopAccess[driver.ProcessingJob]{
		created:       func(j *driver.ProcessingJob) string { return j.CreationTime },
		stopRequested: func(j *driver.ProcessingJob) *string { return &j.StopRequested },
		stages:        simpleStages(),
	})
	if err != nil || !accepted {
		return err
	}

	if job, ok := m.processingJobs.Get(name); ok {
		m.emitProcessingEvents(ctx, m.observeProcessingJob(job), driver.JobStopping, driver.JobStopped)
	}

	return nil
}

// --- Transform jobs ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateTransformJob(ctx context.Context, cfg driver.TransformJobConfig) (*driver.TransformJob, error) {
	if err := validateJobName("transformJobName", cfg.JobName); err != nil {
		return nil, err
	}

	if cfg.ModelName == "" {
		return nil, errors.New(errors.InvalidArgument, "modelName is required")
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
		LastModifiedTime: now,
		Tags:             copyTags(cfg.Tags),
		Echo:             cloneEcho(cfg.Echo),
	}

	if !m.transformJobs.SetIfAbsent(cfg.JobName, job) {
		return nil, errors.Newf(errors.AlreadyExists, "transform job %q already exists", cfg.JobName)
	}

	m.setTags(arn, cfg.Tags)
	m.emitJobUtilization("Transform", cfg.JobName,
		driver.ResourceConfig{InstanceType: cfg.InstanceType, InstanceCount: cfg.InstanceCount}, now)

	out := m.observeTransformJob(job)
	m.emitTransformEvents(ctx, out, driver.JobInProgress, driver.JobCompleted)

	return out, nil
}

func (m *Mock) observeTransformJob(j *driver.TransformJob) *driver.TransformJob {
	out := *j
	out.Tags = copyTags(j.Tags)
	out.Echo = cloneEcho(j.Echo)

	stages := simpleStages()
	p := m.project(j.CreationTime, j.StopRequested, len(stages))
	out.Status = p.primary(stages, mixedCaseStop)
	out.LastModifiedTime = fmtTime(p.modified)
	out.TransformEndTime = endTime(p)

	return &out
}

func (m *Mock) DescribeTransformJob(_ context.Context, name string) (*driver.TransformJob, error) {
	job, ok := m.transformJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "transform job %q not found", name)
	}

	return m.observeTransformJob(job), nil
}

//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape
func (m *Mock) ListTransformJobs(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.TransformJob], error) {
	return listObserved(m.transformJobs, m.observeTransformJob, listSpec[driver.TransformJob]{
		name:     func(t *driver.TransformJob) string { return t.JobName },
		status:   func(t *driver.TransformJob) string { return t.Status },
		created:  func(t *driver.TransformJob) string { return t.CreationTime },
		modified: func(t *driver.TransformJob) string { return t.LastModifiedTime },
	}, opts)
}

func (m *Mock) StopTransformJob(ctx context.Context, name string) error {
	accepted, err := stopJobAccepted(m, m.transformJobs, name, "transform job", stopAccess[driver.TransformJob]{
		created:       func(j *driver.TransformJob) string { return j.CreationTime },
		stopRequested: func(j *driver.TransformJob) *string { return &j.StopRequested },
		stages:        simpleStages(),
	})
	if err != nil || !accepted {
		return err
	}

	if job, ok := m.transformJobs.Get(name); ok {
		m.emitTransformEvents(ctx, m.observeTransformJob(job), driver.JobStopping, driver.JobStopped)
	}

	return nil
}

// --- Hyperparameter tuning jobs ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateHyperParameterTuningJob(
	ctx context.Context, cfg driver.HyperParameterTuningJobConfig,
) (*driver.HyperParameterTuningJob, error) {
	if err := validateJobName("hyperParameterTuningJobName", cfg.JobName); err != nil {
		return nil, err
	}

	now := m.now()
	arn := m.arn("hyper-parameter-tuning-job/" + cfg.JobName)
	job := &driver.HyperParameterTuningJob{
		JobName:          cfg.JobName,
		JobARN:           arn,
		Strategy:         orDefault(cfg.Strategy, "Bayesian"),
		MaxJobs:          cfg.MaxJobs,
		MaxParallelJobs:  cfg.MaxParallelJobs,
		ObjectiveMetric:  cfg.ObjectiveMetric,
		ObjectiveType:    orDefault(cfg.ObjectiveType, "Maximize"),
		Status:           driver.JobCompleted,
		CreationTime:     now,
		LastModifiedTime: now,
		Tags:             copyTags(cfg.Tags),
		Echo:             cloneEcho(cfg.Echo),
	}

	if !m.tuningJobs.SetIfAbsent(cfg.JobName, job) {
		return nil, errors.Newf(errors.AlreadyExists, "tuning job %q already exists", cfg.JobName)
	}

	m.setTags(arn, cfg.Tags)

	out := m.observeTuningJob(job)
	m.emitTuningEvents(ctx, out, driver.JobInProgress, driver.JobCompleted)

	return out, nil
}

func (m *Mock) observeTuningJob(j *driver.HyperParameterTuningJob) *driver.HyperParameterTuningJob {
	out := *j
	out.Tags = copyTags(j.Tags)
	out.Echo = cloneEcho(j.Echo)

	stages := simpleStages()
	p := m.project(j.CreationTime, j.StopRequested, len(stages))
	out.Status = p.primary(stages, mixedCaseStop)
	out.LastModifiedTime = fmtTime(p.modified)
	out.HPOJobEndTime = endTime(p)
	out.BestTrainingJob = ""
	out.TrainingJobCounts = map[string]int{}

	total := maxInt(j.MaxJobs, 1)

	switch p.state {
	case jobCompleted:
		out.BestTrainingJob = j.JobName + "-001"
		out.TrainingJobCounts["Completed"] = total
	case jobStopped, jobStopping:
		out.TrainingJobCounts["Stopped"] = total
	default:
		out.TrainingJobCounts["InProgress"] = minInt(maxInt(j.MaxParallelJobs, 1), total)
	}

	return &out
}

func (m *Mock) DescribeHyperParameterTuningJob(_ context.Context, name string) (*driver.HyperParameterTuningJob, error) {
	job, ok := m.tuningJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "tuning job %q not found", name)
	}

	return m.observeTuningJob(job), nil
}

//nolint:dupl,gocritic,lll // per-resource shim mirrors the SDK shape
func (m *Mock) ListHyperParameterTuningJobs(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.HyperParameterTuningJob], error) {
	return listObserved(m.tuningJobs, m.observeTuningJob, listSpec[driver.HyperParameterTuningJob]{
		name:     func(t *driver.HyperParameterTuningJob) string { return t.JobName },
		status:   func(t *driver.HyperParameterTuningJob) string { return t.Status },
		created:  func(t *driver.HyperParameterTuningJob) string { return t.CreationTime },
		modified: func(t *driver.HyperParameterTuningJob) string { return t.LastModifiedTime },
	}, opts)
}

func (m *Mock) StopHyperParameterTuningJob(ctx context.Context, name string) error {
	accepted, err := stopJobAccepted(m, m.tuningJobs, name, "tuning job", stopAccess[driver.HyperParameterTuningJob]{
		created:       func(j *driver.HyperParameterTuningJob) string { return j.CreationTime },
		stopRequested: func(j *driver.HyperParameterTuningJob) *string { return &j.StopRequested },
		stages:        simpleStages(),
	})
	if err != nil || !accepted {
		return err
	}

	if job, ok := m.tuningJobs.Get(name); ok {
		m.emitTuningEvents(ctx, m.observeTuningJob(job), driver.JobStopping, driver.JobStopped)
	}

	return nil
}

// --- AutoML jobs ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateAutoMLJobV2(_ context.Context, cfg driver.AutoMLJobConfig) (*driver.AutoMLJob, error) {
	if err := validateJobName("autoMLJobName", cfg.JobName); err != nil {
		return nil, err
	}

	now := m.now()
	arn := m.arn("automl-job/" + cfg.JobName)
	job := &driver.AutoMLJob{
		JobName:          cfg.JobName,
		JobARN:           arn,
		RoleARN:          cfg.RoleARN,
		InputS3URI:       cfg.InputS3URI,
		OutputS3URI:      cfg.OutputS3URI,
		ProblemType:      cfg.ProblemType,
		TargetColumn:     cfg.TargetColumn,
		Status:           driver.JobCompleted,
		SecondaryStatus:  driver.SecondaryCompleted,
		CreationTime:     now,
		LastModifiedTime: now,
		Tags:             copyTags(cfg.Tags),
		Echo:             cloneEcho(cfg.Echo),
	}

	if !m.autoMLJobs.SetIfAbsent(cfg.JobName, job) {
		return nil, errors.Newf(errors.AlreadyExists, "AutoML job %q already exists", cfg.JobName)
	}

	m.setTags(arn, cfg.Tags)

	return m.observeAutoMLJob(job), nil
}

func (m *Mock) observeAutoMLJob(j *driver.AutoMLJob) *driver.AutoMLJob {
	out := *j
	out.Tags = copyTags(j.Tags)
	out.Echo = cloneEcho(j.Echo)

	stages := autoMLStages()
	p := m.project(j.CreationTime, j.StopRequested, len(stages))
	out.Status = p.primary(stages, mixedCaseStop)
	out.SecondaryStatus = p.secondary(stages)
	out.LastModifiedTime = fmtTime(p.modified)
	out.AutoMLJobEndTime = endTime(p)
	out.BestCandidateName = ""

	if p.state == jobCompleted {
		out.BestCandidateName = j.JobName + "-best"
	}

	return &out
}

func (m *Mock) DescribeAutoMLJobV2(_ context.Context, name string) (*driver.AutoMLJob, error) {
	job, ok := m.autoMLJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "AutoML job %q not found", name)
	}

	return m.observeAutoMLJob(job), nil
}

//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape
func (m *Mock) ListAutoMLJobs(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.AutoMLJob], error) {
	return listObserved(m.autoMLJobs, m.observeAutoMLJob, listSpec[driver.AutoMLJob]{
		name:     func(t *driver.AutoMLJob) string { return t.JobName },
		status:   func(t *driver.AutoMLJob) string { return t.Status },
		created:  func(t *driver.AutoMLJob) string { return t.CreationTime },
		modified: func(t *driver.AutoMLJob) string { return t.LastModifiedTime },
	}, opts)
}

func (m *Mock) StopAutoMLJob(_ context.Context, name string) error {
	return stopJob(m, m.autoMLJobs, name, "AutoML job", stopAccess[driver.AutoMLJob]{
		created:       func(j *driver.AutoMLJob) string { return j.CreationTime },
		stopRequested: func(j *driver.AutoMLJob) *string { return &j.StopRequested },
		stages:        autoMLStages(),
	})
}

// --- Labeling jobs ---

//nolint:gocritic,dupl // cfg matches the driver signature; the create-job shape recurs across job kinds.
func (m *Mock) CreateLabelingJob(_ context.Context, cfg driver.LabelingJobConfig) (*driver.LabelingJob, error) {
	if err := validateJobName("labelingJobName", cfg.JobName); err != nil {
		return nil, err
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
		LastModifiedTime: now,
		Tags:             copyTags(cfg.Tags),
		Echo:             cloneEcho(cfg.Echo),
	}

	if !m.labelingJobs.SetIfAbsent(cfg.JobName, job) {
		return nil, errors.Newf(errors.AlreadyExists, "labeling job %q already exists", cfg.JobName)
	}

	m.setTags(arn, cfg.Tags)

	return m.observeLabelingJob(job), nil
}

func (m *Mock) observeLabelingJob(j *driver.LabelingJob) *driver.LabelingJob {
	out := *j
	out.Tags = copyTags(j.Tags)
	out.Echo = cloneEcho(j.Echo)

	stages := labelingStages()
	p := m.project(j.CreationTime, j.StopRequested, len(stages))
	out.Status = p.primary(stages, mixedCaseStop)
	out.LastModifiedTime = fmtTime(p.modified)
	out.LabelingEndTime = endTime(p)

	return &out
}

func (m *Mock) DescribeLabelingJob(_ context.Context, name string) (*driver.LabelingJob, error) {
	job, ok := m.labelingJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "labeling job %q not found", name)
	}

	return m.observeLabelingJob(job), nil
}

//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape
func (m *Mock) ListLabelingJobs(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.LabelingJob], error) {
	return listObserved(m.labelingJobs, m.observeLabelingJob, listSpec[driver.LabelingJob]{
		name:     func(t *driver.LabelingJob) string { return t.JobName },
		status:   func(t *driver.LabelingJob) string { return t.Status },
		created:  func(t *driver.LabelingJob) string { return t.CreationTime },
		modified: func(t *driver.LabelingJob) string { return t.LastModifiedTime },
	}, opts)
}

func (m *Mock) StopLabelingJob(_ context.Context, name string) error {
	return stopJob(m, m.labelingJobs, name, "labeling job", stopAccess[driver.LabelingJob]{
		created:       func(j *driver.LabelingJob) string { return j.CreationTime },
		stopRequested: func(j *driver.LabelingJob) *string { return &j.StopRequested },
		stages:        labelingStages(),
	})
}

// --- Compilation jobs ---

//nolint:gocritic,dupl // cfg matches the driver signature; the create-job shape recurs across job kinds.
func (m *Mock) CreateCompilationJob(_ context.Context, cfg driver.CompilationJobConfig) (*driver.CompilationJob, error) {
	if err := validateJobName("compilationJobName", cfg.JobName); err != nil {
		return nil, err
	}

	now := m.now()
	arn := m.arn("compilation-job/" + cfg.JobName)
	job := &driver.CompilationJob{
		JobName:          cfg.JobName,
		JobARN:           arn,
		RoleARN:          cfg.RoleARN,
		InputS3URI:       cfg.InputS3URI,
		OutputS3URI:      cfg.OutputS3URI,
		TargetDevice:     cfg.TargetDevice,
		Framework:        cfg.Framework,
		Status:           driver.CompilationCompleted,
		CreationTime:     now,
		LastModifiedTime: now,
		Tags:             copyTags(cfg.Tags),
		Echo:             cloneEcho(cfg.Echo),
	}

	if !m.compilationJobs.SetIfAbsent(cfg.JobName, job) {
		return nil, errors.Newf(errors.AlreadyExists, "compilation job %q already exists", cfg.JobName)
	}

	m.setTags(arn, cfg.Tags)

	return m.observeCompilationJob(job), nil
}

func (m *Mock) observeCompilationJob(j *driver.CompilationJob) *driver.CompilationJob {
	out := *j
	out.Tags = copyTags(j.Tags)
	out.Echo = cloneEcho(j.Echo)

	stages := compilationStages()
	p := m.project(j.CreationTime, j.StopRequested, len(stages))
	out.Status = p.primary(stages, upperCaseStop)
	out.LastModifiedTime = fmtTime(p.modified)
	out.CompilationEndTime = endTime(p)

	return &out
}

func (m *Mock) DescribeCompilationJob(_ context.Context, name string) (*driver.CompilationJob, error) {
	job, ok := m.compilationJobs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "compilation job %q not found", name)
	}

	return m.observeCompilationJob(job), nil
}

//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape
func (m *Mock) ListCompilationJobs(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.CompilationJob], error) {
	return listObserved(m.compilationJobs, m.observeCompilationJob, listSpec[driver.CompilationJob]{
		name:     func(t *driver.CompilationJob) string { return t.JobName },
		status:   func(t *driver.CompilationJob) string { return t.Status },
		created:  func(t *driver.CompilationJob) string { return t.CreationTime },
		modified: func(t *driver.CompilationJob) string { return t.LastModifiedTime },
	}, opts)
}

func (m *Mock) StopCompilationJob(_ context.Context, name string) error {
	return stopJob(m, m.compilationJobs, name, "compilation job", stopAccess[driver.CompilationJob]{
		created:       func(j *driver.CompilationJob) string { return j.CreationTime },
		stopRequested: func(j *driver.CompilationJob) *string { return &j.StopRequested },
		stages:        compilationStages(),
	})
}

// --- shared helpers ---

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

func minInt(a, b int) int {
	if a < b {
		return a
	}

	return b
}
