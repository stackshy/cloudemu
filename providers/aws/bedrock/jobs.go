package bedrock

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// --- Model import jobs ---

// CreateModelImportJob starts a custom-model import job. It completes
// synchronously: the job is recorded already Completed with a materialized
// imported-model ARN.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateModelImportJob(_ context.Context, cfg driver.ModelImportJobConfig) (*driver.ModelImportJob, error) {
	switch {
	case cfg.JobName == "":
		return nil, errors.New(errors.InvalidArgument, "jobName is required")
	case cfg.ImportedModelName == "":
		return nil, errors.New(errors.InvalidArgument, "importedModelName is required")
	case cfg.RoleARN == "":
		return nil, errors.New(errors.InvalidArgument, "roleArn is required")
	case cfg.ModelDataSourceS3URI == "":
		return nil, errors.New(errors.InvalidArgument, "modelDataSource.s3DataSource.s3Uri is required")
	}

	if m.importJobs.Has(cfg.JobName) {
		return nil, errors.Newf(errors.AlreadyExists, "model import job %q already exists", cfg.JobName)
	}

	now := m.now()
	jobARN := idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "model-import-job/"+idgen.GenerateID(""))
	modelARN := idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "imported-model/"+cfg.ImportedModelName)

	job := &driver.ModelImportJob{
		JobARN:               jobARN,
		JobName:              cfg.JobName,
		ImportedModelName:    cfg.ImportedModelName,
		ImportedModelARN:     modelARN,
		RoleARN:              cfg.RoleARN,
		ModelDataSourceS3URI: cfg.ModelDataSourceS3URI,
		Status:               driver.JobCompleted,
		CreationTime:         now,
		LastModifiedTime:     now,
		EndTime:              now,
	}
	m.importJobs.Set(cfg.JobName, job)
	m.setTags(jobARN, m.tagsFromMap(cfg.JobTags))
	m.setTags(modelARN, m.tagsFromMap(cfg.ImportedModelTags))

	result := *job

	return &result, nil
}

// GetModelImportJob returns an import job by name or ARN.
func (m *Mock) GetModelImportJob(_ context.Context, jobIdentifier string) (*driver.ModelImportJob, error) {
	if job, ok := m.importJobs.Get(jobIdentifier); ok {
		result := *job

		return &result, nil
	}

	for _, job := range m.importJobs.All() {
		if job.JobARN == jobIdentifier {
			result := *job

			return &result, nil
		}
	}

	return nil, errors.Newf(errors.NotFound, "model import job %q not found", jobIdentifier)
}

// ListModelImportJobs lists all import jobs.
func (m *Mock) ListModelImportJobs(_ context.Context) ([]driver.ModelImportJob, error) {
	all := m.importJobs.SortedValues()
	out := make([]driver.ModelImportJob, 0, len(all))

	for _, job := range all {
		out = append(out, *job)
	}

	return out, nil
}

// --- Model copy jobs ---

// CreateModelCopyJob starts a model-copy job. It completes synchronously: the
// job is recorded already Completed with a materialized target-model ARN.
func (m *Mock) CreateModelCopyJob(_ context.Context, cfg driver.ModelCopyJobConfig) (*driver.ModelCopyJob, error) {
	switch {
	case cfg.SourceModelARN == "":
		return nil, errors.New(errors.InvalidArgument, "sourceModelArn is required")
	case cfg.TargetModelName == "":
		return nil, errors.New(errors.InvalidArgument, "targetModelName is required")
	}

	now := m.now()
	jobARN := idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "model-copy-job/"+idgen.GenerateID(""))
	targetARN := idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "model-copy-target/"+cfg.TargetModelName)

	job := &driver.ModelCopyJob{
		JobARN:               jobARN,
		SourceAccountID:      m.opts.AccountID,
		SourceModelARN:       cfg.SourceModelARN,
		TargetModelName:      cfg.TargetModelName,
		TargetModelARN:       targetARN,
		TargetModelKMSKeyARN: cfg.ModelKMSKeyID,
		Status:               driver.JobCompleted,
		CreationTime:         now,
	}
	m.copyJobs.Set(jobARN, job)
	m.setTags(targetARN, m.tagsFromMap(cfg.TargetModelTags))

	result := *job

	return &result, nil
}

// GetModelCopyJob returns a copy job by its job ARN.
func (m *Mock) GetModelCopyJob(_ context.Context, jobARN string) (*driver.ModelCopyJob, error) {
	job, ok := m.copyJobs.Get(jobARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "model copy job %q not found", jobARN)
	}

	result := *job

	return &result, nil
}

// ListModelCopyJobs lists all copy jobs.
func (m *Mock) ListModelCopyJobs(_ context.Context) ([]driver.ModelCopyJob, error) {
	all := m.copyJobs.SortedValues()
	out := make([]driver.ModelCopyJob, 0, len(all))

	for _, job := range all {
		out = append(out, *job)
	}

	return out, nil
}

// --- Evaluation jobs ---

// CreateEvaluationJob starts a model-evaluation job. It completes
// synchronously: the job is recorded already Completed.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateEvaluationJob(_ context.Context, cfg driver.EvaluationJobConfig) (*driver.EvaluationJob, error) {
	switch {
	case cfg.JobName == "":
		return nil, errors.New(errors.InvalidArgument, "jobName is required")
	case cfg.RoleARN == "":
		return nil, errors.New(errors.InvalidArgument, "roleArn is required")
	case len(cfg.EvaluationConfig) == 0:
		return nil, errors.New(errors.InvalidArgument, "evaluationConfig is required")
	case len(cfg.InferenceConfig) == 0:
		return nil, errors.New(errors.InvalidArgument, "inferenceConfig is required")
	case cfg.OutputDataS3URI == "":
		return nil, errors.New(errors.InvalidArgument, "outputDataConfig.s3Uri is required")
	}

	if m.evalJobs.Has(cfg.JobName) {
		return nil, errors.Newf(errors.AlreadyExists, "evaluation job %q already exists", cfg.JobName)
	}

	now := m.now()
	jobARN := idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "evaluation-job/"+idgen.GenerateID(""))

	job := &driver.EvaluationJob{
		JobARN:                  jobARN,
		JobName:                 cfg.JobName,
		JobType:                 evaluationJobType(cfg.EvaluationConfig),
		ApplicationType:         cfg.ApplicationType,
		RoleARN:                 cfg.RoleARN,
		EvaluationConfig:        copyBytes(cfg.EvaluationConfig),
		InferenceConfig:         copyBytes(cfg.InferenceConfig),
		OutputDataS3URI:         cfg.OutputDataS3URI,
		JobDescription:          cfg.JobDescription,
		CustomerEncryptionKeyID: cfg.CustomerEncryptionKeyID,
		// Unlike import/copy jobs (which produce an artifact synchronously),
		// evaluation is long-running, so it starts InProgress and stays there
		// until StopEvaluationJob transitions it — making Stop a meaningful op.
		Status:           driver.JobInProgress,
		CreationTime:     now,
		LastModifiedTime: now,
	}
	m.evalJobs.Set(cfg.JobName, job)
	m.setTags(jobARN, m.tagsFromMap(cfg.JobTags))

	result := *job

	return &result, nil
}

// GetEvaluationJob returns an evaluation job by name or ARN.
func (m *Mock) GetEvaluationJob(_ context.Context, jobIdentifier string) (*driver.EvaluationJob, error) {
	job := m.findEvalJob(jobIdentifier)
	if job == nil {
		return nil, errors.Newf(errors.NotFound, "evaluation job %q not found", jobIdentifier)
	}

	result := cloneEvaluationJob(job)

	return &result, nil
}

// ListEvaluationJobs lists all evaluation jobs.
func (m *Mock) ListEvaluationJobs(_ context.Context) ([]driver.EvaluationJob, error) {
	all := m.evalJobs.SortedValues()
	out := make([]driver.EvaluationJob, 0, len(all))

	for _, job := range all {
		out = append(out, cloneEvaluationJob(job))
	}

	return out, nil
}

// StopEvaluationJob transitions an evaluation job to the Stopped state.
func (m *Mock) StopEvaluationJob(_ context.Context, jobIdentifier string) error {
	job := m.findEvalJob(jobIdentifier)
	if job == nil {
		return errors.Newf(errors.NotFound, "evaluation job %q not found", jobIdentifier)
	}

	// Real AWS rejects stopping a job that is no longer in progress with a
	// ConflictException.
	if job.Status != driver.JobInProgress {
		return errors.Newf(errors.FailedPrecondition,
			"evaluation job %q is in terminal state %q and cannot be stopped", jobIdentifier, job.Status)
	}

	// Copy-on-write: never mutate the stored pointer in place, so concurrent
	// Get/List readers (which copy the stored value) can't race the write.
	updated := *job
	updated.Status = driver.JobStopped
	updated.LastModifiedTime = m.now()
	m.evalJobs.Set(updated.JobName, &updated)

	return nil
}

func (m *Mock) findEvalJob(id string) *driver.EvaluationJob {
	if job, ok := m.evalJobs.Get(id); ok {
		return job
	}

	for _, job := range m.evalJobs.All() {
		if job.JobARN == id {
			return job
		}
	}

	return nil
}

// cloneEvaluationJob returns a value copy whose EvaluationConfig and
// InferenceConfig do not alias the stored job, so callers can't mutate internal
// state via the result.
func cloneEvaluationJob(j *driver.EvaluationJob) driver.EvaluationJob {
	out := *j
	out.EvaluationConfig = copyBytes(j.EvaluationConfig)
	out.InferenceConfig = copyBytes(j.InferenceConfig)

	return out
}

// evaluationJobType derives the job type from the evaluationConfig document: a
// "human" member yields Human, otherwise Automated.
func evaluationJobType(cfg []byte) string {
	var probe map[string]json.RawMessage
	if json.Unmarshal(cfg, &probe) == nil {
		if _, ok := probe["human"]; ok {
			return driver.EvaluationTypeHuman
		}
	}

	return driver.EvaluationTypeAutomated
}
