// Package monitoring implements the GCP Cloud Monitoring REST API surface.
// cloud.google.com/go/monitoring uses gRPC by default; this handler covers the
// REST equivalent (google.golang.org/api/monitoring/v3) for HTTP-level testing.
//
// Supported collections under /v3/projects/{p}/:
//
//	alertPolicies         — create/get/list/patch/delete
//	timeSeries            — list (read metric points) / create (ingest points)
//	metricDescriptors     — list/get/create
//	notificationChannels  — create/list/get/delete
package monitoring

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

const (
	contentTypeJSON = "application/json"
	maxBodyBytes    = 1 << 20
)

// Handler serves GCP Cloud Monitoring REST requests.
//
// The portable monitoring driver models a threshold Alarm, not GCP's richer
// alert-policy shape (conditions, combiner, notificationChannels, userLabels).
// To avoid dropping those on read, the full policy is held here keyed by its
// opaque numeric id; the driver alarm is kept as an existence marker. Custom
// metric descriptors created via metricDescriptors.create are held here too.
type Handler struct {
	mon mondriver.Monitoring

	mu          sync.RWMutex
	policies    map[string]alertPolicy      // keyed by opaque policy id
	descriptors map[string]metricDescriptor // custom descriptors keyed by metric type
	seq         atomic.Uint64
}

// New returns a Cloud Monitoring handler.
func New(m mondriver.Monitoring) *Handler {
	return &Handler{
		mon:         m,
		policies:    make(map[string]alertPolicy),
		descriptors: make(map[string]metricDescriptor),
	}
}

// monitoringCollections is the set of Cloud Monitoring collection segments this
// handler serves under /v3/projects/{p}/.
func monitoringCollections() []string {
	return []string{"alertPolicies", "timeSeries", "metricDescriptors", "notificationChannels"}
}

// Matches returns true for the Cloud Monitoring collections under /v3/projects/.
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path
	if !strings.HasPrefix(p, "/v3/projects/") {
		return false
	}

	for _, c := range monitoringCollections() {
		if strings.Contains(p, "/"+c) {
			return true
		}
	}

	return false
}

// ServeHTTP routes by the collection segment, then by method/resource shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	const (
		minParts   = 4 // v3 / projects / {p} / {collection}
		idxProject = 2
		idxColl    = 3
		idxRest    = 4
	)

	if len(parts) < minParts {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown path")
		return
	}

	project := parts[idxProject]
	rest := parts[idxRest:]

	switch parts[idxColl] {
	case "alertPolicies":
		h.serveAlertPolicies(w, r, project, rest)
	case "timeSeries":
		h.serveTimeSeries(w, r, project)
	case "metricDescriptors":
		h.serveMetricDescriptors(w, r, project, rest)
	case "notificationChannels":
		h.serveNotificationChannels(w, r, project, rest)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "unknown path")
	}
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, statusCode, msg string) {
	writeJSON(w, status, errorEnvelope{
		Error: errorBody{Code: status, Message: msg, Status: statusCode},
	})
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
