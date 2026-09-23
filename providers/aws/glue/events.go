package glue

import (
	"context"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/awsevents"
	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// EventBridge identity of Glue's native job-run and crawler events. See
// "Automating AWS Glue with EventBridge" in the Glue developer guide. The real
// events carry an empty resources list.
const (
	eventSource             = "aws.glue"
	eventJobStateChange     = "Glue Job State Change"
	eventCrawlerStateChange = "Glue Crawler State Change"

	severityInfo  = "INFO"
	severityError = "ERROR"

	crawlerStarted   = "Started"
	crawlerSucceeded = "Succeeded"

	// zeroCount renders the crawler summary counters, which the real event
	// reports as strings. The emulator crawls no data source, so every table
	// and partition counter is zero.
	zeroCount = "0"
)

// jobStateChangeDetail is the detail payload of a "Glue Job State Change" event.
type jobStateChangeDetail struct {
	JobName  string `json:"jobName"`
	Severity string `json:"severity"`
	State    string `json:"state"`
	JobRunID string `json:"jobRunId"`
	Message  string `json:"message"`
}

// crawlerStartedDetail is the detail payload of a "Glue Crawler State Change"
// event for a crawl that just started.
type crawlerStartedDetail struct {
	AccountID   string `json:"accountId"`
	CrawlerName string `json:"crawlerName"`
	StartTime   string `json:"startTime"`
	State       string `json:"state"`
	Message     string `json:"message"`
}

// crawlerSucceededDetail is the detail payload of a "Glue Crawler State Change"
// event for a completed crawl, with the real event's string-typed counters.
type crawlerSucceededDetail struct {
	crawlerStartedDetail

	CompletionDate    string `json:"completionDate"`
	RunningTimeSec    string `json:"runningTime (sec)"`
	TablesCreated     string `json:"tablesCreated"`
	TablesUpdated     string `json:"tablesUpdated"`
	TablesDeleted     string `json:"tablesDeleted"`
	PartitionsCreated string `json:"partitionsCreated"`
	PartitionsUpdated string `json:"partitionsUpdated"`
	PartitionsDeleted string `json:"partitionsDeleted"`
	WarningMessage    string `json:"warningMessage"`
	CloudWatchLogLink string `json:"cloudWatchLogLink"`
}

// SetEventPublisher wires the EventBridge default bus that job-run and crawler
// state changes are published to. Safe to leave unset. No events are emitted.
func (m *Mock) SetEventPublisher(p awsevents.Publisher) {
	m.events.SetPublisher(p)
}

// jobStateMessage maps a terminal job-run state to its event severity and
// message, as real Glue reports them.
func jobStateMessage(state string) (severity, message string) {
	switch state {
	case driver.JobRunSucceeded:
		return severityInfo, "Job run succeeded"
	case driver.JobRunStopped:
		return severityInfo, "Job run stopped"
	default:
		return severityError, "Job run " + state
	}
}

// emitJobStateChange publishes a "Glue Job State Change" event for a run that
// reached a terminal state. It must be called without the run's lock held.
func (m *Mock) emitJobStateChange(ctx context.Context, jobName, runID, state string) {
	severity, message := jobStateMessage(state)

	m.events.Emit(ctx, eventSource, eventJobStateChange, jobStateChangeDetail{
		JobName: jobName, Severity: severity, State: state, JobRunID: runID, Message: message,
	})
}

// emitCrawlStarted publishes a crawl's Started state change. It must be called
// without the crawler's lock held.
func (m *Mock) emitCrawlStarted(ctx context.Context, name string, at time.Time) {
	m.events.Emit(ctx, eventSource, eventCrawlerStateChange, crawlerStartedDetail{
		AccountID: m.opts.AccountID, CrawlerName: name, StartTime: at.Format(time.RFC3339),
		State: crawlerStarted, Message: "Crawler Started",
	})
}

// emitCrawlSucceeded publishes a finished crawl's Succeeded state change, with
// the real event's string-typed summary counters. It must be called without
// the crawler's lock held.
func (m *Mock) emitCrawlSucceeded(ctx context.Context, name string, started, completed time.Time) {
	m.events.Emit(ctx, eventSource, eventCrawlerStateChange, crawlerSucceededDetail{
		crawlerStartedDetail: crawlerStartedDetail{
			AccountID: m.opts.AccountID, CrawlerName: name, StartTime: started.Format(time.RFC3339),
			State: crawlerSucceeded, Message: "Crawler Succeeded",
		},
		CompletionDate: completed.Format(time.RFC3339),
		RunningTimeSec: strconv.Itoa(int(completed.Sub(started).Seconds())),
		TablesCreated:  zeroCount, TablesUpdated: zeroCount, TablesDeleted: zeroCount,
		PartitionsCreated: zeroCount, PartitionsUpdated: zeroCount, PartitionsDeleted: zeroCount,
		WarningMessage: "N/A",
		CloudWatchLogLink: "https://console.aws.amazon.com/cloudwatch/home?region=" + m.opts.Region +
			"#logEventViewer:group=/aws-glue/crawlers;stream=" + name,
	})
}
