package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/sagemaker/driver"
	"github.com/stackshy/cloudemu/server/wire"
)

func (h *Handler) routeMoreJobs(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateProcessingJob":
		h.createProcessingJob(w, r)
	case "DescribeProcessingJob":
		h.describeProcessingJob(w, r)
	case "ListProcessingJobs":
		h.listProcessingJobs(w, r)
	case "StopProcessingJob":
		h.stopProcessingJob(w, r)
	case "CreateTransformJob":
		h.createTransformJob(w, r)
	case "DescribeTransformJob":
		h.describeTransformJob(w, r)
	case "ListTransformJobs":
		h.listTransformJobs(w, r)
	case "StopTransformJob":
		h.stopTransformJob(w, r)
	default:
		return h.routeTuningJobs(w, r, op)
	}

	return true
}

func (h *Handler) routeTuningJobs(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateHyperParameterTuningJob":
		h.createTuningJob(w, r)
	case "DescribeHyperParameterTuningJob":
		h.describeTuningJob(w, r)
	case "ListHyperParameterTuningJobs":
		h.listTuningJobs(w, r)
	case "StopHyperParameterTuningJob":
		h.stopTuningJob(w, r)
	case "CreateAutoMLJobV2":
		h.createAutoMLJob(w, r)
	case "DescribeAutoMLJobV2":
		h.describeAutoMLJob(w, r)
	case "ListAutoMLJobs":
		h.listAutoMLJobs(w, r)
	case "StopAutoMLJob":
		h.stopAutoMLJob(w, r)
	default:
		return h.routeMoreJobs2(w, r, op)
	}

	return true
}

func (h *Handler) routeMoreJobs2(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateLabelingJob":
		h.createLabelingJob(w, r)
	case "DescribeLabelingJob":
		h.describeLabelingJob(w, r)
	case "ListLabelingJobs":
		h.listLabelingJobs(w, r)
	case "StopLabelingJob":
		h.stopLabelingJob(w, r)
	case "CreateCompilationJob":
		h.createCompilationJob(w, r)
	case "DescribeCompilationJob":
		h.describeCompilationJob(w, r)
	case "ListCompilationJobs":
		h.listCompilationJobs(w, r)
	case "StopCompilationJob":
		h.stopCompilationJob(w, r)
	default:
		return false
	}

	return true
}

// --- Processing ---

func (h *Handler) createProcessingJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProcessingJobName string `json:"ProcessingJobName"`
		RoleArn           string `json:"RoleArn"`
		AppSpecification  struct {
			ImageURI string `json:"ImageUri"`
		} `json:"AppSpecification"`
		Tags []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	job, err := h.svc.CreateProcessingJob(r.Context(), driver.ProcessingJobConfig{
		JobName:  req.ProcessingJobName,
		RoleARN:  req.RoleArn,
		AppImage: req.AppSpecification.ImageURI,
		Tags:     toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"ProcessingJobArn": job.JobARN})
}

func (h *Handler) describeProcessingJob(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "ProcessingJobName")
	if !ok {
		return
	}

	job, err := h.svc.DescribeProcessingJob(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"ProcessingJobName":   job.JobName,
		"ProcessingJobArn":    job.JobARN,
		"ProcessingJobStatus": job.Status,
		"RoleArn":             job.RoleARN,
		"CreationTime":        epoch(job.CreationTime),
	})
}

func (h *Handler) listProcessingJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.svc.ListProcessingJobs(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		out = append(out, map[string]any{
			"ProcessingJobName":   jobs[i].JobName,
			"ProcessingJobArn":    jobs[i].JobARN,
			"ProcessingJobStatus": jobs[i].Status,
			"CreationTime":        epoch(jobs[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"ProcessingJobSummaries": out})
}

func (h *Handler) stopProcessingJob(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "ProcessingJobName", h.svc.StopProcessingJob)
}

// --- Transform ---

func (h *Handler) createTransformJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TransformJobName string    `json:"TransformJobName"`
		ModelName        string    `json:"ModelName"`
		Tags             []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	job, err := h.svc.CreateTransformJob(r.Context(), driver.TransformJobConfig{
		JobName:   req.TransformJobName,
		ModelName: req.ModelName,
		Tags:      toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"TransformJobArn": job.JobARN})
}

func (h *Handler) describeTransformJob(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "TransformJobName")
	if !ok {
		return
	}

	job, err := h.svc.DescribeTransformJob(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"TransformJobName":   job.JobName,
		"TransformJobArn":    job.JobARN,
		"TransformJobStatus": job.Status,
		"ModelName":          job.ModelName,
		"CreationTime":       epoch(job.CreationTime),
	})
}

func (h *Handler) listTransformJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.svc.ListTransformJobs(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		out = append(out, map[string]any{
			"TransformJobName":   jobs[i].JobName,
			"TransformJobArn":    jobs[i].JobARN,
			"TransformJobStatus": jobs[i].Status,
			"CreationTime":       epoch(jobs[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"TransformJobSummaries": out})
}

func (h *Handler) stopTransformJob(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "TransformJobName", h.svc.StopTransformJob)
}

// --- Hyperparameter tuning ---

func (h *Handler) createTuningJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HyperParameterTuningJobName   string `json:"HyperParameterTuningJobName"`
		HyperParameterTuningJobConfig struct {
			Strategy       string `json:"Strategy"`
			ResourceLimits struct {
				MaxNumberOfTrainingJobs int `json:"MaxNumberOfTrainingJobs"`
				MaxParallelTrainingJobs int `json:"MaxParallelTrainingJobs"`
			} `json:"ResourceLimits"`
			HyperParameterTuningJobObjective struct {
				Type       string `json:"Type"`
				MetricName string `json:"MetricName"`
			} `json:"HyperParameterTuningJobObjective"`
		} `json:"HyperParameterTuningJobConfig"`
		Tags []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cfg := req.HyperParameterTuningJobConfig

	job, err := h.svc.CreateHyperParameterTuningJob(r.Context(), driver.HyperParameterTuningJobConfig{
		JobName:         req.HyperParameterTuningJobName,
		Strategy:        cfg.Strategy,
		MaxJobs:         cfg.ResourceLimits.MaxNumberOfTrainingJobs,
		MaxParallelJobs: cfg.ResourceLimits.MaxParallelTrainingJobs,
		ObjectiveMetric: cfg.HyperParameterTuningJobObjective.MetricName,
		ObjectiveType:   cfg.HyperParameterTuningJobObjective.Type,
		Tags:            toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"HyperParameterTuningJobArn": job.JobARN})
}

func (h *Handler) describeTuningJob(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "HyperParameterTuningJobName")
	if !ok {
		return
	}

	job, err := h.svc.DescribeHyperParameterTuningJob(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"HyperParameterTuningJobName":   job.JobName,
		"HyperParameterTuningJobArn":    job.JobARN,
		"HyperParameterTuningJobStatus": job.Status,
		"CreationTime":                  epoch(job.CreationTime),
	})
}

func (h *Handler) listTuningJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.svc.ListHyperParameterTuningJobs(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		out = append(out, map[string]any{
			"HyperParameterTuningJobName":   jobs[i].JobName,
			"HyperParameterTuningJobArn":    jobs[i].JobARN,
			"HyperParameterTuningJobStatus": jobs[i].Status,
			"CreationTime":                  epoch(jobs[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"HyperParameterTuningJobSummaries": out})
}

func (h *Handler) stopTuningJob(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "HyperParameterTuningJobName", h.svc.StopHyperParameterTuningJob)
}

// --- AutoML V2 ---

func (h *Handler) createAutoMLJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AutoMLJobName    string `json:"AutoMLJobName"`
		RoleArn          string `json:"RoleArn"`
		OutputDataConfig struct {
			S3OutputPath string `json:"S3OutputPath"`
		} `json:"OutputDataConfig"`
		Tags []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	job, err := h.svc.CreateAutoMLJobV2(r.Context(), driver.AutoMLJobConfig{
		JobName:     req.AutoMLJobName,
		RoleARN:     req.RoleArn,
		OutputS3URI: req.OutputDataConfig.S3OutputPath,
		Tags:        toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"AutoMLJobArn": job.JobARN})
}

func (h *Handler) describeAutoMLJob(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "AutoMLJobName")
	if !ok {
		return
	}

	job, err := h.svc.DescribeAutoMLJobV2(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"AutoMLJobName":            job.JobName,
		"AutoMLJobArn":             job.JobARN,
		"AutoMLJobStatus":          job.Status,
		"AutoMLJobSecondaryStatus": job.SecondaryStatus,
		"CreationTime":             epoch(job.CreationTime),
	})
}

func (h *Handler) listAutoMLJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.svc.ListAutoMLJobs(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		out = append(out, map[string]any{
			"AutoMLJobName":   jobs[i].JobName,
			"AutoMLJobArn":    jobs[i].JobARN,
			"AutoMLJobStatus": jobs[i].Status,
			"CreationTime":    epoch(jobs[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"AutoMLJobSummaries": out})
}

func (h *Handler) stopAutoMLJob(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "AutoMLJobName", h.svc.StopAutoMLJob)
}

// --- Labeling ---

func (h *Handler) createLabelingJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LabelingJobName    string    `json:"LabelingJobName"`
		RoleArn            string    `json:"RoleArn"`
		LabelAttributeName string    `json:"LabelAttributeName"`
		Tags               []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	job, err := h.svc.CreateLabelingJob(r.Context(), driver.LabelingJobConfig{
		JobName:        req.LabelingJobName,
		RoleARN:        req.RoleArn,
		LabelAttribute: req.LabelAttributeName,
		Tags:           toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"LabelingJobArn": job.JobARN})
}

func (h *Handler) describeLabelingJob(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "LabelingJobName")
	if !ok {
		return
	}

	job, err := h.svc.DescribeLabelingJob(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"LabelingJobName":   job.JobName,
		"LabelingJobArn":    job.JobARN,
		"LabelingJobStatus": job.Status,
		"CreationTime":      epoch(job.CreationTime),
	})
}

func (h *Handler) listLabelingJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.svc.ListLabelingJobs(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		out = append(out, map[string]any{
			"LabelingJobName":   jobs[i].JobName,
			"LabelingJobArn":    jobs[i].JobARN,
			"LabelingJobStatus": jobs[i].Status,
			"CreationTime":      epoch(jobs[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"LabelingJobSummaryList": out})
}

func (h *Handler) stopLabelingJob(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "LabelingJobName", h.svc.StopLabelingJob)
}

// --- Compilation ---

func (h *Handler) createCompilationJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CompilationJobName string    `json:"CompilationJobName"`
		RoleArn            string    `json:"RoleArn"`
		Tags               []wireTag `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	job, err := h.svc.CreateCompilationJob(r.Context(), driver.CompilationJobConfig{
		JobName: req.CompilationJobName,
		RoleARN: req.RoleArn,
		Tags:    toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"CompilationJobArn": job.JobARN})
}

func (h *Handler) describeCompilationJob(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "CompilationJobName")
	if !ok {
		return
	}

	job, err := h.svc.DescribeCompilationJob(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"CompilationJobName":   job.JobName,
		"CompilationJobArn":    job.JobARN,
		"CompilationJobStatus": job.Status,
		"CreationTime":         epoch(job.CreationTime),
	})
}

func (h *Handler) listCompilationJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.svc.ListCompilationJobs(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		out = append(out, map[string]any{
			"CompilationJobName":   jobs[i].JobName,
			"CompilationJobArn":    jobs[i].JobARN,
			"CompilationJobStatus": jobs[i].Status,
			"CreationTime":         epoch(jobs[i].CreationTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"CompilationJobSummaries": out})
}

func (h *Handler) stopCompilationJob(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "CompilationJobName", h.svc.StopCompilationJob)
}
