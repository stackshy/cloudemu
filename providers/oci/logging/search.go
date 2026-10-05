package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// SearchRequest is a loggingsearch query over a time range.
type SearchRequest struct {
	Query     string
	TimeStart time.Time
	TimeEnd   time.Time
	Limit     int
	// Offset is how many matching entries to skip, the page cursor's position.
	Offset          int
	ReturnFieldInfo bool
}

// SearchEntry is one entry a search matched, with the log it came from.
type SearchEntry struct {
	LogEntry

	CompartmentID string
	LogGroupID    string
	LogName       string
}

// SearchField describes a field of the returned records.
type SearchField struct {
	Name string
	Type string
}

// SearchResult is what SearchLogs returns.
type SearchResult struct {
	Entries []SearchEntry
	Fields  []SearchField
	// NextOffset is where the next page starts, or 0 when this is the last.
	NextOffset int
}

// searchScope is one target of a search query's search clause:
// compartment[/logGroup[/log]].
type searchScope struct {
	compartmentID string
	logGroupID    string
	logID         string
}

// fieldRef is a where clause's field, resolved when the query is parsed so an
// unmodelled field is rejected before any entry is walked rather than quietly
// matching nothing.
type fieldRef struct {
	// name is the canonical field, or "data" when jsonKey is set.
	name string
	// jsonKey is the top-level key of a JSON payload, for data.<key>.
	jsonKey string
}

// condition is one comparison of a where clause. Pattern may carry * wildcards.
type condition struct {
	field   fieldRef
	negated bool
	pattern string
}

// searchQuery is a parsed OCI Logging search query.
type searchQuery struct {
	scopes     []searchScope
	conditions []condition
	sortDesc   bool
}

// Search stage keywords CloudEmu parses.
const (
	stageSearch = "search"
	stageWhere  = "where"
	stageSort   = "sort"
)

// supportedOperators names what a rejection message points the caller at.
const supportedOperators = "'search', 'where' and 'sort by'"

// Fields naming an entry's time, which is also the only field a sort may name.
const (
	fieldTime     = "time"
	fieldDatetime = "datetime"
)

// Record fields a where clause resolves, with the logContent. prefix stripped,
// and the one type every returned field reports.
const (
	fieldData              = "data"
	fieldID                = "id"
	fieldType              = "type"
	fieldSubject           = "subject"
	fieldSource            = "source"
	fieldOracleCompartment = "oracle.compartmentid"
	fieldOracleLogGroup    = "oracle.loggroupid"
	fieldOracleLog         = "oracle.logid"
	fieldOracleIngested    = "oracle.ingestedtime"
	fieldTypeString        = "STRING"
)

// canonicalFields is the record shape a search returns, reported when the
// caller asks for field info.
//
//nolint:gochecknoglobals // immutable record-shape table.
var canonicalFields = []SearchField{
	{Name: "datetime", Type: fieldTypeString},
	{Name: "logContent.data", Type: fieldTypeString},
	{Name: "logContent.id", Type: fieldTypeString},
	{Name: "logContent.source", Type: fieldTypeString},
	{Name: "logContent.subject", Type: fieldTypeString},
	{Name: "logContent.time", Type: fieldTypeString},
	{Name: "logContent.type", Type: fieldTypeString},
	{Name: "logContent.oracle.compartmentid", Type: fieldTypeString},
	{Name: "logContent.oracle.loggroupid", Type: fieldTypeString},
	{Name: "logContent.oracle.logid", Type: fieldTypeString},
}

// SearchLogs runs a search query over a time range — the loggingsearch data
// plane. A query CloudEmu does not model is rejected by name rather than
// answered with an empty result set.
//
//nolint:gocritic // hugeParam: SearchRequest mirrors the wire request and reads better by value.
func (m *Mock) SearchLogs(_ context.Context, req SearchRequest) (*SearchResult, error) {
	if req.TimeStart.IsZero() || req.TimeEnd.IsZero() {
		return nil, cerrors.New(cerrors.InvalidArgument, "timeStart and timeEnd are required")
	}

	if !req.TimeEnd.After(req.TimeStart) {
		return nil, cerrors.New(cerrors.InvalidArgument, "timeEnd must be after timeStart")
	}

	q, err := parseSearchQuery(req.Query)
	if err != nil {
		return nil, err
	}

	limit, err := resolveLimit(req.Limit)
	if err != nil {
		return nil, err
	}

	if req.Offset < 0 {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "offset %d must not be negative", req.Offset)
	}

	m.mu.RLock()
	matched := m.collect(q, req.TimeStart, req.TimeEnd)
	m.mu.RUnlock()

	sortEntries(matched, q)

	out := &SearchResult{Entries: matched[:0]}

	if req.Offset < len(matched) {
		end := min(req.Offset+limit, len(matched))
		out.Entries = matched[req.Offset:end]

		if end < len(matched) {
			out.NextOffset = end
		}
	}

	if req.ReturnFieldInfo {
		out.Fields = canonicalFields
	}

	return out, nil
}

// collect walks the logs a query selects and keeps the entries in range that
// satisfy every condition. The caller holds mu.
func (m *Mock) collect(q *searchQuery, start, end time.Time) []SearchEntry {
	out := make([]SearchEntry, 0)

	for _, rec := range m.logs.SortedValues() {
		g, ok := m.groups.Get(rec.Log.LogGroupID)
		if !ok || !q.selects(g, &rec.Log) {
			continue
		}

		for i := range rec.Entries {
			e := &rec.Entries[i]
			if e.Time.Before(start) || !e.Time.Before(end) {
				continue
			}

			if q.matches(e, g, &rec.Log) {
				out = append(out, SearchEntry{
					LogEntry:      *e,
					CompartmentID: g.CompartmentID,
					LogGroupID:    rec.Log.LogGroupID,
					LogName:       rec.Log.DisplayName,
				})
			}
		}
	}

	return out
}

// selects reports whether a log falls under any of the query's search scopes.
func (q *searchQuery) selects(g *LogGroup, l *Log) bool {
	for _, s := range q.scopes {
		if s.compartmentID != g.CompartmentID {
			continue
		}

		if s.logGroupID != "" && s.logGroupID != l.LogGroupID {
			continue
		}

		if s.logID != "" && s.logID != l.ID {
			continue
		}

		return true
	}

	return false
}

// matches reports whether an entry satisfies every where condition.
func (q *searchQuery) matches(e *LogEntry, g *LogGroup, l *Log) bool {
	for _, c := range q.conditions {
		if globMatch(c.pattern, fieldValue(e, g, l, c.field)) == c.negated {
			return false
		}
	}

	return true
}

// sortEntries orders the results. Without a sort clause the order is by entry
// time and then id, so a search is reproducible.
func sortEntries(entries []SearchEntry, q *searchQuery) {
	before := func(a, b *SearchEntry) bool {
		if a.Time.Equal(b.Time) {
			return a.ID < b.ID
		}

		return a.Time.Before(b.Time)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if q.sortDesc {
			return before(&entries[j], &entries[i])
		}

		return before(&entries[i], &entries[j])
	})
}

// entryFields are the fields of a returned record a where clause may name,
// with the logContent. prefix OCI writes them under already stripped.
//
//nolint:gochecknoglobals // immutable field lookup table.
var entryFields = map[string]struct{}{
	fieldData: {}, fieldID: {}, fieldType: {}, fieldSubject: {}, fieldSource: {},
	fieldTime: {}, fieldDatetime: {},
	fieldOracleCompartment: {}, fieldOracleLogGroup: {},
	fieldOracleLog: {}, fieldOracleIngested: {},
}

// resolveField canonicalises a field named in a where clause, rejecting one
// CloudEmu cannot resolve. The logContent. prefix is optional, matching how
// OCI writes the field in a search clause.
func resolveField(field string) (fieldRef, error) {
	name := strings.ToLower(strings.TrimSpace(field))
	name = strings.TrimPrefix(name, "logcontent.")

	if key, ok := strings.CutPrefix(name, "data."); ok {
		if key == "" || strings.Contains(key, ".") {
			return fieldRef{}, cerrors.Newf(cerrors.InvalidArgument,
				"unsupported search field %q; CloudEmu resolves a single top-level key of a JSON payload, "+
					"not a nested path", field)
		}

		return fieldRef{name: fieldData, jsonKey: key}, nil
	}

	if _, ok := entryFields[name]; !ok {
		return fieldRef{}, cerrors.Newf(cerrors.InvalidArgument,
			"unsupported search field %q; CloudEmu resolves %s and data.<key> of a JSON payload",
			field, strings.Join(sortedFieldNames(), ", "))
	}

	return fieldRef{name: name}, nil
}

// sortedFieldNames lists the resolvable fields, for a rejection message.
func sortedFieldNames() []string {
	out := make([]string, 0, len(entryFields))
	for name := range entryFields {
		out = append(out, name)
	}

	sort.Strings(out)

	return out
}

// fieldValue reads a resolved field off an entry.
func fieldValue(e *LogEntry, g *LogGroup, l *Log, ref fieldRef) string {
	if ref.jsonKey != "" {
		return jsonField(e.Data, ref.jsonKey)
	}

	switch ref.name {
	case fieldData:
		return e.Data
	case fieldID:
		return e.ID
	case fieldType:
		return e.Type
	case fieldSubject:
		return e.Subject
	case fieldSource:
		return e.Source
	case fieldTime, fieldDatetime:
		return e.Time.UTC().Format(timeFormat)
	default:
		return provenanceValue(e, g, l, ref.name)
	}
}

// provenanceValue reads one of the oracle.* fields OCI stamps onto a record.
func provenanceValue(e *LogEntry, g *LogGroup, l *Log, name string) string {
	switch name {
	case fieldOracleCompartment:
		return g.CompartmentID
	case fieldOracleLogGroup:
		return l.LogGroupID
	case fieldOracleLog:
		return l.ID
	case fieldOracleIngested:
		return e.IngestedTime.UTC().Format(timeFormat)
	default:
		return ""
	}
}

// jsonField reads a top-level key out of a JSON entry payload. A payload that
// is not a JSON object, or that lacks the key, resolves to the empty string —
// the field is absent from that record rather than unmodelled.
func jsonField(data, key string) string {
	var payload map[string]any

	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return ""
	}

	v, ok := payload[key]
	if !ok {
		return ""
	}

	if s, isString := v.(string); isString {
		return s
	}

	return fmt.Sprint(v)
}

// globMatch reports whether value matches a pattern whose * stands for any run
// of characters. OCI's search wildcard is *, and a pattern without one is an
// exact comparison.
func globMatch(pattern, value string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == value
	}

	rest := value
	if !strings.HasPrefix(rest, parts[0]) {
		return false
	}

	rest = rest[len(parts[0]):]

	for _, part := range parts[1 : len(parts)-1] {
		idx := strings.Index(rest, part)
		if idx < 0 {
			return false
		}

		rest = rest[idx+len(part):]
	}

	last := parts[len(parts)-1]

	return strings.HasSuffix(rest, last) && len(rest) >= len(last)
}
