package sagemaker

import (
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// decodeJobRequest reads a job create request once, filling the typed fields in
// typed and returning every other member (minus skip) to be stored and echoed
// back by Describe, as SageMaker returns a job's inputs.
func decodeJobRequest(w http.ResponseWriter, r *http.Request, typed any) (map[string]json.RawMessage, bool) {
	var raw map[string]json.RawMessage
	if !wire.DecodeJSON(w, r, &raw) {
		return nil, false
	}

	b, err := json.Marshal(raw)
	if err == nil {
		err = json.Unmarshal(b, typed)
	}

	if err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "SerializationException", err.Error())

		return nil, false
	}

	delete(raw, "Tags") // Describe does not return tags

	return raw, true
}

// describeBody assembles a Describe response: fallback members first (what the
// emulator can derive when the request carried no value), then the request
// echoed verbatim, then the computed members, which always win.
func describeBody(fallback map[string]any, echo map[string]json.RawMessage, computed map[string]any) map[string]any {
	out := make(map[string]any, len(fallback)+len(echo)+len(computed))

	for k, v := range fallback {
		out[k] = v
	}

	for k, v := range echo {
		out[k] = v
	}

	for k, v := range computed {
		out[k] = v
	}

	return out
}

// putTime sets key to the epoch of iso when iso is set.
func putTime(m map[string]any, key, iso string) {
	if iso != "" {
		m[key] = epoch(iso)
	}
}

// --- Training ---

// wireTrainingRequest holds the typed members the provider needs from a
// CreateTrainingJob request.
type wireTrainingRequest struct {
	TrainingJobName        string `json:"TrainingJobName"`
	RoleArn                string `json:"RoleArn"`
	AlgorithmSpecification struct {
		TrainingImage string `json:"TrainingImage"`
	} `json:"AlgorithmSpecification"`
	InputDataConfig []struct {
		ChannelName string `json:"ChannelName"`
		ContentType string `json:"ContentType"`
		DataSource  struct {
			S3DataSource struct {
				S3Uri string `json:"S3Uri"`
			} `json:"S3DataSource"`
		} `json:"DataSource"`
	} `json:"InputDataConfig"`
	OutputDataConfig struct {
		S3OutputPath string `json:"S3OutputPath"`
	} `json:"OutputDataConfig"`
	ResourceConfig struct {
		InstanceType   string `json:"InstanceType"`
		InstanceCount  int    `json:"InstanceCount"`
		VolumeSizeInGB int    `json:"VolumeSizeInGB"`
	} `json:"ResourceConfig"`
	StoppingCondition struct {
		MaxRuntimeInSeconds int `json:"MaxRuntimeInSeconds"`
	} `json:"StoppingCondition"`
	HyperParameters map[string]string `json:"HyperParameters"`
	Tags            []wireTag         `json:"Tags"`
}

func (h *Handler) createTrainingJob(w http.ResponseWriter, r *http.Request) {
	var req wireTrainingRequest

	echo, ok := decodeJobRequest(w, r, &req)
	if !ok {
		return
	}

	channels := make([]driver.Channel, 0, len(req.InputDataConfig))
	for _, c := range req.InputDataConfig {
		channels = append(channels, driver.Channel{
			Name: c.ChannelName, S3URI: c.DataSource.S3DataSource.S3Uri, ContentType: c.ContentType,
		})
	}

	job, err := h.svc.CreateTrainingJob(r.Context(), driver.TrainingJobConfig{
		JobName:         req.TrainingJobName,
		RoleARN:         req.RoleArn,
		AlgorithmImage:  req.AlgorithmSpecification.TrainingImage,
		HyperParameters: req.HyperParameters,
		InputChannels:   channels,
		OutputS3URI:     req.OutputDataConfig.S3OutputPath,
		Resources: driver.ResourceConfig{
			InstanceType:   req.ResourceConfig.InstanceType,
			InstanceCount:  req.ResourceConfig.InstanceCount,
			VolumeSizeInGB: req.ResourceConfig.VolumeSizeInGB,
		},
		MaxRuntimeSeconds: req.StoppingCondition.MaxRuntimeInSeconds,
		Tags:              toTags(req.Tags),
		Echo:              echo,
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"TrainingJobArn": job.JobARN})
}

// defaultMaxRuntimeSeconds is SageMaker's default training StoppingCondition
// (one day), reported when the request set none.
const defaultMaxRuntimeSeconds = 86400

func (h *Handler) describeTrainingJob(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeName1(w, r, "TrainingJobName")
	if !ok {
		return
	}

	job, err := h.svc.DescribeTrainingJob(r.Context(), name)
	if err != nil {
		writeDriverError(w, err)

		return
	}

	fallback := map[string]any{
		"RoleArn": job.RoleARN,
		"AlgorithmSpecification": map[string]any{
			"TrainingImage": job.AlgorithmImage, "TrainingInputMode": "File",
		},
		"OutputDataConfig": map[string]any{"S3OutputPath": job.OutputS3URI},
		"ResourceConfig": map[string]any{
			"InstanceType": job.Resources.InstanceType, "InstanceCount": job.Resources.InstanceCount,
			"VolumeSizeInGB": job.Resources.VolumeSizeInGB,
		},
		"StoppingCondition": map[string]any{"MaxRuntimeInSeconds": defaultMaxRuntimeSeconds},
		"HyperParameters":   job.HyperParameters,
	}

	if len(job.InputChannels) > 0 {
		fallback["InputDataConfig"] = channelsToWire(job.InputChannels)
	}

	computed := map[string]any{
		"TrainingJobName":            job.JobName,
		"TrainingJobArn":             job.JobARN,
		"TrainingJobStatus":          job.Status,
		"SecondaryStatus":            job.SecondaryStatus,
		"SecondaryStatusTransitions": transitionsToWire(job.SecondaryTransitions),
		"ModelArtifacts":             map[string]any{"S3ModelArtifacts": job.ModelArtifactS3URI},
		"CreationTime":               epoch(job.CreationTime),
		"LastModifiedTime":           epoch(job.LastModifiedTime),
	}
	putTime(computed, "TrainingStartTime", job.TrainingStartTime)
	putTime(computed, "TrainingEndTime", job.TrainingEndTime)
	setIf(computed, "FailureReason", job.FailureReason)

	if job.TrainingTimeSeconds > 0 {
		computed["TrainingTimeInSeconds"] = job.TrainingTimeSeconds
		computed["BillableTimeInSeconds"] = job.BillableTimeSeconds
	}

	wire.WriteJSON(w, describeBody(fallback, job.Echo, computed))
}

func channelsToWire(in []driver.Channel) []map[string]any {
	out := make([]map[string]any, 0, len(in))

	for _, c := range in {
		e := map[string]any{
			"ChannelName": c.Name,
			"DataSource": map[string]any{"S3DataSource": map[string]any{
				"S3DataType": "S3Prefix", "S3Uri": c.S3URI, "S3DataDistributionType": "FullyReplicated",
			}},
		}
		setIf(e, "ContentType", c.ContentType)
		out = append(out, e)
	}

	return out
}

func transitionsToWire(in []driver.SecondaryStatusTransition) []map[string]any {
	out := make([]map[string]any, 0, len(in))

	for _, t := range in {
		e := map[string]any{"Status": t.Status, "StartTime": epoch(t.StartTime)}
		putTime(e, "EndTime", t.EndTime)
		setIf(e, "StatusMessage", t.StatusMessage)
		out = append(out, e)
	}

	return out
}

func (h *Handler) listTrainingJobs(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	jobs, err := h.svc.ListTrainingJobs(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "TrainingJobSummaries", jobs, func(j *driver.TrainingJob) map[string]any {
		m := map[string]any{
			"TrainingJobName":   j.JobName,
			"TrainingJobArn":    j.JobARN,
			"TrainingJobStatus": j.Status,
			"CreationTime":      epoch(j.CreationTime),
			"LastModifiedTime":  epoch(j.LastModifiedTime),
		}
		putTime(m, "TrainingEndTime", j.TrainingEndTime)
		setIf(m, "SecondaryStatus", j.SecondaryStatus)

		return m
	})
}

func (h *Handler) stopTrainingJob(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "TrainingJobName", h.svc.StopTrainingJob)
}

// --- Processing ---

func (h *Handler) createProcessingJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProcessingJobName string `json:"ProcessingJobName"`
		RoleArn           string `json:"RoleArn"`
		AppSpecification  struct {
			ImageURI string `json:"ImageUri"`
		} `json:"AppSpecification"`
		ProcessingResources struct {
			ClusterConfig struct {
				InstanceType   string `json:"InstanceType"`
				InstanceCount  int    `json:"InstanceCount"`
				VolumeSizeInGB int    `json:"VolumeSizeInGB"`
			} `json:"ClusterConfig"`
		} `json:"ProcessingResources"`
		Tags []wireTag `json:"Tags"`
	}

	echo, ok := decodeJobRequest(w, r, &req)
	if !ok {
		return
	}

	cc := req.ProcessingResources.ClusterConfig

	job, err := h.svc.CreateProcessingJob(r.Context(), driver.ProcessingJobConfig{
		JobName:   req.ProcessingJobName,
		RoleARN:   req.RoleArn,
		AppImage:  req.AppSpecification.ImageURI,
		Resources: driver.ResourceConfig{InstanceType: cc.InstanceType, InstanceCount: cc.InstanceCount, VolumeSizeInGB: cc.VolumeSizeInGB},
		Tags:      toTags(req.Tags),
		Echo:      echo,
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

	fallback := map[string]any{
		"RoleArn":          job.RoleARN,
		"AppSpecification": map[string]any{"ImageUri": job.AppImage},
		"ProcessingResources": map[string]any{"ClusterConfig": map[string]any{
			"InstanceType": job.Resources.InstanceType, "InstanceCount": job.Resources.InstanceCount,
			"VolumeSizeInGB": job.Resources.VolumeSizeInGB,
		}},
	}

	computed := map[string]any{
		"ProcessingJobName":   job.JobName,
		"ProcessingJobArn":    job.JobARN,
		"ProcessingJobStatus": job.Status,
		"CreationTime":        epoch(job.CreationTime),
		"LastModifiedTime":    epoch(job.LastModifiedTime),
	}
	putTime(computed, "ProcessingStartTime", job.CreationTime)
	putTime(computed, "ProcessingEndTime", job.ProcessingEndTime)
	setIf(computed, "FailureReason", job.FailureReason)

	wire.WriteJSON(w, describeBody(fallback, job.Echo, computed))
}

//nolint:dupl // per-resource shim mirrors the SDK shape
func (h *Handler) listProcessingJobs(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	jobs, err := h.svc.ListProcessingJobs(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "ProcessingJobSummaries", jobs, func(j *driver.ProcessingJob) map[string]any {
		m := map[string]any{
			"ProcessingJobName":   j.JobName,
			"ProcessingJobArn":    j.JobARN,
			"ProcessingJobStatus": j.Status,
			"CreationTime":        epoch(j.CreationTime),
			"LastModifiedTime":    epoch(j.LastModifiedTime),
		}
		putTime(m, "ProcessingEndTime", j.ProcessingEndTime)
		setIf(m, "FailureReason", j.FailureReason)

		return m
	})
}

func (h *Handler) stopProcessingJob(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "ProcessingJobName", h.svc.StopProcessingJob)
}

// --- Transform ---

func (h *Handler) createTransformJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TransformJobName string `json:"TransformJobName"`
		ModelName        string `json:"ModelName"`
		TransformInput   struct {
			DataSource struct {
				S3DataSource struct {
					S3Uri string `json:"S3Uri"`
				} `json:"S3DataSource"`
			} `json:"DataSource"`
		} `json:"TransformInput"`
		TransformOutput struct {
			S3OutputPath string `json:"S3OutputPath"`
		} `json:"TransformOutput"`
		TransformResources struct {
			InstanceType  string `json:"InstanceType"`
			InstanceCount int    `json:"InstanceCount"`
		} `json:"TransformResources"`
		Tags []wireTag `json:"Tags"`
	}

	echo, ok := decodeJobRequest(w, r, &req)
	if !ok {
		return
	}

	job, err := h.svc.CreateTransformJob(r.Context(), driver.TransformJobConfig{
		JobName:       req.TransformJobName,
		ModelName:     req.ModelName,
		InputS3URI:    req.TransformInput.DataSource.S3DataSource.S3Uri,
		OutputS3URI:   req.TransformOutput.S3OutputPath,
		InstanceType:  req.TransformResources.InstanceType,
		InstanceCount: req.TransformResources.InstanceCount,
		Tags:          toTags(req.Tags),
		Echo:          echo,
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

	fallback := map[string]any{
		"TransformInput": map[string]any{"DataSource": map[string]any{"S3DataSource": map[string]any{
			"S3DataType": "S3Prefix", "S3Uri": job.InputS3URI,
		}}},
		"TransformOutput":    map[string]any{"S3OutputPath": job.OutputS3URI},
		"TransformResources": map[string]any{"InstanceType": job.InstanceType, "InstanceCount": job.InstanceCount},
	}

	// A real DescribeTransformJob has no LastModifiedTime.
	computed := map[string]any{
		"TransformJobName":   job.JobName,
		"TransformJobArn":    job.JobARN,
		"TransformJobStatus": job.Status,
		"ModelName":          job.ModelName,
		"CreationTime":       epoch(job.CreationTime),
	}
	putTime(computed, "TransformStartTime", job.CreationTime)
	putTime(computed, "TransformEndTime", job.TransformEndTime)
	setIf(computed, "FailureReason", job.FailureReason)

	wire.WriteJSON(w, describeBody(fallback, job.Echo, computed))
}

//nolint:dupl // per-resource shim mirrors the SDK shape
func (h *Handler) listTransformJobs(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	jobs, err := h.svc.ListTransformJobs(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "TransformJobSummaries", jobs, func(j *driver.TransformJob) map[string]any {
		m := map[string]any{
			"TransformJobName":   j.JobName,
			"TransformJobArn":    j.JobARN,
			"TransformJobStatus": j.Status,
			"CreationTime":       epoch(j.CreationTime),
			"LastModifiedTime":   epoch(j.LastModifiedTime),
		}
		putTime(m, "TransformEndTime", j.TransformEndTime)
		setIf(m, "FailureReason", j.FailureReason)

		return m
	})
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

	echo, ok := decodeJobRequest(w, r, &req)
	if !ok {
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
		Echo:            echo,
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"HyperParameterTuningJobArn": job.JobARN})
}

// tuningCounters renders the training-job and objective counters of a tuning
// job from its observed counts.
func tuningCounters(counts map[string]int) (training, objective map[string]any) {
	training = map[string]any{
		"Completed": counts["Completed"], "InProgress": counts["InProgress"],
		"RetryableError": counts["RetryableError"], "NonRetryableError": counts["NonRetryableError"],
		"Stopped": counts["Stopped"],
	}
	objective = map[string]any{"Succeeded": counts["Completed"], "Pending": counts["InProgress"], "Failed": 0}

	return training, objective
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

	training, objective := tuningCounters(job.TrainingJobCounts)

	fallback := map[string]any{
		"HyperParameterTuningJobConfig": map[string]any{
			"Strategy": job.Strategy,
			"ResourceLimits": map[string]any{
				"MaxNumberOfTrainingJobs": job.MaxJobs, "MaxParallelTrainingJobs": job.MaxParallelJobs,
			},
			"HyperParameterTuningJobObjective": map[string]any{"Type": job.ObjectiveType, "MetricName": job.ObjectiveMetric},
		},
	}

	computed := map[string]any{
		"HyperParameterTuningJobName":   job.JobName,
		"HyperParameterTuningJobArn":    job.JobARN,
		"HyperParameterTuningJobStatus": job.Status,
		"CreationTime":                  epoch(job.CreationTime),
		"LastModifiedTime":              epoch(job.LastModifiedTime),
		"TrainingJobStatusCounters":     training,
		"ObjectiveStatusCounters":       objective,
	}
	putTime(computed, "HyperParameterTuningEndTime", job.HPOJobEndTime)
	setIf(computed, "FailureReason", job.FailureReason)

	if job.BestTrainingJob != "" {
		computed["BestTrainingJob"] = map[string]any{
			"TrainingJobName":   job.BestTrainingJob,
			"TrainingJobStatus": driver.JobCompleted,
			"CreationTime":      epoch(job.CreationTime),
			"TuningJobName":     job.JobName,
		}
	}

	wire.WriteJSON(w, describeBody(fallback, job.Echo, computed))
}

func (h *Handler) listTuningJobs(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	jobs, err := h.svc.ListHyperParameterTuningJobs(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "HyperParameterTuningJobSummaries", jobs, func(j *driver.HyperParameterTuningJob) map[string]any {
		training, objective := tuningCounters(j.TrainingJobCounts)
		m := map[string]any{
			"HyperParameterTuningJobName":   j.JobName,
			"HyperParameterTuningJobArn":    j.JobARN,
			"HyperParameterTuningJobStatus": j.Status,
			"Strategy":                      j.Strategy,
			"CreationTime":                  epoch(j.CreationTime),
			"LastModifiedTime":              epoch(j.LastModifiedTime),
			"TrainingJobStatusCounters":     training,
			"ObjectiveStatusCounters":       objective,
			"ResourceLimits": map[string]any{
				"MaxNumberOfTrainingJobs": j.MaxJobs, "MaxParallelTrainingJobs": j.MaxParallelJobs,
			},
		}
		putTime(m, "HyperParameterTuningEndTime", j.HPOJobEndTime)

		return m
	})
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

	echo, ok := decodeJobRequest(w, r, &req)
	if !ok {
		return
	}

	job, err := h.svc.CreateAutoMLJobV2(r.Context(), driver.AutoMLJobConfig{
		JobName:     req.AutoMLJobName,
		RoleARN:     req.RoleArn,
		OutputS3URI: req.OutputDataConfig.S3OutputPath,
		Tags:        toTags(req.Tags),
		Echo:        echo,
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

	fallback := map[string]any{
		"RoleArn":                  job.RoleARN,
		"OutputDataConfig":         map[string]any{"S3OutputPath": job.OutputS3URI},
		"AutoMLJobInputDataConfig": []any{},
	}

	computed := map[string]any{
		"AutoMLJobName":            job.JobName,
		"AutoMLJobArn":             job.JobARN,
		"AutoMLJobStatus":          job.Status,
		"AutoMLJobSecondaryStatus": job.SecondaryStatus,
		"CreationTime":             epoch(job.CreationTime),
		"LastModifiedTime":         epoch(job.LastModifiedTime),
	}
	putTime(computed, "EndTime", job.AutoMLJobEndTime)
	setIf(computed, "FailureReason", job.FailureReason)

	if job.BestCandidateName != "" {
		computed["BestCandidate"] = map[string]any{
			"CandidateName":   job.BestCandidateName,
			"CandidateStatus": "Completed",
			"ObjectiveStatus": "Succeeded",
			"CandidateSteps":  []any{},
			"CreationTime":    epoch(job.CreationTime),
		}
	}

	wire.WriteJSON(w, describeBody(fallback, job.Echo, computed))
}

func (h *Handler) listAutoMLJobs(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	jobs, err := h.svc.ListAutoMLJobs(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "AutoMLJobSummaries", jobs, func(j *driver.AutoMLJob) map[string]any {
		m := map[string]any{
			"AutoMLJobName":            j.JobName,
			"AutoMLJobArn":             j.JobARN,
			"AutoMLJobStatus":          j.Status,
			"AutoMLJobSecondaryStatus": j.SecondaryStatus,
			"CreationTime":             epoch(j.CreationTime),
			"LastModifiedTime":         epoch(j.LastModifiedTime),
		}
		putTime(m, "EndTime", j.AutoMLJobEndTime)
		setIf(m, "FailureReason", j.FailureReason)

		return m
	})
}

func (h *Handler) stopAutoMLJob(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "AutoMLJobName", h.svc.StopAutoMLJob)
}

// --- Labeling ---

func (h *Handler) createLabelingJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LabelingJobName    string `json:"LabelingJobName"`
		RoleArn            string `json:"RoleArn"`
		LabelAttributeName string `json:"LabelAttributeName"`
		InputConfig        struct {
			DataSource struct {
				S3DataSource struct {
					ManifestS3Uri string `json:"ManifestS3Uri"`
				} `json:"S3DataSource"`
			} `json:"DataSource"`
		} `json:"InputConfig"`
		OutputConfig struct {
			S3OutputPath string `json:"S3OutputPath"`
		} `json:"OutputConfig"`
		HumanTaskConfig struct {
			WorkteamArn string `json:"WorkteamArn"`
		} `json:"HumanTaskConfig"`
		Tags []wireTag `json:"Tags"`
	}

	echo, ok := decodeJobRequest(w, r, &req)
	if !ok {
		return
	}

	job, err := h.svc.CreateLabelingJob(r.Context(), driver.LabelingJobConfig{
		JobName:        req.LabelingJobName,
		RoleARN:        req.RoleArn,
		InputS3URI:     req.InputConfig.DataSource.S3DataSource.ManifestS3Uri,
		OutputS3URI:    req.OutputConfig.S3OutputPath,
		LabelAttribute: req.LabelAttributeName,
		WorkteamARN:    req.HumanTaskConfig.WorkteamArn,
		Tags:           toTags(req.Tags),
		Echo:           echo,
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"LabelingJobArn": job.JobARN})
}

func labelCounters(labeled int) map[string]any {
	return map[string]any{"TotalLabeled": labeled, "HumanLabeled": labeled, "MachineLabeled": 0, "FailedNonRetryableError": 0, "Unlabeled": 0}
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

	fallback := map[string]any{
		"RoleArn": job.RoleARN,
		"InputConfig": map[string]any{"DataSource": map[string]any{"S3DataSource": map[string]any{
			"ManifestS3Uri": job.InputS3URI,
		}}},
		"OutputConfig": map[string]any{"S3OutputPath": job.OutputS3URI},
		"HumanTaskConfig": map[string]any{
			"WorkteamArn": job.WorkteamARN, "TaskTitle": job.JobName, "TaskDescription": job.JobName,
			"NumberOfHumanWorkersPerDataObject": 1, "TaskTimeLimitInSeconds": 3600,
			"PreHumanTaskLambdaArn":         "",
			"UiConfig":                      map[string]any{"UiTemplateS3Uri": ""},
			"AnnotationConsolidationConfig": map[string]any{"AnnotationConsolidationLambdaArn": ""},
		},
	}

	computed := map[string]any{
		"LabelingJobName":   job.JobName,
		"LabelingJobArn":    job.JobARN,
		"LabelingJobStatus": job.Status,
		"CreationTime":      epoch(job.CreationTime),
		"LastModifiedTime":  epoch(job.LastModifiedTime),
		"JobReferenceCode":  driver.JobReferenceCode(job.JobName),
		"LabelCounters":     labelCounters(job.LabeledObjects),
	}
	setIf(computed, "LabelAttributeName", job.LabelAttribute)
	setIf(computed, "FailureReason", job.FailureReason)

	wire.WriteJSON(w, describeBody(fallback, job.Echo, computed))
}

func (h *Handler) listLabelingJobs(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	jobs, err := h.svc.ListLabelingJobs(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "LabelingJobSummaryList", jobs, func(j *driver.LabelingJob) map[string]any {
		m := map[string]any{
			"LabelingJobName":   j.JobName,
			"LabelingJobArn":    j.JobARN,
			"LabelingJobStatus": j.Status,
			"CreationTime":      epoch(j.CreationTime),
			"LastModifiedTime":  epoch(j.LastModifiedTime),
			"LabelCounters":     labelCounters(j.LabeledObjects),
			"WorkteamArn":       j.WorkteamARN,
		}
		setIf(m, "FailureReason", j.FailureReason)

		return m
	})
}

func (h *Handler) stopLabelingJob(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "LabelingJobName", h.svc.StopLabelingJob)
}

// --- Compilation ---

func (h *Handler) createCompilationJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CompilationJobName string `json:"CompilationJobName"`
		RoleArn            string `json:"RoleArn"`
		InputConfig        struct {
			S3Uri     string `json:"S3Uri"`
			Framework string `json:"Framework"`
		} `json:"InputConfig"`
		OutputConfig struct {
			S3OutputLocation string `json:"S3OutputLocation"`
			TargetDevice     string `json:"TargetDevice"`
		} `json:"OutputConfig"`
		Tags []wireTag `json:"Tags"`
	}

	echo, ok := decodeJobRequest(w, r, &req)
	if !ok {
		return
	}

	job, err := h.svc.CreateCompilationJob(r.Context(), driver.CompilationJobConfig{
		JobName:      req.CompilationJobName,
		RoleARN:      req.RoleArn,
		InputS3URI:   req.InputConfig.S3Uri,
		OutputS3URI:  req.OutputConfig.S3OutputLocation,
		TargetDevice: req.OutputConfig.TargetDevice,
		Framework:    req.InputConfig.Framework,
		Tags:         toTags(req.Tags),
		Echo:         echo,
	})
	if err != nil {
		writeDriverError(w, err)

		return
	}

	wire.WriteJSON(w, map[string]any{"CompilationJobArn": job.JobARN})
}

// defaultCompilationRuntimeSeconds is the default compilation StoppingCondition.
const defaultCompilationRuntimeSeconds = 900

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

	fallback := map[string]any{
		"RoleArn":           job.RoleARN,
		"InputConfig":       map[string]any{"S3Uri": job.InputS3URI, "DataInputConfig": "{}", "Framework": job.Framework},
		"OutputConfig":      map[string]any{"S3OutputLocation": job.OutputS3URI, "TargetDevice": job.TargetDevice},
		"StoppingCondition": map[string]any{"MaxRuntimeInSeconds": defaultCompilationRuntimeSeconds},
	}

	// FailureReason is a required member of DescribeCompilationJob: it is "" when
	// the job did not fail.
	computed := map[string]any{
		"CompilationJobName":   job.JobName,
		"CompilationJobArn":    job.JobARN,
		"CompilationJobStatus": job.Status,
		"CreationTime":         epoch(job.CreationTime),
		"LastModifiedTime":     epoch(job.LastModifiedTime),
		"FailureReason":        job.FailureReason,
		"ModelArtifacts":       map[string]any{"S3ModelArtifacts": job.OutputS3URI},
	}
	putTime(computed, "CompilationStartTime", job.CreationTime)
	putTime(computed, "CompilationEndTime", job.CompilationEndTime)

	wire.WriteJSON(w, describeBody(fallback, job.Echo, computed))
}

//nolint:dupl // per-resource shim mirrors the SDK shape
func (h *Handler) listCompilationJobs(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeListReq(w, r)
	if !ok {
		return
	}

	jobs, err := h.svc.ListCompilationJobs(r.Context(), req.options())
	if err != nil {
		writeDriverError(w, err)

		return
	}

	writeSummaries(w, "CompilationJobSummaries", jobs, func(j *driver.CompilationJob) map[string]any {
		m := map[string]any{
			"CompilationJobName":   j.JobName,
			"CompilationJobArn":    j.JobARN,
			"CompilationJobStatus": j.Status,
			"CreationTime":         epoch(j.CreationTime),
			"LastModifiedTime":     epoch(j.LastModifiedTime),
		}
		putTime(m, "CompilationEndTime", j.CompilationEndTime)
		setIf(m, "CompilationTargetDevice", j.TargetDevice)

		return m
	})
}

func (h *Handler) stopCompilationJob(w http.ResponseWriter, r *http.Request) {
	stopByName(w, r, "CompilationJobName", h.svc.StopCompilationJob)
}
