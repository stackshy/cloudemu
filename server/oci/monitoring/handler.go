// Package monitoring implements OCI's Monitoring REST API.
//
// Supported operations, all under the /20180401 API version:
//
//	POST   /metrics                               — PostMetricData
//	POST   /metrics/actions/listMetrics           — ListMetrics
//	POST   /metrics/actions/summarizeMetricsData  — SummarizeMetricsData
//	POST   /alarms                                — CreateAlarm
//	GET    /alarms                                — ListAlarms
//	GET    /alarms/status                         — ListAlarmsStatus
//	GET    /alarms/{alarmId}                      — GetAlarm
//	PUT    /alarms/{alarmId}                      — UpdateAlarm
//	DELETE /alarms/{alarmId}                      — DeleteAlarm
//	GET    /alarms/{alarmId}/history              — GetAlarmHistory
//
// Alarm mutations are synchronous in real OCI Monitoring, so none of them
// returns a work request.
package monitoring

import (
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// apiVersion is Monitoring's path prefix, distinct from every other OCI service.
const apiVersion = "20180401"

// Path segments this handler routes on.
const (
	resourceMetrics = "metrics"
	resourceAlarms  = "alarms"
	segmentActions  = "actions"
	segmentStatus   = "status"
	segmentHistory  = "history"

	actionList      = "listMetrics"
	actionSummarize = "summarizeMetricsData"
)

// subPathParts is the segment count of /{resource}/{a}/{b}.
const subPathParts = 2

// OCI error codes this handler raises itself.
const (
	codeNotFound         = "NotAuthorizedOrNotFound"
	codeMethodNotAllowed = "MethodNotAllowed"
	codeNotImplemented   = "NotImplemented"
	codeInvalidParameter = "InvalidParameter"
)

// Handler serves OCI Monitoring requests.
type Handler struct {
	mon mondriver.OCIMonitoring
}

// New returns a Monitoring handler. A driver without OCI's compartment-scoped
// capability answers NotImplemented.
func New(mon mondriver.Monitoring) *Handler {
	ocimon, _ := mon.(mondriver.OCIMonitoring)

	return &Handler{mon: ocimon}
}

// Matches claims /20180401/metrics and /20180401/alarms, and nothing else.
func (*Handler) Matches(r *http.Request) bool {
	_, _, ok := parsePath(r.URL.Path)

	return ok
}

// ServeHTTP routes a request to its operation.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resource, rest, ok := parsePath(r.URL.Path)
	if !ok {
		writeNotFound(w, r)
		return
	}

	if h.mon == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"monitoring driver does not support OCI compartments")

		return
	}

	if resource == resourceMetrics {
		h.serveMetrics(w, r, rest)
		return
	}

	h.serveAlarms(w, r, rest)
}

func (h *Handler) serveMetrics(w http.ResponseWriter, r *http.Request, rest []string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, r)
		return
	}

	if len(rest) == 0 {
		h.postMetricData(w, r)
		return
	}

	if len(rest) != subPathParts || rest[0] != segmentActions {
		writeNotFound(w, r)
		return
	}

	switch rest[1] {
	case actionList:
		h.listMetrics(w, r)
	case actionSummarize:
		h.summarizeMetricsData(w, r)
	default:
		writeNotFound(w, r)
	}
}

func (h *Handler) serveAlarms(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		h.serveAlarmCollection(w, r)
	case 1:
		h.serveAlarm(w, r, rest[0])
	case subPathParts:
		if rest[1] != segmentHistory || r.Method != http.MethodGet {
			writeNotFound(w, r)
			return
		}

		h.getAlarmHistory(w, r, rest[0])
	default:
		writeNotFound(w, r)
	}
}

func (h *Handler) serveAlarmCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createAlarm(w, r)
	case http.MethodGet:
		h.listAlarms(w, r)
	default:
		writeMethodNotAllowed(w, r)
	}
}

// serveAlarm routes the single-alarm paths. ListAlarmsStatus shares their
// shape, so /alarms/status is claimed before an OCID is assumed.
func (h *Handler) serveAlarm(w http.ResponseWriter, r *http.Request, id string) {
	if id == segmentStatus {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, r)
			return
		}

		h.listAlarmsStatus(w, r)

		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getAlarm(w, r, id)
	case http.MethodPut:
		h.updateAlarm(w, r, id)
	case http.MethodDelete:
		h.deleteAlarm(w, r, id)
	default:
		writeMethodNotAllowed(w, r)
	}
}

// parsePath splits /20180401/{metrics|alarms}[/...], rejecting every other path.
func parsePath(urlPath string) (resource string, rest []string, ok bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < subPathParts || parts[0] != apiVersion {
		return "", nil, false
	}

	if parts[1] != resourceMetrics && parts[1] != resourceAlarms {
		return "", nil, false
	}

	return parts[1], parts[subPathParts:], true
}

func writeNotFound(w http.ResponseWriter, r *http.Request) {
	ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown monitoring path "+r.URL.Path)
}

func writeMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	ocirest.WriteError(w, r, http.StatusMethodNotAllowed, codeMethodNotAllowed, r.Method+" is not allowed on "+r.URL.Path)
}

// timestamp renders a time the way OCI does, and omits a zero one.
func timestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339)
}
