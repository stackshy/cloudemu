package bedrock

import (
	"encoding/json"
	"net/http"
	"strings"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

// --- shared wire types ---

type s3DataSourceJSON struct {
	S3URI string `json:"s3Uri,omitempty"`
}

type modelDataSourceJSON struct {
	S3DataSource *s3DataSourceJSON `json:"s3DataSource,omitempty"`
}

type evalOutputDataConfigJSON struct {
	S3URI string `json:"s3Uri,omitempty"`
}

type createJobArnResponse struct {
	JobARN string `json:"jobArn"`
}

// --- model import job wire types ---

type createImportJobRequest struct {
	JobName               string               `json:"jobName"`
	ImportedModelName     string               `json:"importedModelName"`
	RoleARN               string               `json:"roleArn"`
	ModelDataSource       *modelDataSourceJSON `json:"modelDataSource"`
	ClientRequestToken    string               `json:"clientRequestToken"`
	ImportedModelKMSKeyID string               `json:"importedModelKmsKeyId"`
	JobTags               []tagPair            `json:"jobTags"`
	ImportedModelTags     []tagPair            `json:"importedModelTags"`
}

type importJobJSON struct {
	CreationTime      string               `json:"creationTime,omitempty"`
	EndTime           string               `json:"endTime,omitempty"`
	ImportedModelARN  string               `json:"importedModelArn,omitempty"`
	ImportedModelName string               `json:"importedModelName,omitempty"`
	JobARN            string               `json:"jobArn"`
	JobName           string               `json:"jobName"`
	ModelDataSource   *modelDataSourceJSON `json:"modelDataSource,omitempty"`
	RoleARN           string               `json:"roleArn,omitempty"`
	Status            string               `json:"status"`
	LastModifiedTime  string               `json:"lastModifiedTime,omitempty"`
	FailureMessage    string               `json:"failureMessage,omitempty"`
}

type importJobSummaryJSON struct {
	CreationTime      string `json:"creationTime,omitempty"`
	EndTime           string `json:"endTime,omitempty"`
	ImportedModelARN  string `json:"importedModelArn,omitempty"`
	ImportedModelName string `json:"importedModelName,omitempty"`
	JobARN            string `json:"jobArn"`
	JobName           string `json:"jobName"`
	Status            string `json:"status"`
	LastModifiedTime  string `json:"lastModifiedTime,omitempty"`
}

type listImportJobsResponse struct {
	ModelImportJobSummaries []importJobSummaryJSON `json:"modelImportJobSummaries"`
	NextToken               string                 `json:"nextToken,omitempty"`
}

// --- model copy job wire types ---

type createCopyJobRequest struct {
	SourceModelARN     string    `json:"sourceModelArn"`
	TargetModelName    string    `json:"targetModelName"`
	ClientRequestToken string    `json:"clientRequestToken"`
	ModelKMSKeyID      string    `json:"modelKmsKeyId"`
	TargetModelTags    []tagPair `json:"targetModelTags"`
}

type copyJobJSON struct {
	CreationTime         string `json:"creationTime,omitempty"`
	JobARN               string `json:"jobArn"`
	SourceAccountID      string `json:"sourceAccountId,omitempty"`
	SourceModelARN       string `json:"sourceModelArn"`
	SourceModelName      string `json:"sourceModelName,omitempty"`
	Status               string `json:"status"`
	TargetModelARN       string `json:"targetModelArn,omitempty"`
	TargetModelName      string `json:"targetModelName,omitempty"`
	TargetModelKMSKeyARN string `json:"targetModelKmsKeyArn,omitempty"`
	FailureMessage       string `json:"failureMessage,omitempty"`
}

type listCopyJobsResponse struct {
	ModelCopyJobSummaries []copyJobJSON `json:"modelCopyJobSummaries"`
	NextToken             string        `json:"nextToken,omitempty"`
}

// --- evaluation job wire types ---

type createEvalJobRequest struct {
	JobName                 string                    `json:"jobName"`
	RoleARN                 string                    `json:"roleArn"`
	EvaluationConfig        json.RawMessage           `json:"evaluationConfig"`
	InferenceConfig         json.RawMessage           `json:"inferenceConfig"`
	OutputDataConfig        *evalOutputDataConfigJSON `json:"outputDataConfig"`
	ApplicationType         string                    `json:"applicationType"`
	ClientRequestToken      string                    `json:"clientRequestToken"`
	CustomerEncryptionKeyID string                    `json:"customerEncryptionKeyId"`
	JobDescription          string                    `json:"jobDescription"`
	JobTags                 []tagPair                 `json:"jobTags"`
}

type evalJobJSON struct {
	ApplicationType         string                    `json:"applicationType,omitempty"`
	CreationTime            string                    `json:"creationTime,omitempty"`
	EvaluationConfig        json.RawMessage           `json:"evaluationConfig,omitempty"`
	InferenceConfig         json.RawMessage           `json:"inferenceConfig,omitempty"`
	JobARN                  string                    `json:"jobArn"`
	JobName                 string                    `json:"jobName"`
	JobType                 string                    `json:"jobType,omitempty"`
	OutputDataConfig        *evalOutputDataConfigJSON `json:"outputDataConfig,omitempty"`
	RoleARN                 string                    `json:"roleArn,omitempty"`
	Status                  string                    `json:"status"`
	LastModifiedTime        string                    `json:"lastModifiedTime,omitempty"`
	JobDescription          string                    `json:"jobDescription,omitempty"`
	CustomerEncryptionKeyID string                    `json:"customerEncryptionKeyId,omitempty"`
	FailureMessages         []string                  `json:"failureMessages,omitempty"`
}

type evalJobSummaryJSON struct {
	ApplicationType     string   `json:"applicationType,omitempty"`
	CreationTime        string   `json:"creationTime,omitempty"`
	JobARN              string   `json:"jobArn"`
	JobName             string   `json:"jobName"`
	JobType             string   `json:"jobType"`
	Status              string   `json:"status"`
	EvaluationTaskTypes []string `json:"evaluationTaskTypes,omitempty"`
}

type listEvalJobsResponse struct {
	JobSummaries []evalJobSummaryJSON `json:"jobSummaries"`
	NextToken    string               `json:"nextToken,omitempty"`
}

// --- import job dispatch + operations ---

// serveImportJobs handles /model-import-jobs[/{jobIdentifier}].
func (h *Handler) serveImportJobs(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		switch r.Method {
		case http.MethodPost:
			h.createImportJob(w, r)
		case http.MethodGet:
			h.listImportJobs(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	h.getImportJob(w, r, id)
}

func (h *Handler) createImportJob(w http.ResponseWriter, r *http.Request) {
	var in createImportJobRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	job, err := h.bedrock.CreateModelImportJob(r.Context(), bedrockdriver.ModelImportJobConfig{
		JobName:               in.JobName,
		ImportedModelName:     in.ImportedModelName,
		RoleARN:               in.RoleARN,
		ModelDataSourceS3URI:  modelDataSourceURI(in.ModelDataSource),
		ClientRequestToken:    in.ClientRequestToken,
		ImportedModelKMSKeyID: in.ImportedModelKMSKeyID,
		JobTags:               tagsToMap(in.JobTags),
		ImportedModelTags:     tagsToMap(in.ImportedModelTags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, createJobArnResponse{JobARN: job.JobARN})
}

func (h *Handler) getImportJob(w http.ResponseWriter, r *http.Request, id string) {
	job, err := h.bedrock.GetModelImportJob(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toImportJobJSON(job))
}

func (h *Handler) listImportJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.bedrock.ListModelImportJobs(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]importJobSummaryJSON, 0, len(jobs))
	for i := range jobs {
		out = append(out, toImportJobSummaryJSON(&jobs[i]))
	}

	writeJSON(w, listImportJobsResponse{ModelImportJobSummaries: out})
}

// --- copy job dispatch + operations ---

// serveCopyJobs handles /model-copy-jobs[/{jobArn}]. The job ARN contains
// slashes, so it is the entire remainder of the path.
func (h *Handler) serveCopyJobs(w http.ResponseWriter, r *http.Request, arn string) {
	if arn == "" {
		switch r.Method {
		case http.MethodPost:
			h.createCopyJob(w, r)
		case http.MethodGet:
			h.listCopyJobs(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	h.getCopyJob(w, r, arn)
}

func (h *Handler) createCopyJob(w http.ResponseWriter, r *http.Request) {
	var in createCopyJobRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	job, err := h.bedrock.CreateModelCopyJob(r.Context(), bedrockdriver.ModelCopyJobConfig{
		SourceModelARN:     in.SourceModelARN,
		TargetModelName:    in.TargetModelName,
		ClientRequestToken: in.ClientRequestToken,
		ModelKMSKeyID:      in.ModelKMSKeyID,
		TargetModelTags:    tagsToMap(in.TargetModelTags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, createJobArnResponse{JobARN: job.JobARN})
}

func (h *Handler) getCopyJob(w http.ResponseWriter, r *http.Request, arn string) {
	job, err := h.bedrock.GetModelCopyJob(r.Context(), arn)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toCopyJobJSON(job))
}

func (h *Handler) listCopyJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.bedrock.ListModelCopyJobs(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]copyJobJSON, 0, len(jobs))
	for i := range jobs {
		out = append(out, toCopyJobJSON(&jobs[i]))
	}

	writeJSON(w, listCopyJobsResponse{ModelCopyJobSummaries: out})
}

// --- evaluation job dispatch + operations ---

// serveEvalJobs handles /evaluation-jobs[/{jobIdentifier}].
func (h *Handler) serveEvalJobs(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		switch r.Method {
		case http.MethodPost:
			h.createEvalJob(w, r)
		case http.MethodGet:
			h.listEvalJobs(w, r)
		default:
			methodNotAllowed(w)
		}

		return
	}

	if r.Method != http.MethodGet {
		methodNotAllowed(w)

		return
	}

	h.getEvalJob(w, r, id)
}

// serveEvalJobStop handles POST /evaluation-job/{jobIdentifier}/stop. rest is
// the path with the /evaluation-job/ prefix already trimmed; the identifier may
// contain slashes (ARN), so /stop is split off the tail.
func (h *Handler) serveEvalJobStop(w http.ResponseWriter, r *http.Request, rest string) {
	const suffix = "/stop"

	if !strings.HasSuffix(rest, suffix) {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported evaluation-job path")

		return
	}

	id := strings.TrimSuffix(rest, suffix)

	if r.Method != http.MethodPost {
		methodNotAllowed(w)

		return
	}

	if err := h.bedrock.StopEvaluationJob(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) createEvalJob(w http.ResponseWriter, r *http.Request) {
	var in createEvalJobRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	job, err := h.bedrock.CreateEvaluationJob(r.Context(), bedrockdriver.EvaluationJobConfig{
		JobName:                 in.JobName,
		RoleARN:                 in.RoleARN,
		EvaluationConfig:        []byte(in.EvaluationConfig),
		InferenceConfig:         []byte(in.InferenceConfig),
		OutputDataS3URI:         evalOutputURI(in.OutputDataConfig),
		ApplicationType:         in.ApplicationType,
		ClientRequestToken:      in.ClientRequestToken,
		CustomerEncryptionKeyID: in.CustomerEncryptionKeyID,
		JobDescription:          in.JobDescription,
		JobTags:                 tagsToMap(in.JobTags),
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, createJobArnResponse{JobARN: job.JobARN})
}

func (h *Handler) getEvalJob(w http.ResponseWriter, r *http.Request, id string) {
	job, err := h.bedrock.GetEvaluationJob(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toEvalJobJSON(job))
}

func (h *Handler) listEvalJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.bedrock.ListEvaluationJobs(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]evalJobSummaryJSON, 0, len(jobs))
	for i := range jobs {
		out = append(out, toEvalJobSummaryJSON(&jobs[i]))
	}

	writeJSON(w, listEvalJobsResponse{JobSummaries: out})
}

// --- converters ---

func modelDataSourceURI(in *modelDataSourceJSON) string {
	if in == nil || in.S3DataSource == nil {
		return ""
	}

	return in.S3DataSource.S3URI
}

func evalOutputURI(in *evalOutputDataConfigJSON) string {
	if in == nil {
		return ""
	}

	return in.S3URI
}

func dataSourceOf(uri string) *modelDataSourceJSON {
	if uri == "" {
		return nil
	}

	return &modelDataSourceJSON{S3DataSource: &s3DataSourceJSON{S3URI: uri}}
}

func evalOutputOf(uri string) *evalOutputDataConfigJSON {
	if uri == "" {
		return nil
	}

	return &evalOutputDataConfigJSON{S3URI: uri}
}

func toImportJobJSON(job *bedrockdriver.ModelImportJob) importJobJSON {
	return importJobJSON{
		CreationTime:      job.CreationTime,
		EndTime:           job.EndTime,
		ImportedModelARN:  job.ImportedModelARN,
		ImportedModelName: job.ImportedModelName,
		JobARN:            job.JobARN,
		JobName:           job.JobName,
		ModelDataSource:   dataSourceOf(job.ModelDataSourceS3URI),
		RoleARN:           job.RoleARN,
		Status:            job.Status,
		LastModifiedTime:  job.LastModifiedTime,
		FailureMessage:    job.FailureMessage,
	}
}

func toImportJobSummaryJSON(job *bedrockdriver.ModelImportJob) importJobSummaryJSON {
	return importJobSummaryJSON{
		CreationTime:      job.CreationTime,
		EndTime:           job.EndTime,
		ImportedModelARN:  job.ImportedModelARN,
		ImportedModelName: job.ImportedModelName,
		JobARN:            job.JobARN,
		JobName:           job.JobName,
		Status:            job.Status,
		LastModifiedTime:  job.LastModifiedTime,
	}
}

func toCopyJobJSON(job *bedrockdriver.ModelCopyJob) copyJobJSON {
	return copyJobJSON{
		CreationTime:         job.CreationTime,
		JobARN:               job.JobARN,
		SourceAccountID:      job.SourceAccountID,
		SourceModelARN:       job.SourceModelARN,
		SourceModelName:      job.SourceModelName,
		Status:               job.Status,
		TargetModelARN:       job.TargetModelARN,
		TargetModelName:      job.TargetModelName,
		TargetModelKMSKeyARN: job.TargetModelKMSKeyARN,
		FailureMessage:       job.FailureMessage,
	}
}

func toEvalJobJSON(job *bedrockdriver.EvaluationJob) evalJobJSON {
	return evalJobJSON{
		ApplicationType:         job.ApplicationType,
		CreationTime:            job.CreationTime,
		EvaluationConfig:        json.RawMessage(job.EvaluationConfig),
		InferenceConfig:         json.RawMessage(job.InferenceConfig),
		JobARN:                  job.JobARN,
		JobName:                 job.JobName,
		JobType:                 job.JobType,
		OutputDataConfig:        evalOutputOf(job.OutputDataS3URI),
		RoleARN:                 job.RoleARN,
		Status:                  job.Status,
		LastModifiedTime:        job.LastModifiedTime,
		JobDescription:          job.JobDescription,
		CustomerEncryptionKeyID: job.CustomerEncryptionKeyID,
		FailureMessages:         job.FailureMessages,
	}
}

func toEvalJobSummaryJSON(job *bedrockdriver.EvaluationJob) evalJobSummaryJSON {
	return evalJobSummaryJSON{
		ApplicationType:     job.ApplicationType,
		CreationTime:        job.CreationTime,
		JobARN:              job.JobARN,
		JobName:             job.JobName,
		JobType:             job.JobType,
		Status:              job.Status,
		EvaluationTaskTypes: []string{"Generation"},
	}
}
