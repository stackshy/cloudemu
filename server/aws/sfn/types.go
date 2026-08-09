package sfn

import (
	"time"

	sfndriver "github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// epoch renders a time as AWS JSON 1.0 epoch seconds (fractional). A zero time
// serializes as nil so optional timestamps are omitted.
func epoch(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.UnixNano()) / float64(time.Second)

	return &secs
}

type tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func tagsToMap(tags []tag) map[string]string {
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

func mapToTags(m map[string]string) []tag {
	out := make([]tag, 0, len(m))
	for k, v := range m {
		out = append(out, tag{Key: k, Value: v})
	}

	return out
}

// --- request shapes ---

type createStateMachineRequest struct {
	Name                 string `json:"name"`
	Definition           string `json:"definition"`
	RoleArn              string `json:"roleArn"`
	Type                 string `json:"type"`
	Description          string `json:"versionDescription"`
	Publish              bool   `json:"publish"`
	Tags                 []tag  `json:"tags"`
	LoggingConfiguration any    `json:"loggingConfiguration"`
	TracingConfiguration any    `json:"tracingConfiguration"`
}

type stateMachineArnRequest struct {
	StateMachineArn string `json:"stateMachineArn"`
}

type updateStateMachineRequest struct {
	StateMachineArn    string `json:"stateMachineArn"`
	Definition         string `json:"definition"`
	RoleArn            string `json:"roleArn"`
	Publish            bool   `json:"publish"`
	VersionDescription string `json:"versionDescription"`
}

type startExecutionRequest struct {
	StateMachineArn string `json:"stateMachineArn"`
	Name            string `json:"name"`
	Input           string `json:"input"`
}

type executionArnRequest struct {
	ExecutionArn string `json:"executionArn"`
}

type stopExecutionRequest struct {
	ExecutionArn string `json:"executionArn"`
	Error        string `json:"error"`
	Cause        string `json:"cause"`
}

type listExecutionsRequest struct {
	StateMachineArn string `json:"stateMachineArn"`
	StatusFilter    string `json:"statusFilter"`
}

type getExecutionHistoryRequest struct {
	ExecutionArn string `json:"executionArn"`
	ReverseOrder bool   `json:"reverseOrder"`
}

type publishVersionRequest struct {
	StateMachineArn string `json:"stateMachineArn"`
	Description     string `json:"description"`
}

type versionArnRequest struct {
	StateMachineVersionArn string `json:"stateMachineVersionArn"`
}

type routingItem struct {
	StateMachineVersionArn string `json:"stateMachineVersionArn"`
	Weight                 int32  `json:"weight"`
}

type createAliasRequest struct {
	Name                 string        `json:"name"`
	Description          string        `json:"description"`
	RoutingConfiguration []routingItem `json:"routingConfiguration"`
}

type aliasArnRequest struct {
	StateMachineAliasArn string `json:"stateMachineAliasArn"`
}

type updateAliasRequest struct {
	StateMachineAliasArn string        `json:"stateMachineAliasArn"`
	Description          string        `json:"description"`
	RoutingConfiguration []routingItem `json:"routingConfiguration"`
}

type createActivityRequest struct {
	Name string `json:"name"`
	Tags []tag  `json:"tags"`
}

type activityArnRequest struct {
	ActivityArn string `json:"activityArn"`
}

type getActivityTaskRequest struct {
	ActivityArn string `json:"activityArn"`
	WorkerName  string `json:"workerName"`
}

type sendTaskSuccessRequest struct {
	TaskToken string `json:"taskToken"`
	Output    string `json:"output"`
}

type sendTaskFailureRequest struct {
	TaskToken string `json:"taskToken"`
	Error     string `json:"error"`
	Cause     string `json:"cause"`
}

type sendTaskHeartbeatRequest struct {
	TaskToken string `json:"taskToken"`
}

type tagResourceRequest struct {
	ResourceArn string `json:"resourceArn"`
	Tags        []tag  `json:"tags"`
}

type untagResourceRequest struct {
	ResourceArn string   `json:"resourceArn"`
	TagKeys     []string `json:"tagKeys"`
}

type resourceArnRequest struct {
	ResourceArn string `json:"resourceArn"`
}

// --- response shapes ---

type createStateMachineResponse struct {
	StateMachineArn        string   `json:"stateMachineArn"`
	CreationDate           *float64 `json:"creationDate"`
	StateMachineVersionArn string   `json:"stateMachineVersionArn,omitempty"`
}

type describeStateMachineResponse struct {
	StateMachineArn string   `json:"stateMachineArn"`
	Name            string   `json:"name"`
	Definition      string   `json:"definition"`
	RoleArn         string   `json:"roleArn"`
	Type            string   `json:"type"`
	Status          string   `json:"status"`
	Description     string   `json:"description,omitempty"`
	RevisionID      string   `json:"revisionId,omitempty"`
	CreationDate    *float64 `json:"creationDate"`
	Label           string   `json:"label,omitempty"`
}

type updateStateMachineResponse struct {
	UpdateDate             *float64 `json:"updateDate"`
	RevisionID             string   `json:"revisionId,omitempty"`
	StateMachineVersionArn string   `json:"stateMachineVersionArn,omitempty"`
}

type stateMachineListItem struct {
	StateMachineArn string   `json:"stateMachineArn"`
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	CreationDate    *float64 `json:"creationDate"`
}

type listStateMachinesResponse struct {
	StateMachines []stateMachineListItem `json:"stateMachines"`
	NextToken     string                 `json:"nextToken,omitempty"`
}

type startExecutionResponse struct {
	ExecutionArn string   `json:"executionArn"`
	StartDate    *float64 `json:"startDate"`
}

type startSyncExecutionResponse struct {
	ExecutionArn    string   `json:"executionArn"`
	StateMachineArn string   `json:"stateMachineArn"`
	Name            string   `json:"name"`
	Status          string   `json:"status"`
	StartDate       *float64 `json:"startDate"`
	StopDate        *float64 `json:"stopDate"`
	Input           string   `json:"input,omitempty"`
	Output          string   `json:"output,omitempty"`
	Error           string   `json:"error,omitempty"`
	Cause           string   `json:"cause,omitempty"`
}

type describeExecutionResponse struct {
	ExecutionArn    string   `json:"executionArn"`
	StateMachineArn string   `json:"stateMachineArn"`
	Name            string   `json:"name"`
	Status          string   `json:"status"`
	StartDate       *float64 `json:"startDate"`
	StopDate        *float64 `json:"stopDate,omitempty"`
	Input           string   `json:"input,omitempty"`
	Output          string   `json:"output,omitempty"`
	Error           string   `json:"error,omitempty"`
	Cause           string   `json:"cause,omitempty"`
}

type stopExecutionResponse struct {
	StopDate *float64 `json:"stopDate"`
}

type executionListItem struct {
	ExecutionArn    string   `json:"executionArn"`
	StateMachineArn string   `json:"stateMachineArn"`
	Name            string   `json:"name"`
	Status          string   `json:"status"`
	StartDate       *float64 `json:"startDate"`
	StopDate        *float64 `json:"stopDate,omitempty"`
}

type listExecutionsResponse struct {
	Executions []executionListItem `json:"executions"`
	NextToken  string              `json:"nextToken,omitempty"`
}

type executionStartedDetails struct {
	Input string `json:"input,omitempty"`
}

type executionSucceededDetails struct {
	Output string `json:"output,omitempty"`
}

type historyEvent struct {
	ID                        int64                      `json:"id"`
	PreviousEventID           int64                      `json:"previousEventId"`
	Type                      string                     `json:"type"`
	Timestamp                 *float64                   `json:"timestamp"`
	ExecutionStartedDetails   *executionStartedDetails   `json:"executionStartedEventDetails,omitempty"`
	ExecutionSucceededDetails *executionSucceededDetails `json:"executionSucceededEventDetails,omitempty"`
}

type getExecutionHistoryResponse struct {
	Events    []historyEvent `json:"events"`
	NextToken string         `json:"nextToken,omitempty"`
}

type describeStateMachineForExecutionResponse struct {
	StateMachineArn string   `json:"stateMachineArn"`
	Name            string   `json:"name"`
	Definition      string   `json:"definition"`
	RoleArn         string   `json:"roleArn"`
	UpdateDate      *float64 `json:"updateDate"`
	RevisionID      string   `json:"revisionId,omitempty"`
}

type publishVersionResponse struct {
	StateMachineVersionArn string   `json:"stateMachineVersionArn"`
	CreationDate           *float64 `json:"creationDate"`
}

type versionListItem struct {
	StateMachineVersionArn string   `json:"stateMachineVersionArn"`
	CreationDate           *float64 `json:"creationDate"`
}

type listVersionsResponse struct {
	StateMachineVersions []versionListItem `json:"stateMachineVersions"`
	NextToken            string            `json:"nextToken,omitempty"`
}

type createAliasResponse struct {
	StateMachineAliasArn string   `json:"stateMachineAliasArn"`
	CreationDate         *float64 `json:"creationDate"`
}

type describeAliasResponse struct {
	StateMachineAliasArn string        `json:"stateMachineAliasArn"`
	Name                 string        `json:"name"`
	Description          string        `json:"description,omitempty"`
	RoutingConfiguration []routingItem `json:"routingConfiguration"`
	CreationDate         *float64      `json:"creationDate"`
	UpdateDate           *float64      `json:"updateDate"`
}

type updateAliasResponse struct {
	UpdateDate *float64 `json:"updateDate"`
}

type aliasListItem struct {
	StateMachineAliasArn string   `json:"stateMachineAliasArn"`
	CreationDate         *float64 `json:"creationDate"`
}

type listAliasesResponse struct {
	StateMachineAliases []aliasListItem `json:"stateMachineAliases"`
	NextToken           string          `json:"nextToken,omitempty"`
}

type createActivityResponse struct {
	ActivityArn  string   `json:"activityArn"`
	CreationDate *float64 `json:"creationDate"`
}

type describeActivityResponse struct {
	ActivityArn  string   `json:"activityArn"`
	Name         string   `json:"name"`
	CreationDate *float64 `json:"creationDate"`
}

type activityListItem struct {
	ActivityArn  string   `json:"activityArn"`
	Name         string   `json:"name"`
	CreationDate *float64 `json:"creationDate"`
}

type listActivitiesResponse struct {
	Activities []activityListItem `json:"activities"`
	NextToken  string             `json:"nextToken,omitempty"`
}

type getActivityTaskResponse struct {
	TaskToken string `json:"taskToken,omitempty"`
	Input     string `json:"input,omitempty"`
}

type listTagsResponse struct {
	Tags []tag `json:"tags"`
}

// --- converters ---

func routingFromWire(in []routingItem) []sfndriver.RouteEntry {
	out := make([]sfndriver.RouteEntry, 0, len(in))
	for _, r := range in {
		out = append(out, sfndriver.RouteEntry{StateMachineVersionArn: r.StateMachineVersionArn, Weight: r.Weight})
	}

	return out
}

func routingToWire(in []sfndriver.RouteEntry) []routingItem {
	out := make([]routingItem, 0, len(in))
	for _, r := range in {
		out = append(out, routingItem{StateMachineVersionArn: r.StateMachineVersionArn, Weight: r.Weight})
	}

	return out
}
