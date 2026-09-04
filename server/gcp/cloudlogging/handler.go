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
//	POST   /v2/entries:write                                  — WriteLogEntries -> (lazy CreateLogGroup) + PutLogEvents
//	POST   /v2/entries:list                                   — ListLogEntries  -> GetLogEvents
//	GET    /v2/projects/{p}/logs                               — ListLogs        -> ListLogGroups
//	DELETE /v2/projects/{p}/logs/{logid}                       — DeleteLog       -> DeleteLogGroup
//	POST   /v2/projects/{p}/locations/{l}/buckets              — CreateBucket    -> GCPLogging.CreateBucket
//	GET    /v2/projects/{p}/locations/{l}/buckets              — ListBuckets     -> GCPLogging.ListBuckets
//	GET    /v2/projects/{p}/locations/{l}/buckets/{b}          — GetBucket       -> GCPLogging.GetBucket
//	PATCH  /v2/projects/{p}/locations/{l}/buckets/{b}          — UpdateBucket    -> GCPLogging.UpdateBucket
//	DELETE /v2/projects/{p}/locations/{l}/buckets/{b}          — DeleteBucket    -> GCPLogging.DeleteBucket
//
// Export sinks (projects.sinks), log-based metrics (projects.metrics), and log
// buckets (projects.locations.buckets) — GCP resource surfaces with no
// cross-provider equivalent — are served through the optional
// driver.GCPLogging interface, which the GCP backend implements.
//
// The /v2/ URL space is disjoint from the /v1/projects/ family (Firestore, IAM,
// …), /compute/v1/, and /dns/v1/, so registration order relative to them is
// unconstrained. Registered before the GCS fallback for consistency.
package cloudlogging

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

const (
	basePrefix        = "/v2/"
	entriesWrite      = "/v2/entries:write"
	entriesList       = "/v2/entries:list"
	logsCollection    = "logs"
	sinksCollection   = "sinks"
	metricsCollection = "metrics"
	bucketsCollection = "buckets"
	projectsSeg       = "projects"
	locationsSeg      = "locations"
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

	if _, _, _, ok := bucketsPath(p); ok {
		return true
	}

	return collectionPath(p, logsCollection) != "" ||
		collectionPath(p, sinksCollection) != "" ||
		collectionPath(p, metricsCollection) != ""
}

// collectionPath returns the tail after "/v2/projects/{p}/{collection}" for a
// matching URL, or "" when p is not under that collection. A bare collection
// returns "/".
func collectionPath(p, collection string) string {
	if !strings.HasPrefix(p, basePrefix) {
		return ""
	}

	// A collection URL is projects/{p}/{collection}[/{tail}...] — at least the
	// three leading segments must be present.
	const collectionSegments = 3

	parts := strings.Split(strings.TrimPrefix(p, basePrefix), "/")
	if len(parts) < collectionSegments || parts[0] != projectsSeg || parts[2] != collection {
		return ""
	}

	if len(parts) == collectionSegments {
		return "/"
	}

	return "/" + strings.Join(parts[collectionSegments:], "/")
}

// bucketsPath parses a Cloud Logging buckets URL of the form
// "/v2/projects/{project}/locations/{location}/buckets[/{bucketID}]". ok is
// false when p is not under this shape; tail is "/" for the bare collection.
func bucketsPath(p string) (project, location, tail string, ok bool) {
	if !strings.HasPrefix(p, basePrefix) {
		return "", "", "", false
	}

	// projects/{p}/locations/{l}/buckets[/{tail}...] — at least the five
	// leading segments must be present.
	const bucketsSegments = 5

	parts := strings.Split(strings.TrimPrefix(p, basePrefix), "/")
	if len(parts) < bucketsSegments || parts[0] != projectsSeg || parts[2] != locationsSeg || parts[4] != bucketsCollection {
		return "", "", "", false
	}

	project, location = parts[1], parts[3]

	if len(parts) == bucketsSegments {
		return project, location, "/", true
	}

	return project, location, "/" + strings.Join(parts[bucketsSegments:], "/"), true
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

	if project, location, tail, ok := bucketsPath(r.URL.Path); ok {
		h.routeBuckets(w, r, project, location, tail)
		return
	}

	if tail := collectionPath(r.URL.Path, logsCollection); tail != "" {
		h.routeLogs(w, r, tail)
		return
	}

	if tail := collectionPath(r.URL.Path, sinksCollection); tail != "" {
		h.routeSinks(w, r, tail)
		return
	}

	if tail := collectionPath(r.URL.Path, metricsCollection); tail != "" {
		h.routeMetrics(w, r, tail)
		return
	}

	gcprest.WriteError(w, http.StatusNotFound, "notFound", "unrecognized Cloud Logging path")
}

func (h *Handler) routeLogs(w http.ResponseWriter, r *http.Request, tail string) {
	if tail == "/" {
		h.serveLogsCollection(w, r)
		return
	}

	h.serveLog(w, r, strings.TrimPrefix(tail, "/"))
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
