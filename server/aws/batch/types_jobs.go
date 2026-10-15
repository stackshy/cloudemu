package batch

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

// --- Scheduling policies ---

type createSchedulingPolicyRequest struct {
	Name             string            `json:"name"`
	FairsharePolicy  json.RawMessage   `json:"fairsharePolicy"`
	QuotaSharePolicy json.RawMessage   `json:"quotaSharePolicy"`
	Tags             map[string]string `json:"tags"`
}

func (req *createSchedulingPolicyRequest) toInput() driver.CreateSchedulingPolicyInput {
	return driver.CreateSchedulingPolicyInput{
		Name:             req.Name,
		FairsharePolicy:  req.FairsharePolicy,
		QuotaSharePolicy: req.QuotaSharePolicy,
		Tags:             req.Tags,
	}
}

type schedulingPolicyNameArnResponse struct {
	Name string `json:"name"`
	Arn  string `json:"arn"`
}

type describeSchedulingPoliciesRequest struct {
	Arns []string `json:"arns"`
}

type schedulingPolicyDetail struct {
	Name             string            `json:"name"`
	Arn              string            `json:"arn"`
	FairsharePolicy  json.RawMessage   `json:"fairsharePolicy,omitempty"`
	QuotaSharePolicy json.RawMessage   `json:"quotaSharePolicy,omitempty"`
	Tags             map[string]string `json:"tags,omitempty"`
}

type describeSchedulingPoliciesResponse struct {
	SchedulingPolicies []schedulingPolicyDetail `json:"schedulingPolicies"`
}

type listSchedulingPoliciesRequest struct {
	MaxResults *int32 `json:"maxResults"`
	NextToken  string `json:"nextToken"`
}

type schedulingPolicyListing struct {
	Arn string `json:"arn"`
}

type listSchedulingPoliciesResponse struct {
	SchedulingPolicies []schedulingPolicyListing `json:"schedulingPolicies"`
	NextToken          string                    `json:"nextToken,omitempty"`
}

type updateSchedulingPolicyRequest struct {
	Arn              string          `json:"arn"`
	FairsharePolicy  json.RawMessage `json:"fairsharePolicy"`
	QuotaSharePolicy json.RawMessage `json:"quotaSharePolicy"`
}

type deleteSchedulingPolicyRequest struct {
	Arn string `json:"arn"`
}

func toSPDetail(p *driver.SchedulingPolicy) schedulingPolicyDetail {
	return schedulingPolicyDetail{
		Name:             p.Name,
		Arn:              p.ARN,
		FairsharePolicy:  p.FairsharePolicy,
		QuotaSharePolicy: p.QuotaSharePolicy,
		Tags:             p.Tags,
	}
}

// --- Jobs ---

type jobDependencyWire struct {
	JobID string `json:"jobId,omitempty"`
	Type  string `json:"type,omitempty"`
}

type arrayPropertiesRequest struct {
	Size *int32 `json:"size"`
}

type submitJobRequest struct {
	JobName                    string                  `json:"jobName"`
	JobQueue                   string                  `json:"jobQueue"`
	JobDefinition              string                  `json:"jobDefinition"`
	ArrayProperties            *arrayPropertiesRequest `json:"arrayProperties"`
	DependsOn                  []jobDependencyWire     `json:"dependsOn"`
	Parameters                 map[string]string       `json:"parameters"`
	ContainerOverrides         json.RawMessage         `json:"containerOverrides"`
	NodeOverrides              json.RawMessage         `json:"nodeOverrides"`
	EcsPropertiesOverride      json.RawMessage         `json:"ecsPropertiesOverride"`
	EksPropertiesOverride      json.RawMessage         `json:"eksPropertiesOverride"`
	RetryStrategy              json.RawMessage         `json:"retryStrategy"`
	Timeout                    json.RawMessage         `json:"timeout"`
	PropagateTags              *bool                   `json:"propagateTags"`
	SchedulingPriorityOverride *int32                  `json:"schedulingPriorityOverride"`
	ShareIdentifier            string                  `json:"shareIdentifier"`
	Tags                       map[string]string       `json:"tags"`
}

type submitJobResponse struct {
	JobArn  string `json:"jobArn"`
	JobID   string `json:"jobId"`
	JobName string `json:"jobName"`
}

type describeJobsRequest struct {
	Jobs []string `json:"jobs"`
}

type arrayPropertiesWire struct {
	Size          int32            `json:"size,omitempty"`
	Index         *int32           `json:"index,omitempty"`
	StatusSummary map[string]int32 `json:"statusSummary,omitempty"`
}

type jobDetail struct {
	JobArn               string               `json:"jobArn"`
	JobName              string               `json:"jobName"`
	JobID                string               `json:"jobId"`
	JobQueue             string               `json:"jobQueue"`
	Status               string               `json:"status"`
	StatusReason         string               `json:"statusReason,omitempty"`
	Attempts             []json.RawMessage    `json:"attempts"`
	CreatedAt            int64                `json:"createdAt,omitempty"`
	StartedAt            int64                `json:"startedAt,omitempty"`
	StoppedAt            int64                `json:"stoppedAt,omitempty"`
	DependsOn            []jobDependencyWire  `json:"dependsOn"`
	JobDefinition        string               `json:"jobDefinition"`
	Parameters           map[string]string    `json:"parameters"`
	Container            json.RawMessage      `json:"container,omitempty"`
	NodeProperties       json.RawMessage      `json:"nodeProperties,omitempty"`
	EcsProperties        json.RawMessage      `json:"ecsProperties,omitempty"`
	EksProperties        json.RawMessage      `json:"eksProperties,omitempty"`
	ArrayProperties      *arrayPropertiesWire `json:"arrayProperties,omitempty"`
	RetryStrategy        json.RawMessage      `json:"retryStrategy,omitempty"`
	Timeout              json.RawMessage      `json:"timeout,omitempty"`
	PlatformCapabilities []string             `json:"platformCapabilities"`
	PropagateTags        *bool                `json:"propagateTags,omitempty"`
	SchedulingPriority   *int32               `json:"schedulingPriority,omitempty"`
	ShareIdentifier      string               `json:"shareIdentifier,omitempty"`
	IsCancelled          *bool                `json:"isCancelled,omitempty"`
	IsTerminated         *bool                `json:"isTerminated,omitempty"`
	Tags                 map[string]string    `json:"tags,omitempty"`
}

type describeJobsResponse struct {
	Jobs []jobDetail `json:"jobs"`
}

type jobFilterWire struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

type listJobsRequest struct {
	JobQueue       string          `json:"jobQueue"`
	ArrayJobID     string          `json:"arrayJobId"`
	MultiNodeJobID string          `json:"multiNodeJobId"`
	JobStatus      string          `json:"jobStatus"`
	Filters        []jobFilterWire `json:"filters"`
	MaxResults     *int32          `json:"maxResults"`
	NextToken      string          `json:"nextToken"`
}

type containerSummary struct {
	ExitCode *int32 `json:"exitCode,omitempty"`
}

type jobSummary struct {
	JobArn          string               `json:"jobArn"`
	JobID           string               `json:"jobId"`
	JobName         string               `json:"jobName"`
	CreatedAt       int64                `json:"createdAt,omitempty"`
	Status          string               `json:"status"`
	StatusReason    string               `json:"statusReason,omitempty"`
	StartedAt       int64                `json:"startedAt,omitempty"`
	StoppedAt       int64                `json:"stoppedAt,omitempty"`
	Container       *containerSummary    `json:"container,omitempty"`
	ArrayProperties *arrayPropertiesWire `json:"arrayProperties,omitempty"`
	JobDefinition   string               `json:"jobDefinition,omitempty"`
	ShareIdentifier string               `json:"shareIdentifier,omitempty"`
	IsCancelled     *bool                `json:"isCancelled,omitempty"`
	IsTerminated    *bool                `json:"isTerminated,omitempty"`
}

type listJobsResponse struct {
	JobSummaryList []jobSummary `json:"jobSummaryList"`
	NextToken      string       `json:"nextToken,omitempty"`
}

type stopJobRequest struct {
	JobID  string `json:"jobId"`
	Reason string `json:"reason"`
}

func depsToDriver(in []jobDependencyWire) []driver.JobDependency {
	if in == nil {
		return nil
	}

	out := make([]driver.JobDependency, len(in))
	for i, d := range in {
		out[i] = driver.JobDependency{JobID: d.JobID, Type: d.Type}
	}

	return out
}

func depsToWire(in []driver.JobDependency) []jobDependencyWire {
	out := make([]jobDependencyWire, len(in))
	for i, d := range in {
		out[i] = jobDependencyWire{JobID: d.JobID, Type: d.Type}
	}

	return out
}

func filtersToDriver(in []jobFilterWire) []driver.JobFilter {
	if in == nil {
		return nil
	}

	out := make([]driver.JobFilter, len(in))
	for i, f := range in {
		out[i] = driver.JobFilter{Name: f.Name, Values: f.Values}
	}

	return out
}

func arrayToWire(ap *driver.ArrayProperties) *arrayPropertiesWire {
	if ap == nil {
		return nil
	}

	return &arrayPropertiesWire{Size: ap.Size, Index: ap.Index, StatusSummary: ap.StatusSummary}
}

func boolPtrIf(b bool) *bool {
	if !b {
		return nil
	}

	return &b
}

func toJobDetail(j *driver.Job) jobDetail {
	params := j.Parameters
	if params == nil {
		params = map[string]string{}
	}

	caps := j.PlatformCapabilities
	if caps == nil {
		caps = []string{}
	}

	return jobDetail{
		JobArn:               j.JobARN,
		JobName:              j.JobName,
		JobID:                j.JobID,
		JobQueue:             j.JobQueue,
		Status:               j.Status,
		StatusReason:         j.StatusReason,
		Attempts:             []json.RawMessage{},
		CreatedAt:            j.CreatedAt,
		StartedAt:            j.StartedAt,
		StoppedAt:            j.StoppedAt,
		DependsOn:            depsToWire(j.DependsOn),
		JobDefinition:        j.JobDefinition,
		Parameters:           params,
		Container:            j.Container,
		NodeProperties:       j.NodeProperties,
		EcsProperties:        j.EcsProperties,
		EksProperties:        j.EksProperties,
		ArrayProperties:      arrayToWire(j.ArrayProperties),
		RetryStrategy:        j.RetryStrategy,
		Timeout:              j.Timeout,
		PlatformCapabilities: caps,
		PropagateTags:        j.PropagateTags,
		SchedulingPriority:   j.SchedulingPriority,
		ShareIdentifier:      j.ShareIdentifier,
		IsCancelled:          boolPtrIf(j.IsCancelled),
		IsTerminated:         boolPtrIf(j.IsTerminated),
		Tags:                 j.Tags,
	}
}

// toJobSummary renders the ListJobs JobSummary of a job. container.exitCode is
// reported once the job's container has exited successfully.
func toJobSummary(j *driver.Job) jobSummary {
	s := jobSummary{
		JobArn:          j.JobARN,
		JobID:           j.JobID,
		JobName:         j.JobName,
		CreatedAt:       j.CreatedAt,
		Status:          j.Status,
		StatusReason:    j.StatusReason,
		StartedAt:       j.StartedAt,
		StoppedAt:       j.StoppedAt,
		ArrayProperties: arrayToWire(j.ArrayProperties),
		JobDefinition:   j.JobDefinition,
		ShareIdentifier: j.ShareIdentifier,
		IsCancelled:     boolPtrIf(j.IsCancelled),
		IsTerminated:    boolPtrIf(j.IsTerminated),
	}

	if j.Status == driver.JobStatusSucceeded && j.Container != nil && (j.ArrayProperties == nil || j.ArrayProperties.Index != nil) {
		zero := int32(0)
		s.Container = &containerSummary{ExitCode: &zero}
	}

	return s
}
