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

func (h *Handler) getLogEvents(w http.ResponseWriter, r *http.Request) {
	var req getLogEventsRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	events, err := h.logs.GetLogEvents(r.Context(), &logdriver.LogQueryInput{
		LogGroup:  req.LogGroupName,
		LogStream: req.LogStreamName,
		StartTime: millisToTime(req.StartTime),
		EndTime:   millisToTime(req.EndTime),
		Limit:     int(req.Limit),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]outputLogEvent, 0, len(events))
	for _, e := range events {
		out = append(out, outputLogEvent{
			Timestamp:     epochMillis(e.Timestamp),
			Message:       e.Message,
			IngestionTime: ingestionMillis(e.IngestionTime, e.Timestamp),
		})
	}

	// Token values are opaque to the SDK; a stable pair terminates paging.
	wire.WriteJSON(w, getLogEventsResponse{
		Events:            out,
		NextForwardToken:  "f/0",
		NextBackwardToken: "b/0",
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

	events, err := h.logs.FilterLogEvents(r.Context(), &logdriver.FilterLogEventsInput{
		LogGroup:      req.LogGroupName,
		LogStream:     stream,
		FilterPattern: req.FilterPattern,
		StartTime:     millisToTime(req.StartTime),
		EndTime:       millisToTime(req.EndTime),
		Limit:         int(req.Limit),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]filteredLogEvent, 0, len(events))
	for _, e := range events {
		out = append(out, filteredLogEvent{
			LogStreamName: e.LogStream,
			Timestamp:     epochMillis(e.Timestamp),
			Message:       e.Message,
			IngestionTime: ingestionMillis(e.IngestionTime, e.Timestamp),
		})
	}

	wire.WriteJSON(w, filterLogEventsResponse{Events: out})
}
