package cloudlogging

import (
	"net/url"
	"strings"
	"time"

	logdriver "github.com/stackshy/cloudemu/logging/driver"
)

// logEntryJSON is the subset of the Cloud Logging LogEntry resource we model:
// a text payload plus a timestamp, keyed by logName. The driver has no notion
// of severity or structured payloads, so only textPayload round-trips.
type logEntryJSON struct {
	LogName     string `json:"logName,omitempty"`
	Timestamp   string `json:"timestamp,omitempty"`
	TextPayload string `json:"textPayload,omitempty"`
	InsertID    string `json:"insertId,omitempty"`
	Severity    string `json:"severity,omitempty"`
}

// writeLogEntriesRequest is the entries:write body. logName/resource may be set
// at the request level and inherited by entries that omit their own logName.
type writeLogEntriesRequest struct {
	LogName string         `json:"logName"`
	Entries []logEntryJSON `json:"entries"`
}

// listLogEntriesRequest is the entries:list body.
type listLogEntriesRequest struct {
	ResourceNames []string `json:"resourceNames"`
	Filter        string   `json:"filter"`
	PageSize      int      `json:"pageSize"`
	OrderBy       string   `json:"orderBy"`
}

type listLogEntriesResponse struct {
	Entries []logEntryJSON `json:"entries"`
}

type listLogsResponse struct {
	LogNames []string `json:"logNames,omitempty"`
}

// logIDFromName extracts the log id (the last path segment) from a Cloud
// Logging logName, which has the form "projects/{project}/logs/{logID}". The
// logID itself may be URL-encoded (it can contain slashes). Returns "" when the
// name is empty or malformed.
func logIDFromName(logName string) string {
	if logName == "" {
		return ""
	}

	const marker = "/logs/"

	i := strings.Index(logName, marker)
	if i < 0 {
		// Bare log id (no projects/.../logs/ prefix): treat as-is.
		if decoded, err := url.PathUnescape(logName); err == nil {
			return decoded
		}

		return logName
	}

	id := logName[i+len(marker):]
	if decoded, err := url.PathUnescape(id); err == nil {
		return decoded
	}

	return id
}

// logNameFor builds a fully-qualified logName for a log id under project.
func logNameFor(project, logID string) string {
	return "projects/" + project + "/logs/" + url.PathEscape(logID)
}

// projectFromResourceNames pulls the project id out of the first
// "projects/{project}" resource name in an entries:list request. Returns ""
// when none is present.
func projectFromResourceNames(names []string) string {
	for _, n := range names {
		parts := strings.Split(n, "/")
		if len(parts) >= 2 && parts[0] == projectsSeg {
			return parts[1]
		}
	}

	return ""
}

// projectFromPath pulls {project} out of a /v2/projects/{project}/logs[...]
// URL path. Returns "" when the path is not a logs path.
func projectFromPath(p string) string {
	if !strings.HasPrefix(p, basePrefix) {
		return ""
	}

	parts := strings.Split(strings.TrimPrefix(p, basePrefix), "/")
	if len(parts) < 2 || parts[0] != projectsSeg {
		return ""
	}

	return parts[1]
}

// logIDFromFilter extracts the log id from a Cloud Logging filter expression of
// the form `logName="projects/p/logs/mylog"` (quotes optional). Returns "" when
// the filter does not scope by logName.
func logIDFromFilter(filter string) string {
	const key = "logName"

	i := strings.Index(filter, key)
	if i < 0 {
		return ""
	}

	rest := strings.TrimSpace(filter[i+len(key):])
	rest = strings.TrimPrefix(rest, "=")
	rest = strings.TrimSpace(rest)
	rest = strings.Trim(rest, `"`)

	// rest may carry a trailing clause (e.g. "... AND severity=ERROR"); cut at
	// the first whitespace so only the logName value remains.
	if j := strings.IndexAny(rest, " \t"); j >= 0 {
		rest = rest[:j]
	}

	return logIDFromName(rest)
}

// parseTimestamp parses an RFC3339(Nano) Cloud Logging timestamp. A zero/empty
// or unparseable value yields the current time so an entry is never dropped for
// a bad clock.
func parseTimestamp(ts string, now time.Time) time.Time {
	if ts == "" {
		return now
	}

	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t
	}

	return now
}

func toLogEntryJSON(project, logID string, e *logdriver.LogEvent) logEntryJSON {
	return logEntryJSON{
		LogName:     logNameFor(project, logID),
		Timestamp:   e.Timestamp.UTC().Format(time.RFC3339Nano),
		TextPayload: e.Message,
	}
}
