package cloudwatchlogs

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// defaultDescribeLimit is the page size CloudWatch Logs applies to
// DescribeLogGroups / DescribeLogStreams when the caller omits limit.
const defaultDescribeLimit = 50

// defaultEventLimit is the page size CloudWatch Logs applies to GetLogEvents /
// FilterLogEvents when the caller omits limit (AWS caps a page at 10000 events).
const defaultEventLimit = 10000

// forwardTokenPrefix / backwardTokenPrefix mark the GetLogEvents cursor
// direction; AWS forward tokens are "f/<...>" and backward tokens "b/<...>".
const (
	forwardTokenPrefix  = "f/"
	backwardTokenPrefix = "b/"
)

// encodePositionToken renders a slice offset as a direction-prefixed GetLogEvents
// token (e.g. "f/<base64>").
func encodePositionToken(prefix string, offset int) string {
	return prefix + encodeOffsetToken(offset)
}

// decodePositionToken parses a GetLogEvents forward/backward token back into a
// slice offset, tolerating either direction prefix.
func decodePositionToken(tok string) int {
	tok = strings.TrimPrefix(tok, forwardTokenPrefix)
	tok = strings.TrimPrefix(tok, backwardTokenPrefix)

	return decodeOffsetToken(tok)
}

// decodeOffsetToken parses a nextToken produced by encodeOffsetToken back into a
// slice offset. An empty token means "start from the beginning"; a malformed
// token is treated as offset 0 so a stray token never wedges pagination.
func decodeOffsetToken(tok string) int {
	if tok == "" {
		return 0
	}

	raw, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		return 0
	}

	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// encodeOffsetToken renders a slice offset as an opaque nextToken.
func encodeOffsetToken(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// pageBounds resolves [start,end) and the next-page offset for a slice of the
// given length, honoring a caller limit (falling back to defaultDescribeLimit).
// next is 0 when no further page remains.
func pageBounds(total, start int, limit int32) (from, to, next int) {
	size := int(limit)
	if size <= 0 {
		size = defaultDescribeLimit
	}

	if start > total {
		start = total
	}

	end := start + size
	if end >= total {
		return start, total, 0
	}

	return start, end, end
}

// --- log groups ---

func (h *Handler) createLogGroup(w http.ResponseWriter, r *http.Request) {
	var req createLogGroupRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if _, err := h.logs.CreateLogGroup(r.Context(), logdriver.LogGroupConfig{
		Name: req.LogGroupName,
		Tags: req.Tags,
	}); err != nil {
		writeErr(w, err)
		return
	}

	// CreateLogGroup has an empty response body.
	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) describeLogGroups(w http.ResponseWriter, r *http.Request) {
	var req describeLogGroupsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	infos, err := h.logs.ListLogGroups(r.Context(), scope.Scope{})
	if err != nil {
		writeErr(w, err)
		return
	}

	matched := make([]logdriver.LogGroupInfo, 0, len(infos))

	for i := range infos {
		if req.LogGroupNamePrefix != "" && !strings.HasPrefix(infos[i].Name, req.LogGroupNamePrefix) {
			continue
		}

		matched = append(matched, infos[i])
	}

	from, to, next := pageBounds(len(matched), decodeOffsetToken(req.NextToken), req.Limit)

	out := make([]logGroupJSON, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, toLogGroupJSON(&matched[i]))
	}

	resp := describeLogGroupsResponse{LogGroups: out}
	if next > 0 {
		resp.NextToken = encodeOffsetToken(next)
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) deleteLogGroup(w http.ResponseWriter, r *http.Request) {
	var req deleteLogGroupRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.logs.DeleteLogGroup(r.Context(), req.LogGroupName); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

// --- log streams ---

func (h *Handler) createLogStream(w http.ResponseWriter, r *http.Request) {
	var req createLogStreamRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if _, err := h.logs.CreateLogStream(r.Context(), req.LogGroupName, req.LogStreamName); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

func (h *Handler) describeLogStreams(w http.ResponseWriter, r *http.Request) {
	var req describeLogStreamsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	infos, err := h.logs.ListLogStreams(r.Context(), req.LogGroupName)
	if err != nil {
		writeErr(w, err)
		return
	}

	// logStreamNamePrefix filters to streams whose name starts with the prefix.
	// ListLogStreams already returns streams ordered by name (the default
	// orderBy=LogStreamName), so filtering the slice keeps that order.
	if req.LogStreamNamePrefix != "" {
		filtered := make([]logdriver.LogStreamInfo, 0, len(infos))

		for i := range infos {
			if strings.HasPrefix(infos[i].Name, req.LogStreamNamePrefix) {
				filtered = append(filtered, infos[i])
			}
		}

		infos = filtered
	}

	// The stream ARN is derived from the owning group's ARN, which the driver
	// carries on the group, not the stream.
	groupARN := ""
	if g, gerr := h.logs.GetLogGroup(r.Context(), req.LogGroupName); gerr == nil {
		groupARN = g.ResourceID
	}

	from, to, next := pageBounds(len(infos), decodeOffsetToken(req.NextToken), req.Limit)

	out := make([]logStreamJSON, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, toLogStreamJSON(&infos[i], groupARN))
	}

	resp := describeLogStreamsResponse{LogStreams: out}
	if next > 0 {
		resp.NextToken = encodeOffsetToken(next)
	}

	wire.WriteJSON(w, resp)
}

func (h *Handler) deleteLogStream(w http.ResponseWriter, r *http.Request) {
	var req deleteLogStreamRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := h.logs.DeleteLogStream(r.Context(), req.LogGroupName, req.LogStreamName); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}

// --- log events ---

func (h *Handler) putLogEvents(w http.ResponseWriter, r *http.Request) {
	var req putLogEventsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	events := make([]logdriver.LogEvent, 0, len(req.LogEvents))
	for _, e := range req.LogEvents {
		events = append(events, logdriver.LogEvent{
			Timestamp: millisToTime(e.Timestamp),
			Message:   e.Message,
		})
	}

	if err := h.logs.PutLogEvents(r.Context(), req.LogGroupName, req.LogStreamName, events); err != nil {
		writeErr(w, err)
		return
	}

	// The sequence token is deprecated and ignored by the modern SDK, but a
	// non-empty value keeps older callers happy.
	wire.WriteJSON(w, putLogEventsResponse{NextSequenceToken: "0"})
}

// getLogEventsWindow picks the [start,end) slice bounds for a GetLogEvents page
// over an ascending (oldest→newest) event set. A continuation token wins and is
// direction-aware: a forward token marks the START of the next page, a backward
// token marks the END (exclusive) of the previous page — so following the
// backward token yields the older window, not the current one. With no token the
// AWS default (startFromHead false) returns the tail (latest events first) and
// startFromHead=true returns the head.
func getLogEventsWindow(nextToken string, startFromHead *bool, total, size int) (start, end int) {
	switch {
	case strings.HasPrefix(nextToken, backwardTokenPrefix):
		end = decodePositionToken(nextToken)
		start = end - size
	case nextToken != "":
		start = decodePositionToken(nextToken)
		end = start + size
	case startFromHead != nil && *startFromHead:
		start, end = 0, size
	default:
		start, end = total-size, total
	}

	if start < 0 {
		start = 0
	}

	if start > total {
		start = total
	}

	if end > total {
		end = total
	}

	if end < start {
		end = start
	}

	return start, end
}

func (h *Handler) getLogEvents(w http.ResponseWriter, r *http.Request) {
	var req getLogEventsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	// Fetch the full ordered slice (Limit -1 = no cap) and page it here so the
	// forward / backward tokens can carry a real position across all events.
	events, err := h.logs.GetLogEvents(r.Context(), &logdriver.LogQueryInput{
		LogGroup:  req.LogGroupName,
		LogStream: req.LogStreamName,
		StartTime: millisToTime(req.StartTime),
		EndTime:   millisToTime(req.EndTime),
		Limit:     -1,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	size := int(req.Limit)
	if size <= 0 {
		size = defaultEventLimit
	}

	start, end := getLogEventsWindow(req.NextToken, req.StartFromHead, len(events), size)

	out := make([]outputLogEvent, 0, end-start)
	for _, e := range events[start:end] {
		out = append(out, outputLogEvent{
			Timestamp:     epochMillis(e.Timestamp),
			Message:       e.Message,
			IngestionTime: ingestionMillis(e.IngestionTime, e.Timestamp),
		})
	}

	// Tokens are never null. When the cursor is at the end, the forward token
	// equals the one the caller passed in, which is how the SDK paginator stops.
	wire.WriteJSON(w, getLogEventsResponse{
		Events:            out,
		NextForwardToken:  encodePositionToken(forwardTokenPrefix, end),
		NextBackwardToken: encodePositionToken(backwardTokenPrefix, start),
	})
}

func (h *Handler) filterLogEvents(w http.ResponseWriter, r *http.Request) {
	var req filterLogEventsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	// The driver filters one stream at a time; when the caller scopes the query
	// to a single stream, honor it, otherwise search across all streams.
	stream := ""
	if len(req.LogStreamNames) == 1 {
		stream = req.LogStreamNames[0]
	}

	// Fetch every match (Limit -1 = no cap) and page here so a NextToken can be
	// handed back across all matches.
	events, err := h.logs.FilterLogEvents(r.Context(), &logdriver.FilterLogEventsInput{
		LogGroup:      req.LogGroupName,
		LogStream:     stream,
		FilterPattern: req.FilterPattern,
		StartTime:     millisToTime(req.StartTime),
		EndTime:       millisToTime(req.EndTime),
		Limit:         -1,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultEventLimit
	}

	from, to, next := pageBounds(len(events), decodeOffsetToken(req.NextToken), limit)

	out := make([]filteredLogEvent, 0, to-from)
	for _, e := range events[from:to] {
		out = append(out, filteredLogEvent{
			LogStreamName: e.LogStream,
			Timestamp:     epochMillis(e.Timestamp),
			Message:       e.Message,
			IngestionTime: ingestionMillis(e.IngestionTime, e.Timestamp),
		})
	}

	// searchedLogStreams is an always-empty list on modern AWS (deprecated 2020).
	resp := filterLogEventsResponse{Events: out, SearchedLogStreams: []searchedLogStreamJSON{}}
	if next > 0 {
		resp.NextToken = encodeOffsetToken(next)
	}

	wire.WriteJSON(w, resp)
}
