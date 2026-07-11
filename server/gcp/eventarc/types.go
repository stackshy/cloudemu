package eventarc

import (
	"encoding/json"

	ebdriver "github.com/stackshy/cloudemu/eventbus/driver"
)

// destinationTargetID is the fixed target id under which a trigger's
// destination is stored on the driver rule.
const destinationTargetID = "destination"

// eventFilterJSON is the Eventarc v1 EventFilter shape.
type eventFilterJSON struct {
	Attribute string `json:"attribute,omitempty"`
	Operator  string `json:"operator,omitempty"`
	Value     string `json:"value,omitempty"`
}

// cloudRunJSON is the subset of the Eventarc v1 CloudRun destination we
// round-trip.
type cloudRunJSON struct {
	Service string `json:"service,omitempty"`
	Region  string `json:"region,omitempty"`
	Path    string `json:"path,omitempty"`
}

// destinationJSON is the Eventarc v1 Destination shape (subset).
type destinationJSON struct {
	CloudRun      *cloudRunJSON `json:"cloudRun,omitempty"`
	CloudFunction string        `json:"cloudFunction,omitempty"`
	Workflow      string        `json:"workflow,omitempty"`
}

// triggerJSON is the Eventarc v1 Trigger resource shape (subset the driver can
// store).
type triggerJSON struct {
	Name           string            `json:"name,omitempty"`
	EventFilters   []eventFilterJSON `json:"eventFilters,omitempty"`
	ServiceAccount string            `json:"serviceAccount,omitempty"`
	Destination    *destinationJSON  `json:"destination,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	CreateTime     string            `json:"createTime,omitempty"`
	UpdateTime     string            `json:"updateTime,omitempty"`
	UID            string            `json:"uid,omitempty"`
}

type listTriggersResponse struct {
	Triggers      []triggerJSON `json:"triggers"`
	NextPageToken string        `json:"nextPageToken,omitempty"`
}

// operationJSON is a google.longrunning.Operation. Eventarc's create and delete
// are async; the mock returns a completed operation immediately with the
// resulting resource in Response (create) or nil (delete).
type operationJSON struct {
	Name     string `json:"name"`
	Done     bool   `json:"done"`
	Response any    `json:"response,omitempty"`
}

// triggerResourceName builds the fully-qualified Eventarc trigger name.
func triggerResourceName(project, location, id string) string {
	return "projects/" + project + "/locations/" + location + "/triggers/" + id
}

// channelName is the synthesized event-bus name backing a location's triggers.
// It has no Eventarc analogue and is never surfaced to the SDK.
func channelName(location string) string {
	return "eventarc-" + location
}

// encodeEventPattern serializes a trigger's event filters into the driver rule's
// EventPattern string so getTrigger can reconstruct them. Returns "" when there
// are no filters.
func encodeEventPattern(filters []eventFilterJSON) string {
	if len(filters) == 0 {
		return ""
	}

	b, err := json.Marshal(filters)
	if err != nil {
		return ""
	}

	return string(b)
}

// decodeEventPattern is the inverse of encodeEventPattern.
func decodeEventPattern(pattern string) []eventFilterJSON {
	if pattern == "" {
		return nil
	}

	var filters []eventFilterJSON
	if err := json.Unmarshal([]byte(pattern), &filters); err != nil {
		return nil
	}

	return filters
}

// destinationTarget folds a trigger destination into the single driver Target
// the rule stores. The destination is serialized into the target's Input so it
// round-trips faithfully; the ARN carries a human-readable summary.
func destinationTarget(dest *destinationJSON) (ebdriver.Target, bool) {
	if dest == nil {
		return ebdriver.Target{}, false
	}

	input, err := json.Marshal(dest)
	if err != nil {
		return ebdriver.Target{}, false
	}

	return ebdriver.Target{
		ID:    destinationTargetID,
		ARN:   destinationSummary(dest),
		Input: string(input),
	}, true
}

// destinationFromTargets reconstructs the trigger destination from the driver
// rule's targets.
func destinationFromTargets(targets []ebdriver.Target) *destinationJSON {
	for i := range targets {
		if targets[i].ID != destinationTargetID || targets[i].Input == "" {
			continue
		}

		var dest destinationJSON
		if err := json.Unmarshal([]byte(targets[i].Input), &dest); err != nil {
			return nil
		}

		return &dest
	}

	return nil
}

// destinationSummary is a short human-readable identifier for a destination,
// used as the driver target's ARN.
func destinationSummary(dest *destinationJSON) string {
	switch {
	case dest.CloudRun != nil && dest.CloudRun.Service != "":
		return "cloudRun/" + dest.CloudRun.Service
	case dest.CloudFunction != "":
		return dest.CloudFunction
	case dest.Workflow != "":
		return dest.Workflow
	default:
		return ""
	}
}

// toTriggerJSON converts a driver rule into its Eventarc Trigger element.
func toTriggerJSON(project, location string, rule *ebdriver.Rule) triggerJSON {
	return triggerJSON{
		Name:         triggerResourceName(project, location, rule.Name),
		EventFilters: decodeEventPattern(rule.EventPattern),
		Destination:  destinationFromTargets(rule.Targets),
		CreateTime:   rule.CreatedAt,
		UpdateTime:   rule.CreatedAt,
	}
}
