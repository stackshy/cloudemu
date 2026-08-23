package cloudwatchlogs

import (
	"time"

	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// --- request envelopes ---

type createLogGroupRequest struct {
	LogGroupName string            `json:"logGroupName"`
	Tags         map[string]string `json:"tags"`
}

type describeLogGroupsRequest struct {
	LogGroupNamePrefix string `json:"logGroupNamePrefix"`
	Limit              int32  `json:"limit"`
	NextToken          string `json:"nextToken"`
}

type deleteLogGroupRequest struct {
	LogGroupName string `json:"logGroupName"`
}

type createLogStreamRequest struct {
	LogGroupName  string `json:"logGroupName"`
	LogStreamName string `json:"logStreamName"`
}

type describeLogStreamsRequest struct {
	LogGroupName string `json:"logGroupName"`
	Limit        int32  `json:"limit"`
	NextToken    string `json:"nextToken"`
}

type deleteLogStreamRequest struct {
	LogGroupName  string `json:"logGroupName"`
	LogStreamName string `json:"logStreamName"`
}

type inputLogEvent struct {
	Timestamp int64  `json:"timestamp"`
	Message   string `json:"message"`
}

type putLogEventsRequest struct {
	LogGroupName  string          `json:"logGroupName"`
	LogStreamName string          `json:"logStreamName"`
	LogEvents     []inputLogEvent `json:"logEvents"`
}

type getLogEventsRequest struct {
	LogGroupName  string `json:"logGroupName"`
	LogStreamName string `json:"logStreamName"`
	StartTime     int64  `json:"startTime"`
	EndTime       int64  `json:"endTime"`
	Limit         int32  `json:"limit"`
}

type filterLogEventsRequest struct {
	LogGroupName   string   `json:"logGroupName"`
	LogStreamNames []string `json:"logStreamNames"`
	FilterPattern  string   `json:"filterPattern"`
	StartTime      int64    `json:"startTime"`
	EndTime        int64    `json:"endTime"`
	Limit          int32    `json:"limit"`
}

// --- response envelopes ---

// logGroupJSON is the CloudWatch Logs LogGroup shape. Timestamps are epoch
// milliseconds, which is what the AWS JSON protocol uses for the SDK's
// *int64 CreationTime field.
type logGroupJSON struct {
	LogGroupName    string `json:"logGroupName"`
	Arn             string `json:"arn"`
	CreationTime    int64  `json:"creationTime"`
	RetentionInDays int32  `json:"retentionInDays,omitempty"`
	StoredBytes     int64  `json:"storedBytes"`
}

type describeLogGroupsResponse struct {
	LogGroups []logGroupJSON `json:"logGroups"`
	NextToken string         `json:"nextToken,omitempty"`
}

type logStreamJSON struct {
	LogStreamName       string `json:"logStreamName"`
	Arn                 string `json:"arn,omitempty"`
	CreationTime        int64  `json:"creationTime"`
	LastEventTimestamp  int64  `json:"lastEventTimestamp,omitempty"`
	LastIngestionTime   int64  `json:"lastIngestionTime,omitempty"`
	FirstEventTimestamp int64  `json:"firstEventTimestamp,omitempty"`
	StoredBytes         int64  `json:"storedBytes"`
}

type describeLogStreamsResponse struct {
	LogStreams []logStreamJSON `json:"logStreams"`
	NextToken  string          `json:"nextToken,omitempty"`
}

type putLogEventsResponse struct {
	NextSequenceToken string `json:"nextSequenceToken"`
}

type outputLogEvent struct {
	Timestamp     int64  `json:"timestamp"`
	Message       string `json:"message"`
	IngestionTime int64  `json:"ingestionTime"`
}

type getLogEventsResponse struct {
	Events            []outputLogEvent `json:"events"`
	NextForwardToken  string           `json:"nextForwardToken"`
	NextBackwardToken string           `json:"nextBackwardToken"`
}

type filteredLogEvent struct {
	LogStreamName string `json:"logStreamName"`
	Timestamp     int64  `json:"timestamp"`
	Message       string `json:"message"`
	IngestionTime int64  `json:"ingestionTime"`
}

type filterLogEventsResponse struct {
	Events []filteredLogEvent `json:"events"`
}

// epochMillis converts a time to Unix epoch milliseconds, the form the AWS JSON
// protocol uses for CloudWatch Logs timestamp fields. A zero time yields 0.
func epochMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.UnixMilli()
}

// ingestionMillis renders an event's ingestion time as epoch milliseconds,
// falling back to the event's own timestamp when the ingestion time is unknown
// (zero) so the wire field is never blank.
func ingestionMillis(ingestion, eventTS time.Time) int64 {
	if ingestion.IsZero() {
		return epochMillis(eventTS)
	}

	return epochMillis(ingestion)
}

// millisToTime is the inverse of epochMillis: epoch milliseconds to time. Zero
// yields the zero time so callers can distinguish "unset".
func millisToTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}

	return time.UnixMilli(ms).UTC()
}

// isoToEpochMillis converts an RFC3339 timestamp (the form the driver stores
// LogGroupInfo.CreatedAt / LogStreamInfo timestamps in) to epoch milliseconds.
// Returns 0 on empty input or a parse failure.
func isoToEpochMillis(iso string) int64 {
	if iso == "" {
		return 0
	}

	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return 0
	}

	return t.UnixMilli()
}

func toLogGroupJSON(info *logdriver.LogGroupInfo) logGroupJSON {
	return logGroupJSON{
		LogGroupName:    info.Name,
		Arn:             info.ResourceID,
		CreationTime:    isoToEpochMillis(info.CreatedAt),
		RetentionInDays: int32(info.RetentionDays), //nolint:gosec // retention days is a small positive value
		StoredBytes:     info.StoredBytes,
	}
}

// toLogStreamJSON renders a driver log stream. groupARN is the owning log
// group's ARN, used to derive the stream ARN Terraform's
// aws_cloudwatch_log_stream reads; empty groupARN omits the arn field.
func toLogStreamJSON(info *logdriver.LogStreamInfo, groupARN string) logStreamJSON {
	last := isoToEpochMillis(info.LastEvent)

	js := logStreamJSON{
		LogStreamName:       info.Name,
		CreationTime:        isoToEpochMillis(info.CreatedAt),
		LastEventTimestamp:  last,
		LastIngestionTime:   last,
		FirstEventTimestamp: isoToEpochMillis(info.FirstEvent),
	}

	if groupARN != "" {
		js.Arn = groupARN + ":log-stream:" + info.Name
	}

	return js
}
