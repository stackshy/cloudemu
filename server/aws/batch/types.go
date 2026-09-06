package batch

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

// computeEnvironmentOrderWire is the wire form of a job queue's ordered
// compute-environment association.
type computeEnvironmentOrderWire struct {
	Order              int32  `json:"order"`
	ComputeEnvironment string `json:"computeEnvironment"`
}

func ordersToDriver(in []computeEnvironmentOrderWire) []driver.ComputeEnvironmentOrder {
	if in == nil {
		return nil
	}

	out := make([]driver.ComputeEnvironmentOrder, len(in))
	for i, o := range in {
		out[i] = driver.ComputeEnvironmentOrder{Order: o.Order, ComputeEnvironment: o.ComputeEnvironment}
	}

	return out
}

func ordersToWire(in []driver.ComputeEnvironmentOrder) []computeEnvironmentOrderWire {
	if in == nil {
		return nil
	}

	out := make([]computeEnvironmentOrderWire, len(in))
	for i, o := range in {
		out[i] = computeEnvironmentOrderWire{Order: o.Order, ComputeEnvironment: o.ComputeEnvironment}
	}

	return out
}

// --- Compute environments ---

type createComputeEnvironmentRequest struct {
	ComputeEnvironmentName string            `json:"computeEnvironmentName"`
	Type                   string            `json:"type"`
	State                  string            `json:"state"`
	ComputeResources       json.RawMessage   `json:"computeResources"`
	ServiceRole            string            `json:"serviceRole"`
	UnmanagedvCpus         *int32            `json:"unmanagedvCpus"`
	Tags                   map[string]string `json:"tags"`
}

type computeEnvironmentNameArnResponse struct {
	ComputeEnvironmentName string `json:"computeEnvironmentName"`
	ComputeEnvironmentArn  string `json:"computeEnvironmentArn"`
}

type describeComputeEnvironmentsRequest struct {
	ComputeEnvironments []string `json:"computeEnvironments"`
}

type computeEnvironmentDetail struct {
	ComputeEnvironmentName string            `json:"computeEnvironmentName"`
	ComputeEnvironmentArn  string            `json:"computeEnvironmentArn"`
	EcsClusterArn          string            `json:"ecsClusterArn,omitempty"`
	Type                   string            `json:"type,omitempty"`
	State                  string            `json:"state,omitempty"`
	Status                 string            `json:"status,omitempty"`
	StatusReason           string            `json:"statusReason,omitempty"`
	ComputeResources       json.RawMessage   `json:"computeResources,omitempty"`
	ServiceRole            string            `json:"serviceRole,omitempty"`
	UUID                   string            `json:"uuid,omitempty"`
	UnmanagedvCpus         *int32            `json:"unmanagedvCpus,omitempty"`
	Tags                   map[string]string `json:"tags,omitempty"`
}

type describeComputeEnvironmentsResponse struct {
	ComputeEnvironments []computeEnvironmentDetail `json:"computeEnvironments"`
}

func toCEDetail(ce *driver.ComputeEnvironment) computeEnvironmentDetail {
	return computeEnvironmentDetail{
		ComputeEnvironmentName: ce.Name,
		ComputeEnvironmentArn:  ce.ARN,
		EcsClusterArn:          ce.EcsClusterARN,
		Type:                   ce.Type,
		State:                  ce.State,
		Status:                 ce.Status,
		StatusReason:           ce.StatusReason,
		ComputeResources:       ce.ComputeResources,
		ServiceRole:            ce.ServiceRole,
		UUID:                   ce.UUID,
		UnmanagedvCpus:         ce.UnmanagedvCpus,
		Tags:                   ce.Tags,
	}
}

type updateComputeEnvironmentRequest struct {
	ComputeEnvironment string          `json:"computeEnvironment"`
	State              string          `json:"state"`
	ServiceRole        string          `json:"serviceRole"`
	ComputeResources   json.RawMessage `json:"computeResources"`
}

type deleteComputeEnvironmentRequest struct {
	ComputeEnvironment string `json:"computeEnvironment"`
}

// --- Job queues ---

type createJobQueueRequest struct {
	JobQueueName            string                        `json:"jobQueueName"`
	State                   string                        `json:"state"`
	Priority                *int32                        `json:"priority"`
	ComputeEnvironmentOrder []computeEnvironmentOrderWire `json:"computeEnvironmentOrder"`
	SchedulingPolicyArn     string                        `json:"schedulingPolicyArn"`
	Tags                    map[string]string             `json:"tags"`
}

type jobQueueNameArnResponse struct {
	JobQueueName string `json:"jobQueueName"`
	JobQueueArn  string `json:"jobQueueArn"`
}

type describeJobQueuesRequest struct {
	JobQueues []string `json:"jobQueues"`
}

type jobQueueDetail struct {
	JobQueueName            string                        `json:"jobQueueName"`
	JobQueueArn             string                        `json:"jobQueueArn"`
	State                   string                        `json:"state,omitempty"`
	Status                  string                        `json:"status,omitempty"`
	StatusReason            string                        `json:"statusReason,omitempty"`
	Priority                int32                         `json:"priority"`
	ComputeEnvironmentOrder []computeEnvironmentOrderWire `json:"computeEnvironmentOrder,omitempty"`
	SchedulingPolicyArn     string                        `json:"schedulingPolicyArn,omitempty"`
	Tags                    map[string]string             `json:"tags,omitempty"`
}

type describeJobQueuesResponse struct {
	JobQueues []jobQueueDetail `json:"jobQueues"`
}

func toJQDetail(q *driver.JobQueue) jobQueueDetail {
	return jobQueueDetail{
		JobQueueName:            q.Name,
		JobQueueArn:             q.ARN,
		State:                   q.State,
		Status:                  q.Status,
		StatusReason:            q.StatusReason,
		Priority:                q.Priority,
		ComputeEnvironmentOrder: ordersToWire(q.ComputeEnvironmentOrder),
		SchedulingPolicyArn:     q.SchedulingPolicyARN,
		Tags:                    q.Tags,
	}
}

type updateJobQueueRequest struct {
	JobQueue                string                        `json:"jobQueue"`
	State                   *string                       `json:"state"`
	Priority                *int32                        `json:"priority"`
	ComputeEnvironmentOrder []computeEnvironmentOrderWire `json:"computeEnvironmentOrder"`
	SchedulingPolicyArn     *string                       `json:"schedulingPolicyArn"`
}

type deleteJobQueueRequest struct {
	JobQueue string `json:"jobQueue"`
}

// --- Job definitions ---

type registerJobDefinitionRequest struct {
	JobDefinitionName    string            `json:"jobDefinitionName"`
	Type                 string            `json:"type"`
	ContainerProperties  json.RawMessage   `json:"containerProperties"`
	NodeProperties       json.RawMessage   `json:"nodeProperties"`
	EcsProperties        json.RawMessage   `json:"ecsProperties"`
	EksProperties        json.RawMessage   `json:"eksProperties"`
	RetryStrategy        json.RawMessage   `json:"retryStrategy"`
	Timeout              json.RawMessage   `json:"timeout"`
	Parameters           map[string]string `json:"parameters"`
	PlatformCapabilities []string          `json:"platformCapabilities"`
	PropagateTags        *bool             `json:"propagateTags"`
	SchedulingPriority   *int32            `json:"schedulingPriority"`
	Tags                 map[string]string `json:"tags"`
}

type registerJobDefinitionResponse struct {
	JobDefinitionName string `json:"jobDefinitionName"`
	JobDefinitionArn  string `json:"jobDefinitionArn"`
	Revision          int32  `json:"revision"`
}

type describeJobDefinitionsRequest struct {
	JobDefinitions    []string `json:"jobDefinitions"`
	JobDefinitionName string   `json:"jobDefinitionName"`
	Status            string   `json:"status"`
}

type jobDefinitionDetail struct {
	JobDefinitionName    string            `json:"jobDefinitionName"`
	JobDefinitionArn     string            `json:"jobDefinitionArn"`
	Revision             int32             `json:"revision"`
	Status               string            `json:"status,omitempty"`
	Type                 string            `json:"type,omitempty"`
	ContainerProperties  json.RawMessage   `json:"containerProperties,omitempty"`
	NodeProperties       json.RawMessage   `json:"nodeProperties,omitempty"`
	EcsProperties        json.RawMessage   `json:"ecsProperties,omitempty"`
	EksProperties        json.RawMessage   `json:"eksProperties,omitempty"`
	RetryStrategy        json.RawMessage   `json:"retryStrategy,omitempty"`
	Timeout              json.RawMessage   `json:"timeout,omitempty"`
	Parameters           map[string]string `json:"parameters,omitempty"`
	PlatformCapabilities []string          `json:"platformCapabilities,omitempty"`
	PropagateTags        *bool             `json:"propagateTags,omitempty"`
	SchedulingPriority   *int32            `json:"schedulingPriority,omitempty"`
	Tags                 map[string]string `json:"tags,omitempty"`
}

type describeJobDefinitionsResponse struct {
	JobDefinitions []jobDefinitionDetail `json:"jobDefinitions"`
}

func toJDDetail(jd *driver.JobDefinition) jobDefinitionDetail {
	return jobDefinitionDetail{
		JobDefinitionName:    jd.Name,
		JobDefinitionArn:     jd.ARN,
		Revision:             jd.Revision,
		Status:               jd.Status,
		Type:                 jd.Type,
		ContainerProperties:  jd.ContainerProperties,
		NodeProperties:       jd.NodeProperties,
		EcsProperties:        jd.EcsProperties,
		EksProperties:        jd.EksProperties,
		RetryStrategy:        jd.RetryStrategy,
		Timeout:              jd.Timeout,
		Parameters:           jd.Parameters,
		PlatformCapabilities: jd.PlatformCapabilities,
		PropagateTags:        jd.PropagateTags,
		SchedulingPriority:   jd.SchedulingPriority,
		Tags:                 jd.Tags,
	}
}

type deregisterJobDefinitionRequest struct {
	JobDefinition string `json:"jobDefinition"`
}

// --- Tags ---

type listTagsForResourceResponse struct {
	Tags map[string]string `json:"tags"`
}

type tagResourceRequest struct {
	Tags map[string]string `json:"tags"`
}

// emptyResponse is the {} body Batch returns for mutations with no output.
type emptyResponse struct{}
