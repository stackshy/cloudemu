package cloudwatchlogs

import (
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// maxDescribeExportTasks is the DescribeExportTasks limit maximum and default.
const maxDescribeExportTasks = 50

// dispatchExport handles the export task operations, delegating tagging to
// dispatchTags.
func (h *Handler) dispatchExport(w http.ResponseWriter, r *http.Request, op string) {
	switch op {
	case "CreateExportTask":
		h.createExportTask(w, r)
	case "DescribeExportTasks":
		h.describeExportTasks(w, r)
	case "CancelExportTask":
		h.cancelExportTask(w, r)
	default:
		h.dispatchTags(w, r, op)
	}
}

type createExportTaskRequest struct {
	TaskName            string `json:"taskName"`
	LogGroupName        string `json:"logGroupName"`
	LogStreamNamePrefix string `json:"logStreamNamePrefix"`
	From                *int64 `json:"from"`
	To                  *int64 `json:"to"`
	Destination         string `json:"destination"`
	DestinationPrefix   string `json:"destinationPrefix"`
}

func (h *Handler) createExportTask(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "CreateExportTask")
	if !ok {
		return
	}

	var req createExportTaskRequest
	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	for _, f := range []struct {
		name string
		v    *int64
	}{{"from", req.From}, {"to", req.To}} {
		if f.v == nil {
			wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException",
				"1 validation error detected: Value null at '"+f.name+"' failed to satisfy constraint: Member must not be null")

			return
		}
	}

	id, err := ext.CreateExportTask(r.Context(), &logdriver.CreateExportTaskInput{
		TaskName: req.TaskName, LogGroupName: req.LogGroupName, LogStreamNamePrefix: req.LogStreamNamePrefix,
		From: time.UnixMilli(*req.From).UTC(), To: time.UnixMilli(*req.To).UTC(),
		Destination: req.Destination, DestinationPrefix: req.DestinationPrefix,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]string{"taskId": id})
}

type exportTaskStatusJSON struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

type exportExecutionInfoJSON struct {
	CreationTime   int64 `json:"creationTime,omitempty"`
	CompletionTime int64 `json:"completionTime,omitempty"`
}

type exportTaskJSON struct {
	TaskID            string                  `json:"taskId"`
	TaskName          string                  `json:"taskName,omitempty"`
	LogGroupName      string                  `json:"logGroupName"`
	From              int64                   `json:"from"`
	To                int64                   `json:"to"`
	Destination       string                  `json:"destination"`
	DestinationPrefix string                  `json:"destinationPrefix"`
	Status            exportTaskStatusJSON    `json:"status"`
	ExecutionInfo     exportExecutionInfoJSON `json:"executionInfo"`
}

func (h *Handler) describeExportTasks(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "DescribeExportTasks")
	if !ok {
		return
	}

	var req struct {
		TaskID     string `json:"taskId"`
		StatusCode string `json:"statusCode"`
		Limit      int32  `json:"limit"`
		NextToken  string `json:"nextToken"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if req.Limit < 0 || req.Limit > maxDescribeExportTasks {
		wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterException",
			"1 validation error detected: Value at 'limit' failed to satisfy constraint: "+
				"Member must have value between 1 and 50")

		return
	}

	tasks, err := ext.DescribeExportTasks(r.Context(), logdriver.DescribeExportTasksInput{
		TaskID: req.TaskID, StatusCode: req.StatusCode,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	from, to, next := pageBounds(len(tasks), decodeOffsetToken(req.NextToken), req.Limit)

	out := make([]exportTaskJSON, 0, to-from)
	for i := range tasks[from:to] {
		out = append(out, exportTaskToJSON(&tasks[from+i]))
	}

	resp := map[string]any{"exportTasks": out}
	if next > 0 {
		resp["nextToken"] = encodeOffsetToken(next)
	}

	wire.WriteJSON(w, resp)
}

func exportTaskToJSON(t *logdriver.ExportTask) exportTaskJSON {
	return exportTaskJSON{
		TaskID: t.TaskID, TaskName: t.TaskName, LogGroupName: t.LogGroupName,
		From: t.From.UnixMilli(), To: t.To.UnixMilli(),
		Destination: t.Destination, DestinationPrefix: t.DestinationPrefix,
		Status: exportTaskStatusJSON{Code: t.StatusCode, Message: t.StatusMessage},
		ExecutionInfo: exportExecutionInfoJSON{
			CreationTime: epochMillis(t.CreationTime), CompletionTime: epochMillis(t.CompletionTime),
		},
	}
}

func (h *Handler) cancelExportTask(w http.ResponseWriter, r *http.Request) {
	ext, ok := h.awsLogs(w, "CancelExportTask")
	if !ok {
		return
	}

	var req struct {
		TaskID string `json:"taskId"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := ext.CancelExportTask(r.Context(), req.TaskID); err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, struct{}{})
}
