package monitor

import (
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// activityLogSuffix is the Activity Log "list management events" API path
// (GET {scope}/providers/Microsoft.Insights/eventtypes/management/values).
const activityLogSuffix = "/providers/microsoft.insights/eventtypes/management/values"

// ActivityLogHandler serves the Azure Activity Log read API. It surfaces the
// management events the wire server auto-records as ARM operations flow through
// it (see the server observer), so a client that lists the Activity Log sees
// real API activity. It is backed by the optional ActivityLogRecorder
// capability; when the monitoring backend doesn't implement it, Matches is
// false so the handler adds nothing.
type ActivityLogHandler struct {
	rec mondriver.ActivityLogRecorder
}

// NewActivityLogHandler returns an Activity Log handler when m implements the
// recorder capability, else nil (so callers register it only when usable).
func NewActivityLogHandler(m mondriver.Monitoring) *ActivityLogHandler {
	rec, ok := m.(mondriver.ActivityLogRecorder)
	if !ok {
		return nil
	}

	return &ActivityLogHandler{rec: rec}
}

// Matches claims GETs on {scope}/providers/Microsoft.Insights/eventtypes/
// management/values. The lowercased-suffix match wins over any resource handler.
func (h *ActivityLogHandler) Matches(r *http.Request) bool {
	if h == nil || h.rec == nil || r.Method != http.MethodGet {
		return false
	}

	return strings.HasSuffix(strings.ToLower(r.URL.Path), activityLogSuffix)
}

func (h *ActivityLogHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := mondriver.ActivityLogQuery{
		ResourceGroup: filterEq(r.URL.Query().Get("$filter"), "resourceGroupName"),
		StartTime:     filterTime(r.URL.Query().Get("$filter"), "ge"),
		EndTime:       filterTime(r.URL.Query().Get("$filter"), "le"),
	}

	events := h.rec.ListActivityLogEvents(q)

	value := make([]map[string]any, 0, len(events))
	for i := range events {
		value = append(value, activityLogJSON(&events[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, map[string]any{"value": value})
}

// activityLogJSON renders one event in the shape the Activity Log API returns.
func activityLogJSON(e *mondriver.ActivityLogEvent) map[string]any {
	return map[string]any{
		"eventDataId":       e.EventID,
		"correlationId":     e.CorrelationID,
		"eventName":         map[string]any{"value": "EndRequest", "localizedValue": "End request"},
		"operationName":     map[string]any{"value": e.OperationName, "localizedValue": e.OperationName},
		"status":            map[string]any{"value": e.Status, "localizedValue": e.Status},
		"level":             e.Level,
		"resourceId":        e.ResourceID,
		"resourceGroupName": e.ResourceGroup,
		"resourceProviderName": map[string]any{
			"value": e.ResourceProvider, "localizedValue": e.ResourceProvider,
		},
		"subscriptionId": e.SubscriptionID,
		"caller":         e.Caller,
		"eventTimestamp": e.EventTimestamp.UTC().Format(time.RFC3339Nano),
		"category":       map[string]any{"value": "Administrative", "localizedValue": "Administrative"},
	}
}

// filterEq pulls the value of "<field> eq '<value>'" from an OData $filter, if
// present. Activity Log filters are simple conjunctions, so a substring scan is
// enough for the fields the emulator supports.
func filterEq(filter, field string) string {
	lower := strings.ToLower(filter)
	key := strings.ToLower(field) + " eq '"

	i := strings.Index(lower, key)
	if i < 0 {
		return ""
	}

	rest := filter[i+len(key):]
	if j := strings.IndexByte(rest, '\''); j >= 0 {
		return rest[:j]
	}

	return ""
}

// filterTime pulls the timestamp of "eventTimestamp <op> '<rfc3339>'" from an
// OData $filter, where op is "ge" (start) or "le" (end). A missing or
// unparseable bound returns the zero time (no bound).
func filterTime(filter, op string) time.Time {
	lower := strings.ToLower(filter)
	key := "eventtimestamp " + op + " '"

	i := strings.Index(lower, key)
	if i < 0 {
		return time.Time{}
	}

	rest := filter[i+len(key):]

	j := strings.IndexByte(rest, '\'')
	if j < 0 {
		return time.Time{}
	}

	t, err := time.Parse(time.RFC3339, rest[:j])
	if err != nil {
		return time.Time{}
	}

	return t
}
