package cloudwatchlogs

import (
	"errors"
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// Logs Insights paging limits.
const (
	maxQueryResultsPage   = 10000
	maxDescribeQueries    = 1000
	defaultDescribeLength = 1000
)

type startQueryRequest struct {
	LogGroupName        string   `json:"logGroupName"`
	LogGroupNames       []string `json:"logGroupNames"`
	LogGroupIdentifiers []string `json:"logGroupIdentifiers"`
	StartTime           int64    `json:"startTime"`
	EndTime             int64    `json:"endTime"`
	QueryString         string   `json:"queryString"`
	QueryLanguage       string   `json:"queryLanguage"`
	Limit               int      `json:"limit"`
}

// startQuery starts a Logs Insights query (Logs_20140328.StartQuery). The
// time range is in epoch seconds.
func (h *Handler) startQuery(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "StartQuery")
	if !ok {
		return
	}

	var req startQueryRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	id, err := ext.StartQuery(r.Context(), &logdriver.StartQueryInput{
		LogGroupName: req.LogGroupName, LogGroupNames: req.LogGroupNames, LogGroupIdentifiers: req.LogGroupIdentifiers,
		StartTime: time.Unix(req.StartTime, 0).UTC(), EndTime: time.Unix(req.EndTime, 0).UTC(),
		QueryString: req.QueryString, QueryLanguage: req.QueryLanguage, Limit: req.Limit,
	})
	if err != nil {
		writeQueryErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]string{"queryId": id})
}

// writeQueryErr reports a query that does not compile as
// MalformedQueryException with its queryCompileError location, and anything
// else as writeErr does.
func writeQueryErr(w http.ResponseWriter, err error) {
	var mq *logdriver.MalformedQueryError
	if !errors.As(err, &mq) {
		writeErr(w, err)
		return
	}

	wire.WriteJSONErrorFields(w, http.StatusBadRequest, "MalformedQueryException", mq.Message, map[string]any{
		"queryCompileError": map[string]any{
			"location": map[string]int{"startCharOffset": mq.Start, "endCharOffset": mq.End},
			"message":  mq.Message,
		},
	})
}

type resultFieldJSON struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

type queryStatisticsJSON struct {
	RecordsMatched   float64 `json:"recordsMatched"`
	RecordsScanned   float64 `json:"recordsScanned"`
	BytesScanned     float64 `json:"bytesScanned"`
	LogGroupsScanned float64 `json:"logGroupsScanned"`
}

type getQueryResultsResponse struct {
	Results       [][]resultFieldJSON `json:"results"`
	Statistics    queryStatisticsJSON `json:"statistics"`
	Status        string              `json:"status"`
	QueryLanguage string              `json:"queryLanguage,omitempty"`
	EncryptionKey string              `json:"encryptionKey,omitempty"`
	NextToken     string              `json:"nextToken,omitempty"`
}

func (h *Handler) getQueryResults(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "GetQueryResults")
	if !ok {
		return
	}

	var req struct {
		QueryID   string `json:"queryId"`
		MaxItems  int32  `json:"maxItems"`
		NextToken string `json:"nextToken"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if req.MaxItems < 0 || req.MaxItems > maxQueryResultsPage {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException",
			"1 validation error detected: Value at 'maxItems' failed to satisfy constraint: "+
				"Member must have value between 0 and 10000")

		return
	}

	res, err := ext.GetQueryResults(r.Context(), req.QueryID)
	if err != nil {
		writeErr(w, err)
		return
	}

	limit := req.MaxItems
	if limit == 0 {
		limit = maxQueryResultsPage
	}

	from, to, next := pageBounds(len(res.Results), decodeOffsetToken(req.NextToken), limit)

	resp := getQueryResultsResponse{
		Results: make([][]resultFieldJSON, 0, to-from), Status: res.Status,
		QueryLanguage: res.QueryLanguage, EncryptionKey: res.EncryptionKey,
		Statistics: queryStatisticsJSON{
			RecordsMatched: res.Statistics.RecordsMatched, RecordsScanned: res.Statistics.RecordsScanned,
			BytesScanned: res.Statistics.BytesScanned, LogGroupsScanned: res.Statistics.LogGroupsScanned,
		},
	}

	for _, row := range res.Results[from:to] {
		out := make([]resultFieldJSON, 0, len(row))
		for _, f := range row {
			out = append(out, resultFieldJSON{Field: f.Field, Value: f.Value})
		}

		resp.Results = append(resp.Results, out)
	}

	if next > 0 {
		resp.NextToken = encodeOffsetToken(next)
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) stopQuery(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "StopQuery")
	if !ok {
		return
	}

	var req struct {
		QueryID string `json:"queryId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := ext.StopQuery(r.Context(), req.QueryID); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]bool{"success": true})
}

type queryInfoJSON struct {
	QueryID       string  `json:"queryId"`
	QueryString   string  `json:"queryString"`
	QueryLanguage string  `json:"queryLanguage,omitempty"`
	Status        string  `json:"status"`
	CreateTime    int64   `json:"createTime"`
	LogGroupName  string  `json:"logGroupName,omitempty"`
	QueryDuration int64   `json:"queryDuration,omitempty"`
	BytesScanned  float64 `json:"bytesScanned"`
}

func (h *Handler) describeQueries(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "DescribeQueries")
	if !ok {
		return
	}

	var req struct {
		LogGroupName  string `json:"logGroupName"`
		Status        string `json:"status"`
		QueryLanguage string `json:"queryLanguage"`
		MaxResults    int32  `json:"maxResults"`
		NextToken     string `json:"nextToken"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if req.MaxResults < 0 || req.MaxResults > maxDescribeQueries {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException",
			"1 validation error detected: Value at 'maxResults' failed to satisfy constraint: "+
				"Member must have value between 1 and 1000")

		return
	}

	infos, err := ext.DescribeQueries(r.Context(), logdriver.DescribeQueriesInput{
		LogGroupName: req.LogGroupName, Status: req.Status, QueryLanguage: req.QueryLanguage,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	limit := req.MaxResults
	if limit == 0 {
		limit = defaultDescribeLength
	}

	from, to, next := pageBounds(len(infos), decodeOffsetToken(req.NextToken), limit)

	out := make([]queryInfoJSON, 0, to-from)
	for _, q := range infos[from:to] {
		out = append(out, queryInfoJSON{
			QueryID: q.QueryID, QueryString: q.QueryString, QueryLanguage: q.QueryLanguage, Status: q.Status,
			CreateTime: epochMillis(q.CreateTime), LogGroupName: q.LogGroupName,
			QueryDuration: q.QueryDuration, BytesScanned: q.BytesScanned,
		})
	}

	resp := map[string]any{"queries": out}
	if next > 0 {
		resp["nextToken"] = encodeOffsetToken(next)
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) getLogRecord(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "GetLogRecord")
	if !ok {
		return
	}

	var req struct {
		LogRecordPointer string `json:"logRecordPointer"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	rec, err := ext.GetLogRecord(r.Context(), req.LogRecordPointer)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"logRecord": rec})
}

type logGroupFieldJSON struct {
	Name    string `json:"name"`
	Percent int    `json:"percent"`
}

// getLogGroupFields lists a log group's fields (Logs_20140328.GetLogGroupFields).
// Exactly one of logGroupName or logGroupIdentifier names the group; time is
// in epoch seconds.
func (h *Handler) getLogGroupFields(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "GetLogGroupFields")
	if !ok {
		return
	}

	var req struct {
		LogGroupName       string `json:"logGroupName"`
		LogGroupIdentifier string `json:"logGroupIdentifier"`
		Time               int64  `json:"time"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if (req.LogGroupName == "") == (req.LogGroupIdentifier == "") {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException",
			"exactly one of logGroupName or logGroupIdentifier must be specified")

		return
	}

	var at time.Time
	if req.Time > 0 {
		at = time.Unix(req.Time, 0).UTC()
	}

	fields, err := ext.GetLogGroupFields(r.Context(), logGroupName(req.LogGroupIdentifier, req.LogGroupName), at)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]logGroupFieldJSON, 0, len(fields))
	for _, f := range fields {
		out = append(out, logGroupFieldJSON{Name: f.Name, Percent: f.Percent})
	}

	wire.WriteJSON(w, map[string]any{"logGroupFields": out})
}
