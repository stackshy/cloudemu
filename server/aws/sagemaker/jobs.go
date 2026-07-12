package sagemaker

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
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

//nolint:dupl // SDK-compat decode/encode shim; the skeleton recurs but each op maps a distinct type.
func (h *Handler) listTrainingJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.svc.ListTrainingJobs(r.Context())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "TrainingJobSummaries", jobs, func(j *driver.TrainingJob) map[string]any {
		return map[string]any{
			"TrainingJobName":   j.JobName,
			"TrainingJobArn":    j.JobARN,
			"TrainingJobStatus": j.Status,
			"CreationTime":      epoch(j.CreationTime),
			"LastModifiedTime":  epoch(j.LastModifiedTime),
		}
	})
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
