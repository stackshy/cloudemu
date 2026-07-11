// Package cloudlogging implements the Cloud Logging (logging.googleapis.com) v2
// REST API as a server.Handler. Real google.golang.org/api/logging/v2 clients
// pointed at this server write log entries, list them back, and manage logs
// end-to-end against the shared logging driver.
//
// GCP Cloud Logging has no explicit "create log group" call: a log springs into
// existence on the first entries:write. This handler mirrors that — a write to
// logName "projects/{p}/logs/{logid}" lazily creates the driver log group named
// {logid} plus a default log stream, then appends the events. entries:list maps
// onto GetLogEvents, honoring a `logName=` filter to scope the query.
//
// Driver -> wire mapping:
//
//	POST   /v2/entries:write              — WriteLogEntries -> (lazy CreateLogGroup) + PutLogEvents
//	POST   /v2/entries:list               — ListLogEntries  -> GetLogEvents
//	GET    /v2/projects/{p}/logs          — ListLogs        -> ListLogGroups
//	DELETE /v2/projects/{p}/logs/{logid}  — DeleteLog       -> DeleteLogGroup
//
// The /v2/ URL space is disjoint from the /v1/projects/ family (Firestore, IAM,
// …), /compute/v1/, and /dns/v1/, so registration order relative to them is
// unconstrained. Registered before the GCS fallback for consistency.
//
// Out of scope for this slice: log buckets / sinks / metrics
// (projects.locations.buckets, projects.sinks, projects.metrics) — a separate
// resource surface not backed by the logging driver's group/stream model.
package cloudlogging

import (
	"net/http"
	"strings"

	logdriver "github.com/stackshy/cloudemu/logging/driver"
	"github.com/stackshy/cloudemu/server/wire/gcprest"
)

const (
	basePrefix     = "/v2/"
	entriesWrite   = "/v2/entries:write"
	entriesList    = "/v2/entries:list"
	logsCollection = "logs"
	projectsSeg    = "projects"
)

// defaultStream is the implicit log stream every Cloud Logging write lands in.
// GCP has no stream concept at this layer, but the driver requires one, so we
// funnel all entries for a log through a single well-known stream.
const defaultStream = "default"

// Handler serves logging.googleapis.com v2 requests against a logging driver.
type Handler struct {
	logs logdriver.Logging
}

// New returns a Cloud Logging handler backed by l.
func New(l logdriver.Logging) *Handler {
	return &Handler{logs: l}
}

// Matches claims /v2/entries:{write,list} and /v2/projects/{p}/logs[...] paths —
// the logging.googleapis.com v2 URL space, disjoint from the /v1/projects/
// family and from /compute/v1/ and /dns/v1/. Registered before the GCS
// fallback.
func (*Handler) Matches(r *http.Request) bool {
	p := r.URL.Path
	if p == entriesWrite || p == entriesList {
		return true
	}

	return logsPath(p) != ""
}

// logsPath returns the tail after "/v2/projects/{p}/logs" for a logs URL, or ""
// when p is not a logs path. A bare collection returns "/".
func logsPath(p string) string {
	if !strings.HasPrefix(p, basePrefix) {
		return ""
	}

	parts := strings.Split(strings.TrimPrefix(p, basePrefix), "/")
	if len(parts) < 3 || parts[0] != projectsSeg || parts[2] != logsCollection {
		return ""
	}

	if len(parts) == 3 {
		return "/"
	}

	return "/" + strings.Join(parts[3:], "/")
}

// ServeHTTP routes on the path and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case entriesWrite:
		h.serveEntriesWrite(w, r)
		return
	case entriesList:
		h.serveEntriesList(w, r)
		return
	}

	tail := logsPath(r.URL.Path)
	switch {
	case tail == "/":
		h.serveLogsCollection(w, r)
	case tail != "":
		h.serveLog(w, r, strings.TrimPrefix(tail, "/"))
	default:
		gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Cloud Logging path")
	}
}

func (h *Handler) serveEntriesWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	h.writeEntries(w, r)
}

func (h *Handler) serveEntriesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	h.listEntries(w, r)
}

func (h *Handler) serveLogsCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	h.listLogs(w, r)
}

func (h *Handler) serveLog(w http.ResponseWriter, r *http.Request, logID string) {
	if r.Method != http.MethodDelete {
		writeMethodNotAllowed(w)
		return
	}

	h.deleteLog(w, r, logID)
}

func writeMethodNotAllowed(w http.ResponseWriter) {
	gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
}
