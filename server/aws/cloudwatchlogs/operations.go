package cloudwatchlogs

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

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

	infos, err := h.logs.ListLogGroups(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]logGroupJSON, 0, len(infos))

	for i := range infos {
		if req.LogGroupNamePrefix != "" && !strings.HasPrefix(infos[i].Name, req.LogGroupNamePrefix) {
			continue
		}

		out = append(out, toLogGroupJSON(&infos[i]))
	}

	wire.WriteJSON(w, describeLogGroupsResponse{LogGroups: out})
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

	out := make([]logStreamJSON, 0, len(infos))
	for i := range infos {
		out = append(out, toLogStreamJSON(&infos[i]))
	}

	wire.WriteJSON(w, describeLogStreamsResponse{LogStreams: out})
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
			IngestionTime: epochMillis(e.Timestamp),
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
			IngestionTime: epochMillis(e.Timestamp),
		})
	}

	wire.WriteJSON(w, filterLogEventsResponse{Events: out})
}
