package eventbridge

import (
	"time"

	ebdriver "github.com/stackshy/cloudemu/eventbus/driver"
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

type putRuleRequest struct {
	Name         string `json:"Name"`
	EventBusName string `json:"EventBusName"`
	Description  string `json:"Description"`
	EventPattern string `json:"EventPattern"`
	State        string `json:"State"`
}

type ruleRefRequest struct {
	Name         string `json:"Name"`
	EventBusName string `json:"EventBusName"`
}

type targetJSON struct {
	ID    string `json:"Id"`
	ARN   string `json:"Arn"`
	Input string `json:"Input,omitempty"`
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
}

type putRuleResponse struct {
	RuleArn string `json:"RuleArn"`
}

type describeRuleResponse struct {
	Arn          string `json:"Arn"`
	Name         string `json:"Name"`
	EventBusName string `json:"EventBusName"`
	Description  string `json:"Description,omitempty"`
	EventPattern string `json:"EventPattern,omitempty"`
	State        string `json:"State"`
}

type ruleEntry struct {
	Arn          string `json:"Arn"`
	Name         string `json:"Name"`
	EventBusName string `json:"EventBusName"`
	Description  string `json:"Description,omitempty"`
	EventPattern string `json:"EventPattern,omitempty"`
	State        string `json:"State"`
}

type listRulesResponse struct {
	Rules []ruleEntry `json:"Rules"`
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
	Targets []targetJSON `json:"Targets"`
}

type putEventsResultEntry struct {
	EventID string `json:"EventId,omitempty"`
}

type putEventsResponse struct {
	FailedEntryCount int                    `json:"FailedEntryCount"`
	Entries          []putEventsResultEntry `json:"Entries"`
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
func ruleARN(bus, rule string) string {
	if bus == "" {
		bus = defaultBusName
	}

	return "arn:aws:events:::rule/" + bus + "/" + rule
}

func toTargetJSON(t *ebdriver.Target) targetJSON {
	return targetJSON{ID: t.ID, ARN: t.ARN, Input: t.Input}
}

func fromTargetJSON(t *targetJSON) ebdriver.Target {
	return ebdriver.Target{ID: t.ID, ARN: t.ARN, Input: t.Input}
}
