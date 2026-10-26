package batch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/awsevents"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

// EventBridge identity of Batch's native job events. See "Job state change
// events" in the AWS Batch user guide. Batch publishes a "Batch Job State
// Change" event every time an existing job changes status, but none for the
// initial submission; the detail is the job as DescribeJobs reports it.
const (
	eventSource         = "aws.batch"
	eventJobStateChange = "Batch Job State Change"
)

// jobEventDependency is a dependsOn entry of the event detail.
type jobEventDependency struct {
	JobID string `json:"jobId,omitempty"`
	Type  string `json:"type,omitempty"`
}

// jobEventArray is the arrayProperties section of the event detail.
type jobEventArray struct {
	Size          int32            `json:"size,omitempty"`
	Index         *int32           `json:"index,omitempty"`
	StatusSummary map[string]int32 `json:"statusSummary,omitempty"`
}

// jobStateChangeDetail is the detail payload of a "Batch Job State Change"
// event: the JobDetail shape DescribeJobs returns.
type jobStateChangeDetail struct {
	JobArn               string               `json:"jobArn"`
	JobName              string               `json:"jobName"`
	JobID                string               `json:"jobId"`
	JobQueue             string               `json:"jobQueue"`
	Status               string               `json:"status"`
	StatusReason         string               `json:"statusReason,omitempty"`
	Attempts             []any                `json:"attempts"`
	CreatedAt            int64                `json:"createdAt"`
	StartedAt            int64                `json:"startedAt,omitempty"`
	StoppedAt            int64                `json:"stoppedAt,omitempty"`
	RetryStrategy        json.RawMessage      `json:"retryStrategy,omitempty"`
	DependsOn            []jobEventDependency `json:"dependsOn"`
	JobDefinition        string               `json:"jobDefinition"`
	Parameters           map[string]string    `json:"parameters"`
	Container            json.RawMessage      `json:"container,omitempty"`
	NodeProperties       json.RawMessage      `json:"nodeProperties,omitempty"`
	EcsProperties        json.RawMessage      `json:"ecsProperties,omitempty"`
	EksProperties        json.RawMessage      `json:"eksProperties,omitempty"`
	ArrayProperties      *jobEventArray       `json:"arrayProperties,omitempty"`
	Timeout              json.RawMessage      `json:"timeout,omitempty"`
	Tags                 map[string]string    `json:"tags,omitempty"`
	PropagateTags        bool                 `json:"propagateTags"`
	PlatformCapabilities []string             `json:"platformCapabilities"`
	ShareIdentifier      string               `json:"shareIdentifier,omitempty"`
	SchedulingPriority   *int32               `json:"schedulingPriority,omitempty"`
	IsCancelled          bool                 `json:"isCancelled,omitempty"`
	IsTerminated         bool                 `json:"isTerminated,omitempty"`
}

// SetEventPublisher wires the EventBridge default bus that job state changes
// are published to. Safe to leave unset. No events are emitted.
func (m *Mock) SetEventPublisher(p awsevents.Publisher) {
	m.events.SetPublisher(p)
}

// pendingJobEvent is one event computed under jobsMu and published after it is
// released.
type pendingJobEvent struct {
	arn    string
	detail jobStateChangeDetail
}

// jobPath is the ordered status path a job walks on success. Array parents go
// straight from PENDING to their terminal state; a job with dependencies passes
// through PENDING; others skip it.
func jobPath(rec *jobRecord) []string {
	switch {
	case rec.arraySize() > 0:
		return []string{driver.JobStatusSubmitted, driver.JobStatusPending, driver.JobStatusSucceeded}
	case len(rec.Job.DependsOn) > 0:
		return []string{
			driver.JobStatusSubmitted, driver.JobStatusPending, driver.JobStatusRunnable,
			driver.JobStatusStarting, driver.JobStatusRunning, driver.JobStatusSucceeded,
		}
	default:
		return []string{
			driver.JobStatusSubmitted, driver.JobStatusRunnable, driver.JobStatusStarting,
			driver.JobStatusRunning, driver.JobStatusSucceeded,
		}
	}
}

// transitions returns the statuses a job passed through since last (exclusive)
// up to current (inclusive). A FAILED job reports only FAILED: the emulator
// does not reconstruct how far it got before failing.
func transitions(rec *jobRecord, last, current string) []string {
	if last == current {
		return nil
	}

	if current == driver.JobStatusFailed {
		return []string{current}
	}

	path := jobPath(rec)

	from, to := -1, -1

	for i, s := range path {
		if s == last {
			from = i
		}

		if s == current {
			to = i
		}
	}

	if to < 0 || to <= from {
		return []string{current}
	}

	return path[from+1 : to+1]
}

// publishJobEvents brings every job's published state up to date: for each job
// whose observed status moved since the last event, it publishes one "Batch
// Job State Change" per status passed through. It runs on every scheduler Tick
// and on every Batch job API call, so each transition is published once,
// shortly after the provider clock passes it. It reports whether anything was
// published.
func (m *Mock) publishJobEvents(ctx context.Context, now time.Time) bool {
	m.jobsMu.Lock()

	o := m.newObserver(now)

	var pending []pendingJobEvent

	all := m.jobs.All()
	for id := range all {
		rec := all[id]
		if isTerminal(rec.LastEmitted) {
			continue
		}

		j := o.render(&rec, -1)

		steps := transitions(&rec, rec.LastEmitted, j.Status)
		if len(steps) == 0 {
			continue
		}

		for _, s := range steps {
			pending = append(pending, pendingJobEvent{arn: j.JobARN, detail: eventDetail(&j, s)})
		}

		next := cloneJobRecord(rec)
		next.LastEmitted = j.Status
		m.jobs.Set(rec.Job.JobID, next)
	}

	m.jobsMu.Unlock()

	for i := range pending {
		m.events.Emit(ctx, eventSource, eventJobStateChange, pending[i].detail, pending[i].arn)
	}

	return len(pending) > 0
}

// Tick publishes the state-change events of every job whose status moved by
// now. It has the config.Tickable signature of the shared serve scheduler, so a
// job that nobody polls still reports each transition shortly after it
// happens; Batch job API calls also publish, so nothing has to call it.
func (m *Mock) Tick(now time.Time) bool {
	return m.publishJobEvents(context.Background(), now)
}

// eventDetail renders j as it stood in status: times not yet reached in that
// status are dropped.
func eventDetail(j *driver.Job, status string) jobStateChangeDetail {
	d := jobStateChangeDetail{
		JobArn: j.JobARN, JobName: j.JobName, JobID: j.JobID, JobQueue: j.JobQueue,
		Status: status, Attempts: []any{}, CreatedAt: j.CreatedAt,
		RetryStrategy: j.RetryStrategy, DependsOn: make([]jobEventDependency, 0, len(j.DependsOn)),
		JobDefinition: j.JobDefinition, Parameters: j.Parameters, Container: j.Container,
		NodeProperties: j.NodeProperties, EcsProperties: j.EcsProperties, EksProperties: j.EksProperties,
		Timeout: j.Timeout, Tags: j.Tags, PlatformCapabilities: j.PlatformCapabilities,
		ShareIdentifier: j.ShareIdentifier, SchedulingPriority: j.SchedulingPriority,
	}

	if d.Parameters == nil {
		d.Parameters = map[string]string{}
	}

	if d.PlatformCapabilities == nil {
		d.PlatformCapabilities = []string{}
	}

	if j.PropagateTags != nil {
		d.PropagateTags = *j.PropagateTags
	}

	for _, dep := range j.DependsOn {
		d.DependsOn = append(d.DependsOn, jobEventDependency(dep))
	}

	if ap := j.ArrayProperties; ap != nil {
		d.ArrayProperties = &jobEventArray{Size: ap.Size, Index: ap.Index, StatusSummary: ap.StatusSummary}
	}

	if status != j.Status {
		// An intermediate status replayed after the fact: report only what was
		// true then. The container hadn't exited yet, so it has no exitCode.
		d.Container = withoutExitCode(j.Container)

		return d
	}

	d.StatusReason = j.StatusReason
	d.StartedAt = j.StartedAt
	d.StoppedAt = j.StoppedAt
	d.IsCancelled = j.IsCancelled
	d.IsTerminated = j.IsTerminated

	return d
}
