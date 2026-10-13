package sagemaker

import (
	"context"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/awsevents"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// EventBridge identity of SageMaker's native state-change events. See
// "Automating Amazon SageMaker AI with Amazon EventBridge" in the SageMaker
// developer guide.
const (
	eventSource = "aws.sagemaker"

	eventTrainingJob  = "SageMaker Training Job State Change"
	eventProcessing   = "SageMaker Processing Job State Change"
	eventTransformJob = "SageMaker Transform Job State Change"
	eventTuningJob    = "SageMaker HyperParameter Tuning Job State Change"
	eventEndpoint     = "SageMaker Endpoint State Change"
	eventModelPackage = "SageMaker Model Package State Change"
	eventPipelineExec = "SageMaker Model Building Pipeline Execution Status Change"
)

// SetEventPublisher wires the event bus SageMaker publishes its state-change
// events to. Unwired, events are not emitted and every operation behaves as
// before.
func (m *Mock) SetEventPublisher(p awsevents.Publisher) {
	m.events.SetPublisher(p)
}

// asyncEvents reports whether state changes are published one at a time: under
// --async-settle a resource's later states are published when it settles.
func (m *Mock) asyncEvents() bool {
	return m.opts.SettleDuration(settle.DefaultJobStageSettle) > 0 || m.opts.SettleDuration(settle.DefaultEndpointSettle) > 0
}

// firstSettle reports true exactly once per key, so a settled state is
// published once however often the resource is read afterwards.
func (m *Mock) firstSettle(key string) bool {
	m.evMu.Lock()
	defer m.evMu.Unlock()

	if _, done := m.settledEmitted[key]; done {
		return false
	}

	m.settledEmitted[key] = struct{}{}

	return true
}

// publishSettled publishes a job's terminal state once, the first time it is
// observed settled under --async-settle. There is no scheduler in the mock, so a
// settled state is noticed by the read that finds it; without async settle every
// state was already published at the call.
func (m *Mock) publishSettled(arn, status string, terminal bool, emit func(state string)) {
	if !terminal || !m.asyncEvents() || !m.firstSettle(arn+"|"+status) {
		return
	}

	emit(status)
}

// emitStates publishes one event per state, in order. Without --async-settle a
// resource passes through its states instantly, so every state is published
// back to back (real SageMaker may send several events per change). Under
// --async-settle only the first state is published at call time; the terminal
// state is published once when the resource is first read settled
// (publishSettled, and the endpoint observer).
func (m *Mock) emitStates(ctx context.Context, detailType string, resources, states []string, detail func(state string) any) {
	if m.asyncEvents() {
		states = states[:1]
	}

	for _, s := range states {
		m.events.Emit(ctx, eventSource, detailType, detail(s), resources...)
	}
}

// epochMillis converts a stored RFC3339 time to epoch milliseconds (0 when
// unparsable).
func epochMillis(iso string) int64 {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return 0
	}

	return t.UnixMilli()
}

// millisString is epochMillis rendered as a string: SageMaker's training-job
// and tuning-job events carry their times as strings, processing and endpoint
// events as numbers, the others as ISO-8601. An unset time is "", which the
// event structs omit, instead of the string "0".
func millisString(iso string) string {
	ms := epochMillis(iso)
	if ms == 0 {
		return ""
	}

	return strconv.FormatInt(ms, 10)
}

// tagMap renders API tags the way events carry them: a key/value object.
func tagMap(tags []driver.Tag) map[string]string {
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

type trainingJobEvent struct {
	TrainingJobName        string            `json:"TrainingJobName"`
	TrainingJobArn         string            `json:"TrainingJobArn"`
	TrainingJobStatus      string            `json:"TrainingJobStatus"`
	SecondaryStatus        string            `json:"SecondaryStatus"`
	HyperParameters        map[string]string `json:"HyperParameters"`
	AlgorithmSpecification map[string]string `json:"AlgorithmSpecification"`
	RoleArn                string            `json:"RoleArn"`
	ResourceConfig         resourceEvent     `json:"ResourceConfig"`
	CreationTime           string            `json:"CreationTime"`
	TrainingStartTime      string            `json:"TrainingStartTime,omitempty"`
	TrainingEndTime        string            `json:"TrainingEndTime,omitempty"`
	LastModifiedTime       string            `json:"LastModifiedTime"`
	FailureReason          string            `json:"FailureReason,omitempty"`
	Tags                   map[string]string `json:"Tags"`
}

type resourceEvent struct {
	InstanceType   string `json:"InstanceType"`
	InstanceCount  int    `json:"InstanceCount"`
	VolumeSizeInGB int    `json:"VolumeSizeInGB"`
}

func (m *Mock) emitTrainingEvents(ctx context.Context, j *driver.TrainingJob, states ...string) {
	m.emitStates(ctx, eventTrainingJob, []string{j.JobARN}, states, func(state string) any {
		secondary := j.SecondaryStatus
		if state == driver.JobInProgress {
			secondary = driver.SecondaryStarting
		}

		return trainingJobEvent{
			TrainingJobName: j.JobName, TrainingJobArn: j.JobARN, TrainingJobStatus: state, SecondaryStatus: secondary,
			HyperParameters:        j.HyperParameters,
			AlgorithmSpecification: map[string]string{"TrainingImage": j.AlgorithmImage},
			RoleArn:                j.RoleARN,
			ResourceConfig: resourceEvent{
				InstanceType: j.Resources.InstanceType, InstanceCount: j.Resources.InstanceCount, VolumeSizeInGB: j.Resources.VolumeSizeInGB,
			},
			CreationTime: millisString(j.CreationTime), TrainingStartTime: millisString(j.TrainingStartTime),
			TrainingEndTime: millisString(j.TrainingEndTime), LastModifiedTime: millisString(j.LastModifiedTime),
			FailureReason: j.FailureReason, Tags: tagMap(j.Tags),
		}
	})
}

type processingJobEvent struct {
	ProcessingJobName   string            `json:"ProcessingJobName"`
	ProcessingJobArn    string            `json:"ProcessingJobArn"`
	ProcessingJobStatus string            `json:"ProcessingJobStatus"`
	RoleArn             string            `json:"RoleArn"`
	AppSpecification    map[string]string `json:"AppSpecification"`
	ProcessingResources resourceEvent     `json:"ProcessingResources"`
	CreationTime        int64             `json:"CreationTime"`
	ProcessingEndTime   int64             `json:"ProcessingEndTime,omitempty"`
	LastModifiedTime    int64             `json:"LastModifiedTime"`
	FailureReason       string            `json:"FailureReason,omitempty"`
}

func (m *Mock) emitProcessingEvents(ctx context.Context, j *driver.ProcessingJob, states ...string) {
	m.emitStates(ctx, eventProcessing, []string{j.JobARN}, states, func(state string) any {
		return processingJobEvent{
			ProcessingJobName: j.JobName, ProcessingJobArn: j.JobARN, ProcessingJobStatus: state, RoleArn: j.RoleARN,
			AppSpecification: map[string]string{"ImageUri": j.AppImage},
			ProcessingResources: resourceEvent{
				InstanceType: j.Resources.InstanceType, InstanceCount: j.Resources.InstanceCount, VolumeSizeInGB: j.Resources.VolumeSizeInGB,
			},
			CreationTime: epochMillis(j.CreationTime), ProcessingEndTime: epochMillis(j.ProcessingEndTime),
			LastModifiedTime: epochMillis(j.LastModifiedTime), FailureReason: j.FailureReason,
		}
	})
}

type transformJobEvent struct {
	TransformJobName   string            `json:"TransformJobName"`
	TransformJobArn    string            `json:"TransformJobArn"`
	TransformJobStatus string            `json:"TransformJobStatus"`
	ModelName          string            `json:"ModelName"`
	TransformResources map[string]any    `json:"TransformResources"`
	CreationTime       string            `json:"CreationTime"`
	TransformEndTime   string            `json:"TransformEndTime,omitempty"`
	FailureReason      string            `json:"FailureReason,omitempty"`
	Tags               map[string]string `json:"Tags"`
}

func (m *Mock) emitTransformEvents(ctx context.Context, j *driver.TransformJob, states ...string) {
	m.emitStates(ctx, eventTransformJob, []string{j.JobARN}, states, func(state string) any {
		return transformJobEvent{
			TransformJobName: j.JobName, TransformJobArn: j.JobARN, TransformJobStatus: state, ModelName: j.ModelName,
			TransformResources: map[string]any{"InstanceType": j.InstanceType, "InstanceCount": j.InstanceCount},
			CreationTime:       j.CreationTime, TransformEndTime: j.TransformEndTime,
			FailureReason: j.FailureReason, Tags: tagMap(j.Tags),
		}
	})
}

type tuningJobEvent struct {
	HyperParameterTuningJobName   string            `json:"HyperParameterTuningJobName"`
	HyperParameterTuningJobArn    string            `json:"HyperParameterTuningJobArn"`
	HyperParameterTuningJobStatus string            `json:"HyperParameterTuningJobStatus"`
	CreationTime                  string            `json:"CreationTime"`
	LastModifiedTime              string            `json:"LastModifiedTime"`
	TrainingJobStatusCounters     map[string]int    `json:"TrainingJobStatusCounters"`
	FailureReason                 string            `json:"FailureReason,omitempty"`
	Tags                          map[string]string `json:"Tags"`
}

func (m *Mock) emitTuningEvents(ctx context.Context, j *driver.HyperParameterTuningJob, states ...string) {
	m.emitStates(ctx, eventTuningJob, []string{j.JobARN}, states, func(state string) any {
		return tuningJobEvent{
			HyperParameterTuningJobName: j.JobName, HyperParameterTuningJobArn: j.JobARN, HyperParameterTuningJobStatus: state,
			CreationTime: j.CreationTime, LastModifiedTime: j.LastModifiedTime,
			TrainingJobStatusCounters: j.TrainingJobCounts, FailureReason: j.FailureReason, Tags: tagMap(j.Tags),
		}
	})
}

// endpointEventStatus is the status literal SageMaker's Endpoint State Change
// event carries. The developer guide's example shows upper-snake values
// ("IN_SERVICE"), unlike the API's "InService"; the example is the only
// documented evidence, so the event follows it.
func endpointEventStatus(status string) string {
	switch status {
	case driver.EndpointInService:
		return "IN_SERVICE"
	case driver.EndpointCreating:
		return "CREATING"
	case driver.EndpointUpdating:
		return "UPDATING"
	case driver.EndpointDeleting:
		return "DELETING"
	default:
		return status
	}
}

type endpointEvent struct {
	EndpointName       string            `json:"EndpointName"`
	EndpointArn        string            `json:"EndpointArn"`
	EndpointConfigName string            `json:"EndpointConfigName"`
	ProductionVariants []variantEvent    `json:"ProductionVariants"`
	EndpointStatus     string            `json:"EndpointStatus"`
	CreationTime       int64             `json:"CreationTime"`
	LastModifiedTime   int64             `json:"LastModifiedTime"`
	FailureReason      string            `json:"FailureReason,omitempty"`
	Tags               map[string]string `json:"Tags"`
}

type variantEvent struct {
	DesiredWeight        float64 `json:"DesiredWeight"`
	DesiredInstanceCount int     `json:"DesiredInstanceCount"`
}

func (m *Mock) emitEndpointEvents(ctx context.Context, ep *driver.Endpoint, states ...string) {
	m.emitStates(ctx, eventEndpoint, []string{ep.EndpointARN}, states, func(state string) any {
		variants := make([]variantEvent, 0, len(ep.VariantSummaries))
		for _, v := range ep.VariantSummaries {
			variants = append(variants, variantEvent{DesiredWeight: v.DesiredWeight, DesiredInstanceCount: v.DesiredInstanceCount})
		}

		return endpointEvent{
			EndpointName: ep.EndpointName, EndpointArn: ep.EndpointARN, EndpointConfigName: ep.ConfigName,
			ProductionVariants: variants, EndpointStatus: endpointEventStatus(state),
			CreationTime: epochMillis(ep.CreationTime), LastModifiedTime: epochMillis(ep.LastModifiedTime),
			FailureReason: ep.FailureReason, Tags: tagMap(ep.Tags),
		}
	})
}

type modelPackageEvent struct {
	ModelPackageGroupName     string   `json:"ModelPackageGroupName"`
	ModelPackageVersion       int      `json:"ModelPackageVersion"`
	ModelPackageArn           string   `json:"ModelPackageArn"`
	CreationTime              string   `json:"CreationTime"`
	ModelPackageStatus        string   `json:"ModelPackageStatus"`
	ModelApprovalStatus       string   `json:"ModelApprovalStatus"`
	UpdatedModelPackageFields []string `json:"UpdatedModelPackageFields,omitempty"`
}

func (m *Mock) emitModelPackageEvent(ctx context.Context, p *driver.ModelPackage, updated ...string) {
	m.events.Emit(ctx, eventSource, eventModelPackage, modelPackageEvent{
		ModelPackageGroupName: p.GroupName, ModelPackageVersion: p.Version, ModelPackageArn: p.PackageARN,
		CreationTime: p.CreationTime, ModelPackageStatus: p.Status, ModelApprovalStatus: p.ApprovalStatus,
		UpdatedModelPackageFields: updated,
	}, p.PackageARN)
}

// pipelineExecutionEvent uses camelCase keys, unlike every other SageMaker
// event.
type pipelineExecutionEvent struct {
	CurrentStatus     string `json:"currentPipelineExecutionStatus"`
	PreviousStatus    string `json:"previousPipelineExecutionStatus"`
	ExecutionStart    string `json:"executionStartTime"`
	ExecutionEnd      string `json:"executionEndTime,omitempty"`
	PipelineArn       string `json:"pipelineArn"`
	PipelineExecution string `json:"pipelineExecutionArn"`
}

// emitPipelineExecutionEvents publishes a pipeline execution's status changes;
// each event's previous status is the one before it ("" for the first).
func (m *Mock) emitPipelineExecutionEvents(ctx context.Context, ex *driver.PipelineExecution, states ...string) {
	pipelineARN := m.arn("pipeline/" + ex.PipelineName)
	previous := ""

	for _, s := range states {
		end := ""
		if s != driver.ExecutionExecuting && s != driver.ExecutionStopping {
			end = ex.EndTime
		}

		m.events.Emit(ctx, eventSource, eventPipelineExec, pipelineExecutionEvent{
			CurrentStatus: s, PreviousStatus: previous, ExecutionStart: ex.StartTime, ExecutionEnd: end,
			PipelineArn: pipelineARN, PipelineExecution: ex.ExecutionARN,
		}, pipelineARN, ex.ExecutionARN)

		previous = s
	}
}
