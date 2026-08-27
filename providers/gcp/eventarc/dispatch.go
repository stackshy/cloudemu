package eventarc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	crdriver "github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// cloudRunDeliveryTimeout bounds how long a single Cloud Run destination
// delivery waits before the CloudEvent POST is abandoned, so an unreachable
// *.run.app URL can never hang a PutEvents call.
const cloudRunDeliveryTimeout = 10 * time.Second

// destinationTargetID mirrors the id the server/gcp/eventarc handler folds a
// trigger's destination under on the driver rule. Keep the two in sync.
const destinationTargetID = "destination"

// cloudEventSpecVersion is the CloudEvents spec version Eventarc delivers with.
const cloudEventSpecVersion = "1.0"

// FunctionInvoker invokes a Cloud Function by its (short) name with the
// CloudEvent payload. The cloudfunctions.Mock satisfies this via Invoke, so a
// trigger whose destination is a Cloud Function fires the function on a matching
// event — mirroring the Pub/Sub -> Cloud Functions delivery wired in #803.
type FunctionInvoker interface {
	Invoke(ctx context.Context, input sdrv.InvokeInput) (*sdrv.InvokeOutput, error)
}

// CloudRunResolver resolves a Cloud Run service by name so its serving URL can
// be looked up for HTTP delivery. The cloudrun.Mock satisfies this via
// GetService. Cloud Run has no in-process invoke seam (it models CRUD only), so
// a Cloud Run destination is delivered to over HTTP at the service's URL.
type CloudRunResolver interface {
	GetService(ctx context.Context, name string) (*crdriver.Service, error)
}

// cloudRunDestination is the Cloud Run subset of a trigger destination.
type cloudRunDestination struct {
	Service string `json:"service,omitempty"`
	Region  string `json:"region,omitempty"`
	Path    string `json:"path,omitempty"`
}

// triggerDestination is the parsed form of the Eventarc destination the server
// handler serializes into the driver Target's Input. It intentionally mirrors
// server/gcp/eventarc.destinationJSON — the two packages can't share a type
// (provider must not import server), so the wire-compatible subset is redefined
// here.
type triggerDestination struct {
	CloudRun      *cloudRunDestination `json:"cloudRun,omitempty"`
	CloudFunction string               `json:"cloudFunction,omitempty"`
	Workflow      string               `json:"workflow,omitempty"`
}

// eventFilter is the Eventarc EventFilter shape the server handler serializes
// into the driver rule's EventPattern (a JSON array). Redefined here for the
// same reason as triggerDestination.
type eventFilter struct {
	Attribute string `json:"attribute,omitempty"`
	Operator  string `json:"operator,omitempty"`
	Value     string `json:"value,omitempty"`
}

// cloudEventEnvelope is the structured-mode CloudEvents 1.0 JSON delivered to a
// trigger destination. Real Eventarc delivers gen2/Cloud Run destinations a
// binary-mode HTTP request, but structured JSON carries the same attributes and
// is what this mock delivers to both destination kinds.
type cloudEventEnvelope struct {
	SpecVersion string          `json:"specversion"`
	ID          string          `json:"id"`
	Source      string          `json:"source,omitempty"`
	Type        string          `json:"type,omitempty"`
	Subject     string          `json:"subject,omitempty"`
	Time        string          `json:"time,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
}

// dispatchEvent delivers a stored event to every ENABLED trigger on the same
// bus whose eventFilters match it. Best-effort: a nil peer, an unparseable
// destination, or a failing delivery is swallowed so a publish never fails.
func (m *Mock) dispatchEvent(ctx context.Context, bd *busData, event *ebdriver.Event) {
	if m.functions == nil && m.cloudRun == nil {
		return
	}

	var body []byte

	for _, rd := range bd.rules.All() {
		if rd.rule.State != defaultTriggerState {
			continue
		}

		if !eventMatchesFilters(event, decodeFilters(rd.rule.EventPattern)) {
			continue
		}

		dest := decodeDestination(rd.rule.Targets)
		if dest == nil {
			continue
		}

		if body == nil {
			body = renderCloudEvent(event)
		}

		m.dispatchDestination(ctx, dest, body)
	}
}

// dispatchDestination routes one rendered CloudEvent to a single trigger
// destination. A nil peer is skipped gracefully.
func (m *Mock) dispatchDestination(ctx context.Context, dest *triggerDestination, body []byte) {
	switch {
	case dest.CloudFunction != "":
		if m.functions == nil {
			return
		}

		name := lastSegment(dest.CloudFunction)
		if name == "" {
			return
		}

		_, _ = m.functions.Invoke(ctx, sdrv.InvokeInput{
			FunctionName: name,
			Payload:      body,
			InvokeType:   "Event",
		})
	case dest.CloudRun != nil && dest.CloudRun.Service != "":
		m.deliverCloudRun(ctx, dest.CloudRun, body)
	}
}

// deliverCloudRun resolves the Cloud Run service's serving URL and POSTs the
// CloudEvent to it (best-effort, bounded). Cloud Run models CRUD only, so
// delivery is over HTTP at the service URL rather than an in-process invoke.
func (m *Mock) deliverCloudRun(ctx context.Context, dest *cloudRunDestination, body []byte) {
	if m.cloudRun == nil {
		return
	}

	svc, err := m.cloudRun.GetService(ctx, dest.Service)
	if err != nil || svc == nil || svc.URI == "" {
		return
	}

	url := svc.URI + normalizePath(dest.Path)

	reqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cloudRunDeliveryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/cloudevents+json; charset=utf-8")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)
}

// renderCloudEvent builds the structured CloudEvents 1.0 envelope delivered to a
// destination.
func renderCloudEvent(event *ebdriver.Event) []byte {
	env := cloudEventEnvelope{
		SpecVersion: cloudEventSpecVersion,
		ID:          event.ID,
		Source:      event.Source,
		Type:        event.DetailType,
		Subject:     event.Subject,
	}

	if !event.Time.IsZero() {
		env.Time = event.Time.UTC().Format(time.RFC3339Nano)
	}

	if event.Detail != "" && json.Valid([]byte(event.Detail)) {
		env.Data = json.RawMessage(event.Detail)
	}

	body, err := json.Marshal(env)
	if err != nil {
		return []byte("{}")
	}

	return body
}

// eventMatchesFilters reports whether the event satisfies every eventFilter
// (AND semantics, matching real Eventarc). An empty filter set matches all
// events.
func eventMatchesFilters(event *ebdriver.Event, filters []eventFilter) bool {
	for i := range filters {
		if filters[i].Attribute == "" {
			continue
		}

		got, ok := eventAttribute(event, filters[i].Attribute)
		if !ok || got != filters[i].Value {
			return false
		}
	}

	return true
}

// eventAttribute resolves a CloudEvent attribute value from the event. The core
// attributes come from the event's own fields; any other attribute is looked up
// as a top-level string field of the event's data (Detail).
func eventAttribute(event *ebdriver.Event, attr string) (string, bool) {
	switch attr {
	case "type":
		return event.DetailType, event.DetailType != ""
	case "source":
		return event.Source, event.Source != ""
	case "subject":
		return event.Subject, event.Subject != ""
	}

	return detailStringField(event.Detail, attr)
}

// detailStringField extracts a top-level string field from a JSON object.
func detailStringField(detail, key string) (string, bool) {
	if detail == "" {
		return "", false
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(detail), &obj); err != nil {
		return "", false
	}

	if v, ok := obj[key].(string); ok {
		return v, true
	}

	return "", false
}

// decodeFilters deserializes the trigger's eventFilters from the rule's
// EventPattern. Returns nil when the pattern is empty or not the Eventarc
// filter-array shape.
func decodeFilters(pattern string) []eventFilter {
	if pattern == "" {
		return nil
	}

	var filters []eventFilter
	if err := json.Unmarshal([]byte(pattern), &filters); err != nil {
		return nil
	}

	return filters
}

// decodeDestination reconstructs a trigger's destination from the driver rule's
// targets. Returns nil when no destination target is stored.
func decodeDestination(targets []ebdriver.Target) *triggerDestination {
	for i := range targets {
		if targets[i].ID != destinationTargetID || targets[i].Input == "" {
			continue
		}

		var dest triggerDestination
		if err := json.Unmarshal([]byte(targets[i].Input), &dest); err != nil {
			return nil
		}

		return &dest
	}

	return nil
}

// lastSegment returns the trailing path segment of a resource name (or the input
// itself when it has no "/").
func lastSegment(name string) string {
	trimmed := strings.TrimRight(name, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}

	return trimmed
}

// normalizePath ensures a destination path is a leading-slash suffix (or empty).
func normalizePath(path string) string {
	if path == "" || path == "/" {
		return ""
	}

	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}

	return path
}
