package bedrock

import (
	"encoding/json"
	"io"
	"net/http"

	bedrockdriver "github.com/stackshy/cloudemu/v2/services/bedrock/driver"
)

func (h *Handler) listFoundationModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.bedrock.ListFoundationModels(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]foundationModelJSON, 0, len(models))
	for i := range models {
		out = append(out, toFoundationJSON(&models[i]))
	}

	writeJSON(w, listFoundationModelsResponse{ModelSummaries: out})
}

func (h *Handler) getFoundationModel(w http.ResponseWriter, r *http.Request, id string) {
	fm, err := h.bedrock.GetFoundationModel(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, getFoundationModelResponse{ModelDetails: toFoundationJSON(fm)})
}

func (h *Handler) createCustomizationJob(w http.ResponseWriter, r *http.Request) {
	var in createJobRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	cfg := bedrockdriver.CustomizationJobConfig{
		JobName:             in.JobName,
		CustomModelName:     in.CustomModelName,
		RoleARN:             in.RoleARN,
		BaseModelIdentifier: in.BaseModelIdentifier,
		CustomizationType:   in.CustomizationType,
		ClientRequestToken:  in.ClientRequestToken,
		HyperParameters:     in.HyperParameters,
		TrainingDataURI:     s3URI(in.TrainingDataConfig),
		OutputDataURI:       s3URI(in.OutputDataConfig),
	}

	job, err := h.bedrock.CreateModelCustomizationJob(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, createJobResponse{JobARN: job.JobARN})
}

func (h *Handler) getCustomizationJob(w http.ResponseWriter, r *http.Request, id string) {
	job, err := h.bedrock.GetModelCustomizationJob(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toJobJSON(job))
}

func (h *Handler) listCustomizationJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.bedrock.ListModelCustomizationJobs(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]jobSummaryJSON, 0, len(jobs))
	for i := range jobs {
		out = append(out, toJobSummaryJSON(&jobs[i]))
	}

	writeJSON(w, listJobsResponse{ModelCustomizationJobSummaries: out})
}

func (h *Handler) listCustomModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.bedrock.ListCustomModels(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]customModelSummaryJSON, 0, len(models))
	for i := range models {
		out = append(out, toCustomModelSummaryJSON(&models[i]))
	}

	writeJSON(w, listCustomModelsResponse{ModelSummaries: out})
}

func (h *Handler) getCustomModel(w http.ResponseWriter, r *http.Request, id string) {
	cm, err := h.bedrock.GetCustomModel(r.Context(), id)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toCustomModelJSON(cm))
}

func (h *Handler) deleteCustomModel(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.bedrock.DeleteCustomModel(r.Context(), id); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, struct{}{})
}

func (h *Handler) invokeModel(w http.ResponseWriter, r *http.Request, modelID string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "read body: "+err.Error())

		return
	}

	res, err := h.bedrock.InvokeModel(r.Context(), bedrockdriver.InvokeModelInput{
		ModelID:     modelID,
		ContentType: r.Header.Get("Content-Type"),
		Accept:      r.Header.Get("Accept"),
		Body:        body,
	})
	if err != nil {
		writeErr(w, err)

		return
	}

	ct := res.ContentType
	if ct == "" {
		ct = contentTypeJSON
	}

	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(res.Body)
}

func (h *Handler) converse(w http.ResponseWriter, r *http.Request, modelID string) {
	var in converseRequest
	if !decodeJSON(w, r, &in) {
		return
	}

	out, err := h.bedrock.Converse(r.Context(), toConverseInput(modelID, &in))
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, toConverseResponse(out))
}

// --- converters ---

func toFoundationJSON(fm *bedrockdriver.FoundationModel) foundationModelJSON {
	out := foundationModelJSON{
		ModelARN:                   fm.ModelARN,
		ModelID:                    fm.ModelID,
		ModelName:                  fm.ModelName,
		ProviderName:               fm.ProviderName,
		InputModalities:            fm.InputModalities,
		OutputModalities:           fm.OutputModalities,
		ResponseStreamingSupported: fm.ResponseStreamingSupported,
		CustomizationsSupported:    fm.CustomizationsSupported,
		InferenceTypesSupported:    fm.InferenceTypesSupported,
	}
	if fm.LifecycleStatus != "" {
		out.ModelLifecycle = &modelLifecycleJSON{Status: fm.LifecycleStatus}
	}

	return out
}

func toJobJSON(job *bedrockdriver.CustomizationJob) jobJSON {
	return jobJSON{
		JobARN:             job.JobARN,
		JobName:            job.JobName,
		OutputModelName:    job.OutputModelName,
		OutputModelARN:     job.OutputModelARN,
		RoleARN:            job.RoleARN,
		BaseModelARN:       job.BaseModelARN,
		Status:             job.Status,
		CustomizationType:  job.CustomizationType,
		ClientRequestToken: job.ClientRequestToken,
		HyperParameters:    job.HyperParameters,
		TrainingDataConfig: dataConfigOf(job.TrainingDataURI),
		OutputDataConfig:   dataConfigOf(job.OutputDataURI),
		CreationTime:       job.CreationTime,
		LastModifiedTime:   job.LastModifiedTime,
		EndTime:            job.EndTime,
		FailureMessage:     job.FailureMessage,
	}
}

func toJobSummaryJSON(job *bedrockdriver.CustomizationJob) jobSummaryJSON {
	return jobSummaryJSON{
		JobARN:            job.JobARN,
		JobName:           job.JobName,
		BaseModelARN:      job.BaseModelARN,
		CustomModelName:   job.OutputModelName,
		CustomModelARN:    job.OutputModelARN,
		Status:            job.Status,
		CustomizationType: job.CustomizationType,
		CreationTime:      job.CreationTime,
		LastModifiedTime:  job.LastModifiedTime,
		EndTime:           job.EndTime,
	}
}

func toCustomModelSummaryJSON(cm *bedrockdriver.CustomModel) customModelSummaryJSON {
	return customModelSummaryJSON{
		ModelARN:          cm.ModelARN,
		ModelName:         cm.ModelName,
		BaseModelARN:      cm.BaseModelARN,
		BaseModelName:     cm.BaseModelName,
		CustomizationType: cm.CustomizationType,
		ModelStatus:       cm.ModelStatus,
		OwnerAccountID:    cm.OwnerAccountID,
		CreationTime:      cm.CreationTime,
	}
}

func toCustomModelJSON(cm *bedrockdriver.CustomModel) getCustomModelResponse {
	return getCustomModelResponse{
		ModelARN:           cm.ModelARN,
		ModelName:          cm.ModelName,
		JobARN:             cm.JobARN,
		JobName:            cm.JobName,
		BaseModelARN:       cm.BaseModelARN,
		CustomizationType:  cm.CustomizationType,
		ModelStatus:        cm.ModelStatus,
		HyperParameters:    cm.HyperParameters,
		TrainingDataConfig: dataConfigOf(cm.TrainingDataURI),
		OutputDataConfig:   dataConfigOf(cm.OutputDataURI),
		CreationTime:       cm.CreationTime,
	}
}

func toConverseInput(modelID string, in *converseRequest) bedrockdriver.ConverseInput {
	msgs := make([]bedrockdriver.Message, 0, len(in.Messages))
	for _, m := range in.Messages {
		msgs = append(msgs, bedrockdriver.Message{Role: m.Role, Text: textsOf(m.Content)})
	}

	out := bedrockdriver.ConverseInput{
		ModelID:  modelID,
		System:   textsOf(in.System),
		Messages: msgs,
	}

	if cfg := in.InferenceConfig; cfg != nil {
		out.InferenceConfig = &bedrockdriver.InferenceConfig{
			MaxTokens:     cfg.MaxTokens,
			Temperature:   cfg.Temperature,
			TopP:          cfg.TopP,
			StopSequences: cfg.StopSequences,
		}
	}

	return out
}

func toConverseResponse(out *bedrockdriver.ConverseOutput) converseResponse {
	content := make([]converseTextBlock, 0, len(out.Message.Text))
	for _, t := range out.Message.Text {
		content = append(content, converseTextBlock{Text: t})
	}

	return converseResponse{
		Output:     converseOutputUnion{Message: converseOutputMessage{Role: out.Message.Role, Content: content}},
		StopReason: out.StopReason,
		Usage:      converseUsage{InputTokens: out.InputTokens, OutputTokens: out.OutputTokens, TotalTokens: out.TotalTokens},
		Metrics:    converseMetrics{LatencyMs: out.LatencyMs},
	}
}

func textsOf(blocks []converseTextBlock) []string {
	out := make([]string, 0, len(blocks))

	for _, b := range blocks {
		if b.Text != "" {
			out = append(out, b.Text)
		}
	}

	return out
}

func s3URI(c *dataConfig) string {
	if c == nil {
		return ""
	}

	return c.S3URI
}

func dataConfigOf(uri string) *dataConfig {
	if uri == "" {
		return nil
	}

	return &dataConfig{S3URI: uri}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "ValidationException", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}
