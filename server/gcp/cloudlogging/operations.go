package cloudlogging

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// writeEntries maps WriteLogEntries onto the driver. Cloud Logging creates a log
// lazily on first write, so we ensure the log group and its default stream exist
// (idempotently) before appending the events. Entries are grouped by their
// effective logName so a single request can target multiple logs.
func (h *Handler) writeEntries(w http.ResponseWriter, r *http.Request) {
	var req writeLogEntriesRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	// Group event batches by log id (each entry may override the request-level
	// logName).
	byLog := make(map[string][]logdriver.LogEvent)

	now := time.Now().UTC()

	for i := range req.Entries {
		e := &req.Entries[i]

		name := e.LogName
		if name == "" {
			name = req.LogName
		}

		logID := logIDFromName(name)
		if logID == "" {
			gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument", "log entry is missing a logName")
			return
		}

		// Cloud Logging assigns a server-side insertId when the writer omits one;
		// clients dedupe on it, so it must always be present on read-back.
		if e.InsertID == "" {
			e.InsertID = genInsertID()
		}

		// The MonitoredResource may be set per-entry or inherited from the
		// request level, and request-level labels are merged into every entry.
		if e.Resource == nil {
			e.Resource = req.Resource
		}

		e.Labels = mergeLabels(req.Labels, e.Labels)

		byLog[logID] = append(byLog[logID], logdriver.LogEvent{
			Timestamp:     parseTimestamp(e.Timestamp, now),
			IngestionTime: now,
			Message:       encodeEntryPayload(e),
		})
	}

	for logID, events := range byLog {
		if err := h.ensureLog(r.Context(), logID); err != nil {
			gcprest.WriteCErr(w, err)
			return
		}

		if err := h.logs.PutLogEvents(r.Context(), logID, defaultStream, events); err != nil {
			gcprest.WriteCErr(w, err)
			return
		}
	}

	// WriteLogEntries returns an empty response body on success.
	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}

// mergeLabels overlays entry-level labels onto request-level labels (entry wins),
// returning nil when neither side has any so the field stays omitted on read.
func mergeLabels(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}

	out := make(map[string]string, len(base)+len(override))

	for k, v := range base {
		out[k] = v
	}

	for k, v := range override {
		out[k] = v
	}

	return out
}

// ensureLog creates the log group and its default stream if they do not already
// exist. Both AlreadyExists results are benign — a log accreting more entries.
func (h *Handler) ensureLog(ctx context.Context, logID string) error {
	if _, err := h.logs.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: logID}); err != nil && !cerrors.IsAlreadyExists(err) {
		return err
	}

	if _, err := h.logs.CreateLogStream(ctx, logID, defaultStream); err != nil && !cerrors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

// logEntry pairs a driver event with the log id it belongs to, so entries read
// across many logs still carry their own logName.
type logEntry struct {
	logID string
	event logdriver.LogEvent
}

// allEntriesLimit fetches every stored event from a log so the wire layer can
// sort and page across the full set (the driver's Limit truncates by insertion
// order, which would corrupt a timestamp-ordered page).
const allEntriesLimit = 1 << 30

// defaultEntriesPageSize bounds a page when the caller sets no pageSize.
const defaultEntriesPageSize = 1000

// listEntries maps ListLogEntries onto the driver. The log to read is taken from
// the filter's `logName=` clause; when absent, entries are read across every log
// in the project (matching `gcloud logging read` / read-all). Results are ordered
// by timestamp and paged via pageSize/pageToken.
func (h *Handler) listEntries(w http.ResponseWriter, r *http.Request) {
	var req listLogEntriesRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	project := projectFromResourceNames(req.ResourceNames)

	entries, err := h.gatherEntries(r, project, logIDFromFilter(req.Filter))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// Cloud Logging orders by timestamp — ascending by default, descending for
	// "timestamp desc". Sort by the entry timestamp rather than assuming the
	// driver's insertion order matches (out-of-order writes must still sort).
	desc := strings.Contains(strings.ToLower(req.OrderBy), "desc")

	sort.SliceStable(entries, func(i, j int) bool {
		if desc {
			return entries[i].event.Timestamp.After(entries[j].event.Timestamp)
		}

		return entries[i].event.Timestamp.Before(entries[j].event.Timestamp)
	})

	page, next := pageEntries(entries, req.PageSize, req.PageToken)

	out := make([]logEntryJSON, 0, len(page))
	for i := range page {
		out = append(out, toLogEntryJSON(project, page[i].logID, &page[i].event))
	}

	gcprest.WriteJSON(w, http.StatusOK, listLogEntriesResponse{Entries: out, NextPageToken: next})
}

// gatherEntries collects the events to list. When logID is set, only that log is
// read (a missing log is a not-found, as real Cloud Logging surfaces for a
// single-log filter); when logID is empty, every log in the project is read.
func (h *Handler) gatherEntries(r *http.Request, project, logID string) ([]logEntry, error) {
	if logID != "" {
		events, err := h.logs.GetLogEvents(r.Context(), &logdriver.LogQueryInput{LogGroup: logID, Limit: allEntriesLimit})
		if err != nil {
			return nil, err
		}

		return tagEntries(logID, events), nil
	}

	groups, err := h.logs.ListLogGroups(r.Context(), scope.Scope{Project: project})
	if err != nil {
		return nil, err
	}

	var all []logEntry

	for i := range groups {
		events, err := h.logs.GetLogEvents(r.Context(), &logdriver.LogQueryInput{LogGroup: groups[i].Name, Limit: allEntriesLimit})
		if err != nil {
			return nil, err
		}

		all = append(all, tagEntries(groups[i].Name, events)...)
	}

	return all, nil
}

func tagEntries(logID string, events []logdriver.LogEvent) []logEntry {
	out := make([]logEntry, len(events))
	for i := range events {
		out[i] = logEntry{logID: logID, event: events[i]}
	}

	return out
}

// pageEntries returns the requested page of entries and the token for the next
// page ("" when the page is the last).
func pageEntries(entries []logEntry, pageSize int, pageToken string) (page []logEntry, next string) {
	size := pageSize
	if size <= 0 {
		size = defaultEntriesPageSize
	}

	start := decodePageToken(pageToken)
	if start > len(entries) {
		start = len(entries)
	}

	end := start + size
	if end >= len(entries) {
		return entries[start:], ""
	}

	return entries[start:end], encodePageToken(end)
}

// listLogs maps ListLogs onto ListLogGroups, returning fully-qualified log
// names under the project.
func (h *Handler) listLogs(w http.ResponseWriter, r *http.Request) {
	project := projectFromPath(r.URL.Path)

	infos, err := h.logs.ListLogGroups(r.Context(), scope.Scope{Project: project})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	names := make([]string, 0, len(infos))
	for i := range infos {
		names = append(names, logNameFor(project, infos[i].Name))
	}

	gcprest.WriteJSON(w, http.StatusOK, listLogsResponse{LogNames: names})
}

// deleteLog maps DeleteLog onto DeleteLogGroup. The {logID} URL segment arrives
// percent-decoded by net/http, so it is used directly.
func (h *Handler) deleteLog(w http.ResponseWriter, r *http.Request, logID string) {
	if err := h.logs.DeleteLogGroup(r.Context(), logID); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// DeleteLog returns an empty (Empty) response body.
	gcprest.WriteJSON(w, http.StatusOK, struct{}{})
}
