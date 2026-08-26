package eventbridge

import (
	"encoding/json"
	"sort"
	"time"

	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// defaultBusName is the implicit bus EventBridge routes to when a request omits
// EventBusName; it mirrors the driver's own default.
const defaultBusName = "default"

type tagJSON struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// --- request envelopes ---

type createEventBusRequest struct {
	Name        string    `json:"Name"`
	Description string    `json:"Description"`
	Tags        []tagJSON `json:"Tags"`
}

type nameRequest struct {
	Name string `json:"Name"`
}

type listEventBusesRequest struct {
	NamePrefix string `json:"NamePrefix"`
	Limit      int    `json:"Limit"`
	NextToken  string `json:"NextToken"`
}

type putRuleRequest struct {
	Name               string `json:"Name"`
	EventBusName       string `json:"EventBusName"`
	Description        string `json:"Description"`
	EventPattern       string `json:"EventPattern"`
	ScheduleExpression string `json:"ScheduleExpression"`
	RoleArn            string `json:"RoleArn"`
	State              string `json:"State"`
}

type ruleRefRequest struct {
	Name         string `json:"Name"`
	EventBusName string `json:"EventBusName"`
}

type listRulesRequest struct {
	EventBusName string `json:"EventBusName"`
	NamePrefix   string `json:"NamePrefix"`
	Limit        int    `json:"Limit"`
	NextToken    string `json:"NextToken"`
}

type targetJSON struct {
	ID               string          `json:"Id"`
	ARN              string          `json:"Arn"`
	Input            string          `json:"Input,omitempty"`
	InputPath        string          `json:"InputPath,omitempty"`
	RoleArn          string          `json:"RoleArn,omitempty"`
	InputTransformer json.RawMessage `json:"InputTransformer,omitempty"`
	DeadLetterConfig json.RawMessage `json:"DeadLetterConfig,omitempty"`
	RetryPolicy      json.RawMessage `json:"RetryPolicy,omitempty"`
}

type putTargetsRequest struct {
	Rule         string       `json:"Rule"`
	EventBusName string       `json:"EventBusName"`
	Targets      []targetJSON `json:"Targets"`
}

type removeTargetsRequest struct {
	Rule         string   `json:"Rule"`
	EventBusName string   `json:"EventBusName"`
	Ids          []string `json:"Ids"`
}

type listTargetsByRuleRequest struct {
	Rule         string `json:"Rule"`
	EventBusName string `json:"EventBusName"`
	Limit        int    `json:"Limit"`
	NextToken    string `json:"NextToken"`
}

type putEventsEntry struct {
	Source       string   `json:"Source"`
	DetailType   string   `json:"DetailType"`
	Detail       string   `json:"Detail"`
	EventBusName string   `json:"EventBusName"`
	Resources    []string `json:"Resources"`
}

type putEventsRequest struct {
	Entries []putEventsEntry `json:"Entries"`
}

type testEventPatternRequest struct {
	Event        string `json:"Event"`
	EventPattern string `json:"EventPattern"`
}

// --- response envelopes ---

type createEventBusResponse struct {
	EventBusArn string `json:"EventBusArn"`
	Description string `json:"Description,omitempty"`
}

type describeEventBusResponse struct {
	Arn          string  `json:"Arn"`
	Name         string  `json:"Name"`
	Description  string  `json:"Description,omitempty"`
	CreationTime float64 `json:"CreationTime,omitempty"`
}

type eventBusEntry struct {
	Arn          string  `json:"Arn"`
	Name         string  `json:"Name"`
	Description  string  `json:"Description,omitempty"`
	CreationTime float64 `json:"CreationTime,omitempty"`
}

type listEventBusesResponse struct {
	EventBuses []eventBusEntry `json:"EventBuses"`
	NextToken  string          `json:"NextToken,omitempty"`
}

type putRuleResponse struct {
	RuleArn string `json:"RuleArn"`
}

type describeRuleResponse struct {
	Arn                string `json:"Arn"`
	Name               string `json:"Name"`
	EventBusName       string `json:"EventBusName"`
	Description        string `json:"Description,omitempty"`
	EventPattern       string `json:"EventPattern,omitempty"`
	ScheduleExpression string `json:"ScheduleExpression,omitempty"`
	RoleArn            string `json:"RoleArn,omitempty"`
	State              string `json:"State"`
}

type ruleEntry struct {
	Arn                string `json:"Arn"`
	Name               string `json:"Name"`
	EventBusName       string `json:"EventBusName"`
	Description        string `json:"Description,omitempty"`
	EventPattern       string `json:"EventPattern,omitempty"`
	ScheduleExpression string `json:"ScheduleExpression,omitempty"`
	RoleArn            string `json:"RoleArn,omitempty"`
	State              string `json:"State"`
}

type listRulesResponse struct {
	Rules     []ruleEntry `json:"Rules"`
	NextToken string      `json:"NextToken,omitempty"`
}

type putTargetsResponse struct {
	FailedEntryCount int   `json:"FailedEntryCount"`
	FailedEntries    []any `json:"FailedEntries"`
}

type removeTargetsResponse struct {
	FailedEntryCount int   `json:"FailedEntryCount"`
	FailedEntries    []any `json:"FailedEntries"`
}

type listTargetsByRuleResponse struct {
	Targets   []targetJSON `json:"Targets"`
	NextToken string       `json:"NextToken,omitempty"`
}

type putEventsResultEntry struct {
	EventID      string `json:"EventId,omitempty"`
	ErrorCode    string `json:"ErrorCode,omitempty"`
	ErrorMessage string `json:"ErrorMessage,omitempty"`
}

type putEventsResponse struct {
	FailedEntryCount int                    `json:"FailedEntryCount"`
	Entries          []putEventsResultEntry `json:"Entries"`
}

type testEventPatternResponse struct {
	Result bool `json:"Result"`
}

// --- helpers ---

// busNameOrDefault resolves an optional EventBusName to the driver-facing bus
// name, mirroring the driver's default-bus behavior.
func busNameOrDefault(name string) string {
	if name == "" {
		return defaultBusName
	}

	return name
}

func tagsToMap(tags []tagJSON) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

// epochSeconds converts an RFC3339 timestamp to Unix epoch seconds, the form
// the AWS JSON protocol uses for timestamp fields. Returns 0 on parse failure.
func epochSeconds(iso string) float64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return 0
	}

	return float64(t.Unix())
}

// ruleARN synthesizes an EventBridge rule ARN. The driver's Rule carries no
// ARN, so we derive a stable identifier the SDK can round-trip. Real
// EventBridge rule ARNs are "arn:aws:events:<region>:<account>:rule/<bus>/<rule>";
// region/account aren't threaded into this handler, so they're left as
// placeholders that keep the ARN shape recognizable.
func (h *Handler) ruleARN(bus, rule string) string {
	if bus == "" {
		bus = defaultBusName
	}

	return "arn:aws:events:" + h.region + ":" + h.accountID + ":rule/" + bus + "/" + rule
}

// paginateRules sorts rule entries by name and applies EventBridge's ListRules
// NamePrefix-independent pagination: NextToken resumes after the last rule of
// the prior page, and Limit caps the page size. The returned token is empty
// when the page is the last one.
func paginateRules(entries []ruleEntry, nextToken string, limit int) (page []ruleEntry, next string) {
	return paginateByCursor(entries, nextToken, limit, func(e ruleEntry) string { return e.Name })
}

// paginateByCursor applies EventBridge's value-cursor pagination over a slice
// sorted by the key returned by keyOf: NextToken resumes after the item whose
// key equals the token, and Limit caps the page size. The returned token is the
// key of the last item on a truncated page, or empty when the page is the last.
func paginateByCursor[T any](items []T, nextToken string, limit int, keyOf func(T) string) (page []T, next string) {
	sort.Slice(items, func(i, j int) bool { return keyOf(items[i]) < keyOf(items[j]) })

	if nextToken != "" {
		start := 0

		for i := range items {
			if keyOf(items[i]) == nextToken {
				start = i + 1

				break
			}
		}

		items = items[start:]
	}

	if limit > 0 && limit < len(items) {
		return items[:limit], keyOf(items[limit-1])
	}

	return items, ""
}

// isValidDetail reports whether a PutEvents entry's Detail is acceptable:
// empty (defaulted downstream) or a well-formed JSON object. Real EventBridge
// fails any other Detail with ErrorCode=MalformedDetail.
func isValidDetail(detail string) bool {
	if detail == "" {
		return true
	}

	var obj map[string]json.RawMessage

	return json.Unmarshal([]byte(detail), &obj) == nil
}

func toTargetJSON(t *ebdriver.Target) targetJSON {
	out := targetJSON{
		ID:        t.ID,
		ARN:       t.ARN,
		Input:     t.Input,
		InputPath: t.InputPath,
		RoleArn:   t.RoleARN,
	}

	if t.InputTransformer != "" {
		out.InputTransformer = json.RawMessage(t.InputTransformer)
	}

	if t.DeadLetterConfig != "" {
		out.DeadLetterConfig = json.RawMessage(t.DeadLetterConfig)
	}

	if t.RetryPolicy != "" {
		out.RetryPolicy = json.RawMessage(t.RetryPolicy)
	}

	return out
}

func fromTargetJSON(t *targetJSON) ebdriver.Target {
	return ebdriver.Target{
		ID:               t.ID,
		ARN:              t.ARN,
		Input:            t.Input,
		InputPath:        t.InputPath,
		RoleARN:          t.RoleArn,
		InputTransformer: string(t.InputTransformer),
		DeadLetterConfig: string(t.DeadLetterConfig),
		RetryPolicy:      string(t.RetryPolicy),
	}
}
