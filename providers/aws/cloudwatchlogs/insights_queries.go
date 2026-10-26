package cloudwatchlogs

import (
	"context"
	"encoding/base64"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs/insights"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// Logs Insights limits, per the StartQuery API reference.
const (
	// insightsQueryTimeout is the StartQuery doc's "Queries time out after
	// 60 minutes of runtime".
	insightsQueryTimeout = 60 * time.Minute
	// maxConcurrentInsightsQueries: up to 100 concurrent queries.
	maxConcurrentInsightsQueries = 100
	maxQueryLogGroups            = 50
	maxQueryStringLength         = 10000
	maxQueryLimit                = 100000
	// defaultQueryLimit is the row cap when StartQuery has no limit, the most
	// one GetQueryResults page returns.
	defaultQueryLimit = 10000
	// queryLanguageCWLI is the Logs Insights query language, the only one
	// the emulator runs.
	queryLanguageCWLI = "CWLI"
	// fieldDiscoveryWindow is how far either side of GetLogGroupFields' time
	// it samples; fieldDiscoveryRecent is the window when no time is given.
	fieldDiscoveryWindow = 8 * time.Minute
	fieldDiscoveryRecent = 15 * time.Minute
)

// queryRecord is one Logs Insights query. The request is kept so a Running
// query can be evaluated on its first poll, including after a restore.
type queryRecord struct {
	ID            string                      `json:"id"`
	QueryString   string                      `json:"queryString"`
	QueryLanguage string                      `json:"queryLanguage"`
	LogGroups     []string                    `json:"logGroups"`
	Start         time.Time                   `json:"start"`
	End           time.Time                   `json:"end"`
	Limit         int                         `json:"limit"`
	Status        string                      `json:"status"`
	CreatedAt     time.Time                   `json:"createdAt"`
	DurationMS    int64                       `json:"durationMs,omitempty"`
	Results       [][]driver.QueryResultField `json:"results,omitempty"`
	Stats         driver.QueryStatistics      `json:"stats"`
}

// StartQuery validates and records a Logs Insights query as Running. The
// query runs on its first GetQueryResults.
func (m *Mock) StartQuery(_ context.Context, in *driver.StartQueryInput) (string, error) {
	groups, err := m.queryLogGroups(in)
	if err != nil {
		return "", err
	}

	if err := validateQueryRequest(in); err != nil {
		return "", err
	}

	if _, err := insights.Parse(in.QueryString); err != nil {
		return "", malformed(err)
	}

	rec := queryRecord{
		ID: idgen.UUID(), QueryString: in.QueryString, QueryLanguage: queryLanguageCWLI,
		LogGroups: groups, Start: in.StartTime, End: in.EndTime, Limit: in.Limit,
		Status: driver.QueryStatusRunning, CreatedAt: m.opts.Clock.Now(),
	}

	m.admitMu.Lock()
	defer m.admitMu.Unlock()

	if m.runningQueries() >= maxConcurrentInsightsQueries {
		return "", cerrors.Newf(cerrors.ResourceExhausted,
			"the account already has the maximum of %d concurrent queries", maxConcurrentInsightsQueries)
	}

	m.queries.Set(rec.ID, rec)

	return rec.ID, nil
}

func malformed(err error) error {
	var ce *insights.CompileError
	if errors.As(err, &ce) {
		return &driver.MalformedQueryError{Message: ce.Message, Start: ce.Start, End: ce.End}
	}

	return err
}

// queryLogGroups resolves the one log group selector StartQuery allows to
// existing log group names.
func (m *Mock) queryLogGroups(in *driver.StartQueryInput) ([]string, error) {
	set := 0

	for _, has := range []bool{in.LogGroupName != "", len(in.LogGroupNames) > 0, len(in.LogGroupIdentifiers) > 0} {
		if has {
			set++
		}
	}

	if set != 1 {
		return nil, cerrors.New(cerrors.InvalidArgument,
			"exactly one of logGroupName, logGroupNames or logGroupIdentifiers must be specified")
	}

	names := in.LogGroupNames
	if in.LogGroupName != "" {
		names = []string{in.LogGroupName}
	}

	for _, id := range in.LogGroupIdentifiers {
		names = append(names, logGroupFromIdentifier(id))
	}

	if len(names) > maxQueryLogGroups {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "a query can include at most %d log groups", maxQueryLogGroups)
	}

	for _, n := range names {
		if !m.groups.Has(n) {
			return nil, cerrors.Newf(cerrors.NotFound, "log group %q not found", n)
		}
	}

	return names, nil
}

// logGroupFromIdentifier accepts a log group name or ARN.
func logGroupFromIdentifier(id string) string {
	if i := strings.LastIndex(id, logGroupARNMarker); i >= 0 {
		return strings.TrimSuffix(id[i+len(logGroupARNMarker):], ":*")
	}

	return id
}

func validateQueryRequest(in *driver.StartQueryInput) error {
	switch {
	case len(in.QueryString) > maxQueryStringLength:
		return cerrors.Newf(cerrors.InvalidArgument, "queryString must be at most %d characters", maxQueryStringLength)
	case in.QueryLanguage != "" && in.QueryLanguage != queryLanguageCWLI:
		return cerrors.Newf(cerrors.InvalidArgument, "query language %s is not supported", in.QueryLanguage)
	case in.EndTime.Before(in.StartTime):
		return cerrors.New(cerrors.InvalidArgument, "endTime must not be before startTime")
	case in.Limit < 0 || in.Limit > maxQueryLimit:
		return cerrors.Newf(cerrors.InvalidArgument, "limit must be between 1 and %d", maxQueryLimit)
	}

	return nil
}

func (m *Mock) runningQueries() int {
	n := 0

	all := m.queries.All()
	for id := range all {
		if all[id].Status == driver.QueryStatusRunning {
			n++
		}
	}

	return n
}

// GetQueryResults returns a query's results. The first poll of a Running
// query runs it and settles it to Complete, or to Timeout when it was
// started more than an hour ago.
func (m *Mock) GetQueryResults(_ context.Context, queryID string) (*driver.QueryResults, error) {
	q, ok := m.queries.Get(queryID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "query %s not found", queryID)
	}

	if q.Status == driver.QueryStatusRunning {
		q = m.settleQuery(&q)
	}

	m.mu.RLock()
	key := m.queryResultKmsKey
	m.mu.RUnlock()

	return &driver.QueryResults{
		Status: q.Status, QueryLanguage: q.QueryLanguage, EncryptionKey: key,
		Results: q.Results, Statistics: q.Stats,
	}, nil
}

// settleQuery runs a Running query and records the outcome. The query runs
// outside the queries store lock; if a concurrent poll or StopQuery settled
// it first, that outcome stands.
func (m *Mock) settleQuery(q *queryRecord) queryRecord {
	now := m.opts.Clock.Now()
	timedOut := now.Sub(q.CreatedAt) > insightsQueryTimeout

	var (
		results [][]driver.QueryResultField
		stats   driver.QueryStatistics
	)

	if !timedOut {
		results, stats = m.runQuery(q)
	}

	m.queries.Update(q.ID, func(cur queryRecord) queryRecord {
		if cur.Status != driver.QueryStatusRunning {
			return cur
		}

		if timedOut {
			cur.Status = driver.QueryStatusTimeout
			return cur
		}

		cur.Results, cur.Stats = results, stats
		cur.Status = driver.QueryStatusComplete
		cur.DurationMS = now.Sub(cur.CreatedAt).Milliseconds()

		return cur
	})

	if cur, ok := m.queries.Get(q.ID); ok {
		return cur
	}

	return *q
}

// runQuery evaluates a recorded query over its log groups' events in the
// query's time range. The range is in whole seconds and inclusive.
func (m *Mock) runQuery(q *queryRecord) ([][]driver.QueryResultField, driver.QueryStatistics) {
	parsed, err := insights.Parse(q.QueryString)
	if err != nil {
		return nil, driver.QueryStatistics{}
	}

	recs, bytes := m.queryRecords(q.LogGroups, q.Start, q.End.Add(time.Second))

	limit := q.Limit
	if limit == 0 {
		limit = defaultQueryLimit
	}

	scanned := len(recs)
	res := parsed.Run(recs, limit)

	rows := make([][]driver.QueryResultField, 0, len(res.Rows))

	for _, row := range res.Rows {
		out := make([]driver.QueryResultField, 0, len(row))
		for _, f := range row {
			out = append(out, driver.QueryResultField{Field: f.Name, Value: f.Value})
		}

		rows = append(rows, out)
	}

	return rows, driver.QueryStatistics{
		RecordsMatched: float64(res.Matched), RecordsScanned: float64(scanned),
		BytesScanned: float64(bytes), LogGroupsScanned: float64(len(q.LogGroups)),
	}
}

// queryRecords collects the events with a timestamp in [from, to) from the
// named log groups as query records, with their scanned byte count. Expired
// events are purged first, so queries see what every other read API sees.
func (m *Mock) queryRecords(groups []string, from, to time.Time) (recs []insights.Record, bytes int64) {
	for _, name := range groups {
		g, ok := m.groups.Get(name)
		if !ok {
			continue
		}

		m.purgeExpired(name, g)

		for _, s := range g.streams.SortedValues() {
			r, b := m.streamRecords(name, s, from, to)
			recs = append(recs, r...)
			bytes += b
		}
	}

	return recs, bytes
}

func (m *Mock) streamRecords(group string, s *logStream, from, to time.Time) (recs []insights.Record, bytes int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := map[eventTimes]int{}

	for i := range s.events {
		e := &s.events[i]
		key := eventTimes{e.Timestamp.UnixMilli(), e.IngestionTime.UnixNano()}

		// Events are sorted by timestamp, so equal timestamps are
		// contiguous and the ordinal map only ever holds one timestamp.
		if i > 0 && !s.events[i-1].Timestamp.Equal(e.Timestamp) {
			clear(seen)
		}

		ordinal := seen[key]
		seen[key]++

		if e.Timestamp.Before(from) || !e.Timestamp.Before(to) {
			continue
		}

		bytes += int64(len(e.Message))
		recs = append(recs, insights.NewRecord(&insights.Event{
			Account: m.opts.AccountID, LogGroup: group, LogStream: s.info.Name,
			Timestamp: e.Timestamp, Ingestion: e.IngestionTime, Message: e.Message,
			Ptr: encodePtr(group, s.info.Name, key, ordinal),
		}))
	}

	return recs, bytes
}

// eventTimes is an event's timestamp (milliseconds) and ingestion time
// (nanoseconds), the identity a log record pointer carries.
type eventTimes struct{ ts, ingest int64 }

// ptrSep separates the parts of a log record pointer.
const ptrSep = "\x00"

// encodePtr identifies an event by its group, stream, times and its position
// among the stream's events sharing both times. Unlike a slice index, that
// survives the retention purge removing older events.
func encodePtr(group, stream string, t eventTimes, ordinal int) string {
	raw := strings.Join([]string{
		group, stream,
		strconv.FormatInt(t.ts, 10),
		strconv.FormatInt(t.ingest, 10),
		strconv.Itoa(ordinal),
	}, ptrSep)

	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// ptrParts is the number of fields encodePtr joins.
const ptrParts = 5

// GetLogRecord returns every field of the log event a query result's @ptr
// points to. Times are epoch milliseconds, the unit of the event's timestamp
// and ingestionTime. The API reference's sample shows a 10-digit @timestamp,
// but that sample is hand-written (pointer "123456789", invalid JSON); the
// SDKs carry no fixture and LocalStack does not implement GetLogRecord.
func (m *Mock) GetLogRecord(_ context.Context, ptr string, unmask bool) (map[string]string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(ptr)
	parts := strings.Split(string(raw), ptrSep)

	if err != nil || len(parts) != ptrParts {
		return nil, cerrors.New(cerrors.InvalidArgument, "logRecordPointer is not valid")
	}

	tsMillis, err1 := strconv.ParseInt(parts[2], 10, 64)
	ingestNanos, err2 := strconv.ParseInt(parts[3], 10, 64)
	ordinal, err3 := strconv.Atoi(parts[4])

	if err1 != nil || err2 != nil || err3 != nil {
		return nil, cerrors.New(cerrors.InvalidArgument, "logRecordPointer is not valid")
	}

	e, ok := m.findEvent(parts[0], parts[1], tsMillis, ingestNanos, ordinal)
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, "the log record does not exist")
	}

	rec := insights.NewRecord(&insights.Event{
		Account: m.opts.AccountID, LogGroup: parts[0], LogStream: parts[1],
		Timestamp: e.Timestamp, Ingestion: e.IngestionTime, Message: eventView(&e, unmask).Message,
	}).Strings()

	delete(rec, insights.FieldPtr)
	rec[insights.FieldTimestamp] = strconv.FormatInt(e.Timestamp.UnixMilli(), 10)
	rec[insights.FieldIngestionTime] = strconv.FormatInt(e.IngestionTime.UnixMilli(), 10)

	return rec, nil
}

func (m *Mock) findEvent(group, stream string, tsMillis, ingestNanos int64, ordinal int) (ev driver.LogEvent, found bool) {
	g, ok := m.groups.Get(group)
	if !ok {
		return driver.LogEvent{}, false
	}

	m.purgeExpired(group, g)

	s, ok := g.streams.Get(stream)
	if !ok {
		return driver.LogEvent{}, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := 0

	for _, e := range s.events {
		if e.Timestamp.UnixMilli() != tsMillis || e.IngestionTime.UnixNano() != ingestNanos {
			continue
		}

		if seen == ordinal {
			return e, true
		}

		seen++
	}

	return driver.LogEvent{}, false
}

// StopQuery cancels a query that is still Running.
func (m *Mock) StopQuery(_ context.Context, queryID string) error {
	if !m.queries.Has(queryID) {
		return cerrors.Newf(cerrors.NotFound, "query %s not found", queryID)
	}

	stopped := false

	m.queries.Update(queryID, func(q queryRecord) queryRecord {
		if q.Status == driver.QueryStatusRunning {
			q.Status = driver.QueryStatusCancelled
			stopped = true
		}

		return q
	})

	if !stopped {
		return cerrors.Newf(cerrors.InvalidArgument, "query %s is not running", queryID)
	}

	return nil
}

// DescribeQueries lists queries, newest first.
func (m *Mock) DescribeQueries(_ context.Context, in driver.DescribeQueriesInput) ([]driver.QueryInfo, error) {
	var out []driver.QueryInfo

	all := m.queries.All()
	for id := range all {
		q := all[id]
		if !queryMatches(&q, in) {
			continue
		}

		info := driver.QueryInfo{
			QueryID: q.ID, QueryString: q.QueryString, QueryLanguage: q.QueryLanguage, Status: q.Status,
			CreateTime: q.CreatedAt, QueryDuration: q.DurationMS, BytesScanned: q.Stats.BytesScanned,
		}

		if len(q.LogGroups) > 0 {
			info.LogGroupName = q.LogGroups[0]
		}

		out = append(out, info)
	}

	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreateTime.Equal(out[j].CreateTime) {
			return out[i].CreateTime.After(out[j].CreateTime)
		}

		return out[i].QueryID < out[j].QueryID
	})

	return out, nil
}

func queryMatches(q *queryRecord, in driver.DescribeQueriesInput) bool {
	if in.Status != "" && q.Status != in.Status {
		return false
	}

	if in.QueryLanguage != "" && q.QueryLanguage != in.QueryLanguage {
		return false
	}

	if in.LogGroupName == "" {
		return true
	}

	for _, g := range q.LogGroups {
		if g == in.LogGroupName {
			return true
		}
	}

	return false
}

// GetLogGroupFields reports the fields in a log group's events, with the
// share of events carrying each, over the 8 minutes either side of at, or
// the last 15 minutes when at is zero.
func (m *Mock) GetLogGroupFields(_ context.Context, logGroup string, at time.Time) ([]driver.LogGroupField, error) {
	if !m.groups.Has(logGroup) {
		return nil, cerrors.Newf(cerrors.NotFound, "log group %q not found", logGroup)
	}

	from, to := at.Add(-fieldDiscoveryWindow), at.Add(fieldDiscoveryWindow)

	if at.IsZero() {
		now := m.opts.Clock.Now()
		from, to = now.Add(-fieldDiscoveryRecent), now.Add(time.Nanosecond)
	}

	recs, _ := m.queryRecords([]string{logGroup}, from, to)

	out := make([]driver.LogGroupField, 0)
	if len(recs) == 0 {
		return out, nil
	}

	for _, s := range insights.FieldShares(recs) {
		out = append(out, driver.LogGroupField{Name: s.Name, Percent: s.Percent})
	}

	return out, nil
}
