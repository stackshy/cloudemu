package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/sagemaker/driver"
	"github.com/stackshy/cloudemu/server/wire"
)

// wireAlgorithm is the JSON shape of AlgorithmSpecification.
type wireAlgorithm struct {
	TrainingImage     string `json:"TrainingImage"`
	TrainingInputMode string `json:"TrainingInputMode,omitempty"`
}

type wireOutputConfig struct {
	S3OutputPath string `json:"S3OutputPath"`
}

type wireResourceConfig struct {
	InstanceType   string `json:"InstanceType"`
	InstanceCount  int    `json:"InstanceCount"`
	VolumeSizeInGB int    `json:"VolumeSizeInGB"`
}

type wireStopping struct {
	MaxRuntimeInSeconds int `json:"MaxRuntimeInSeconds,omitempty"`
}

func (h *Handler) createTrainingJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TrainingJobName        string             `json:"TrainingJobName"`
		RoleArn                string             `json:"RoleArn"`
		AlgorithmSpecification wireAlgorithm      `json:"AlgorithmSpecification"`
		OutputDataConfig       wireOutputConfig   `json:"OutputDataConfig"`
		ResourceConfig         wireResourceConfig `json:"ResourceConfig"`
		StoppingCondition      wireStopping       `json:"StoppingCondition"`
		HyperParameters        map[string]string  `json:"HyperParameters"`
		Tags                   []wireTag          `json:"Tags"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	job, err := h.svc.CreateTrainingJob(r.Context(), driver.TrainingJobConfig{
		JobName:         req.TrainingJobName,
		RoleARN:         req.RoleArn,
		AlgorithmImage:  req.AlgorithmSpecification.TrainingImage,
		HyperParameters: req.HyperParameters,
		OutputS3URI:     req.OutputDataConfig.S3OutputPath,
		Resources: driver.ResourceConfig{
			InstanceType:   req.ResourceConfig.InstanceType,
			InstanceCount:  req.ResourceConfig.InstanceCount,
			VolumeSizeInGB: req.ResourceConfig.VolumeSizeInGB,
		},
		MaxRuntimeSeconds: req.StoppingCondition.MaxRuntimeInSeconds,
		Tags:              toTags(req.Tags),
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"TrainingJobArn": job.JobARN})
}

func (h *Handler) describeTrainingJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TrainingJobName string `json:"TrainingJobName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	job, err := h.svc.DescribeTrainingJob(r.Context(), req.TrainingJobName)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{
		"TrainingJobName":   job.JobName,
		"TrainingJobArn":    job.JobARN,
		"TrainingJobStatus": job.Status,
		"SecondaryStatus":   job.SecondaryStatus,
		"RoleArn":           job.RoleARN,
		"AlgorithmSpecification": wireAlgorithm{
			TrainingImage:     job.AlgorithmImage,
			TrainingInputMode: "File",
		},
		"OutputDataConfig": wireOutputConfig{S3OutputPath: job.OutputS3URI},
		"ResourceConfig": wireResourceConfig{
			InstanceType:   job.Resources.InstanceType,
			InstanceCount:  job.Resources.InstanceCount,
			VolumeSizeInGB: job.Resources.VolumeSizeInGB,
		},
		"ModelArtifacts":   map[string]any{"S3ModelArtifacts": job.ModelArtifactS3URI},
		"HyperParameters":  job.HyperParameters,
		"CreationTime":     epoch(job.CreationTime),
		"LastModifiedTime": epoch(job.LastModifiedTime),
	})
}

func (h *Handler) listTrainingJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.svc.ListTrainingJobs(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	out := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		out = append(out, map[string]any{
			"TrainingJobName":   jobs[i].JobName,
			"TrainingJobArn":    jobs[i].JobARN,
			"TrainingJobStatus": jobs[i].Status,
			"CreationTime":      epoch(jobs[i].CreationTime),
			"LastModifiedTime":  epoch(jobs[i].LastModifiedTime),
		})
	}

	wire.WriteJSON(w, map[string]any{"TrainingJobSummaries": out})
}

func (h *Handler) stopTrainingJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TrainingJobName string `json:"TrainingJobName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.svc.StopTrainingJob(r.Context(), req.TrainingJobName); err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{})
}
