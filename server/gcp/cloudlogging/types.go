package cloudlogging

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// logEntryJSON is the subset of the Cloud Logging LogEntry resource we model.
// The driver stores only a message string, so the structured fields
// (severity, jsonPayload, labels, insertId) are JSON-enveloped into it on
// write and reconstructed on read — see encode/decodeEntryPayload.
type logEntryJSON struct {
	LogName     string            `json:"logName,omitempty"`
	Timestamp   string            `json:"timestamp,omitempty"`
	TextPayload string            `json:"textPayload,omitempty"`
	JSONPayload map[string]any    `json:"jsonPayload,omitempty"`
	InsertID    string            `json:"insertId,omitempty"`
	Severity    string            `json:"severity,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// entryPayload is the JSON envelope stored in the driver's message string so a
// log entry's structured fields survive a write→read round-trip.
type entryPayload struct {
	Text        string            `json:"t,omitempty"`
	JSONPayload map[string]any    `json:"j,omitempty"`
	Severity    string            `json:"s,omitempty"`
	InsertID    string            `json:"i,omitempty"`
	Labels      map[string]string `json:"l,omitempty"`
}

// entryPayloadPrefix marks a driver message that carries an encoded envelope.
// A message without it is treated as a plain textPayload (backward compatible).
const entryPayloadPrefix = "\x00cloudemu-log\x00"

func encodeEntryPayload(e *logEntryJSON) string {
	// Plain text with no structured fields stays a bare string, so logs written
	// by other means still read back naturally.
	if e.Severity == "" && e.InsertID == "" && len(e.JSONPayload) == 0 && len(e.Labels) == 0 {
		return e.TextPayload
	}

	b, err := json.Marshal(entryPayload{
		Text:        e.TextPayload,
		JSONPayload: e.JSONPayload,
		Severity:    e.Severity,
		InsertID:    e.InsertID,
		Labels:      e.Labels,
	})
	if err != nil {
		return e.TextPayload
	}

	return entryPayloadPrefix + string(b)
}

func decodeEntryPayload(msg string, out *logEntryJSON) {
	if !strings.HasPrefix(msg, entryPayloadPrefix) {
		out.TextPayload = msg
		return
	}

	var p entryPayload
	if err := json.Unmarshal([]byte(strings.TrimPrefix(msg, entryPayloadPrefix)), &p); err != nil {
		out.TextPayload = msg
		return
	}

	out.TextPayload = p.Text
	out.JSONPayload = p.JSONPayload
	out.Severity = p.Severity
	out.InsertID = p.InsertID
	out.Labels = p.Labels
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

	// Find "logName" as a whole token, not as a substring of e.g.
	// "logName_suffix": the char after the key must be a separator (space,
	// '=' or ':'), and the char before it must not be an identifier char.
	i := -1
	for from := 0; ; {
		j := strings.Index(filter[from:], key)
		if j < 0 {
			break
		}
		at := from + j
		before := byte(' ')
		if at > 0 {
			before = filter[at-1]
		}
		after := byte(' ')
		if at+len(key) < len(filter) {
			after = filter[at+len(key)]
		}
		isIdent := func(b byte) bool {
			return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
		}
		if !isIdent(before) && (after == ' ' || after == '\t' || after == '=' || after == ':') {
			i = at
			break
		}
		from = at + len(key)
	}
	if i < 0 {
		return ""
	}

	rest := strings.TrimSpace(filter[i+len(key):])
	rest = strings.TrimPrefix(rest, "=")
	rest = strings.TrimPrefix(rest, ":")
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
	out := logEntryJSON{
		LogName:   logNameFor(project, logID),
		Timestamp: e.Timestamp.UTC().Format(time.RFC3339Nano),
	}

	decodeEntryPayload(e.Message, &out)

	return out
}
