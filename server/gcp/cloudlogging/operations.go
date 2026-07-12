package cloudlogging

import (
	"context"
	"net/http"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
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

		byLog[logID] = append(byLog[logID], logdriver.LogEvent{
			Timestamp: parseTimestamp(e.Timestamp, now),
			Message:   e.TextPayload,
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

// listEntries maps ListLogEntries onto GetLogEvents. The log to read is taken
// from the filter's `logName=` clause; the project comes from resourceNames.
func (h *Handler) listEntries(w http.ResponseWriter, r *http.Request) {
	var req listLogEntriesRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	project := projectFromResourceNames(req.ResourceNames)

	logID := logIDFromFilter(req.Filter)
	if logID == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalidArgument",
			"a logName filter is required to list entries")
		return
	}

	events, err := h.logs.GetLogEvents(r.Context(), &logdriver.LogQueryInput{
		LogGroup: logID,
		Limit:    req.PageSize,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	out := make([]logEntryJSON, 0, len(events))
	for i := range events {
		out = append(out, toLogEntryJSON(project, logID, &events[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, listLogEntriesResponse{Entries: out})
}

// listLogs maps ListLogs onto ListLogGroups, returning fully-qualified log
// names under the project.
func (h *Handler) listLogs(w http.ResponseWriter, r *http.Request) {
	project := projectFromPath(r.URL.Path)

	infos, err := h.logs.ListLogGroups(r.Context())
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
