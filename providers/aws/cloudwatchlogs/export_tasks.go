package cloudwatchlogs

import (
	"bytes"
	"compress/gzip"
	"context"
	"path"
	"sort"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// Export task defaults and messages, per the CreateExportTask API reference
// and what DescribeExportTasks reports.
const (
	defaultExportPrefix = "exportedlogs"
	// exportChunkName is the one object each exported stream gets. Real
	// exports split large streams into 000000.gz, 000001.gz, ...; the
	// emulator writes every event into the first chunk.
	exportChunkName      = "000000.gz"
	exportLineTimeLayout = "2006-01-02T15:04:05.000Z"
	exportContentType    = "application/octet-stream"

	exportMsgPending   = "Pending"
	exportMsgRunning   = "Started successfully"
	exportMsgCompleted = "Completed successfully"
	exportMsgCancelled = "Cancelled by user"                                              //nolint:misspell // matches the service's wording.
	exportStatusEnum   = "CANCELLED, COMPLETED, FAILED, PENDING, PENDING_CANCEL, RUNNING" //nolint:misspell // the API enum.
)

// exportTask is one export task. Status holds the stored outcome: COMPLETED
// until the task is canceled or fails. While its settle windows are open a
// COMPLETED task is observed as PENDING, then RUNNING. Written records that
// the task's objects were written (or that it never will write any).
type exportTask struct {
	ID           string        `json:"id"`
	Name         string        `json:"name,omitempty"`
	LogGroup     string        `json:"logGroup"`
	StreamPrefix string        `json:"streamPrefix,omitempty"`
	From         time.Time     `json:"from"`
	To           time.Time     `json:"to"`
	Destination  string        `json:"destination"`
	Prefix       string        `json:"prefix"`
	CreatedAt    time.Time     `json:"createdAt"`
	PendingFor   time.Duration `json:"pendingFor,omitempty"`
	RunFor       time.Duration `json:"runFor,omitempty"`
	Status       string        `json:"status"`
	Message      string        `json:"message,omitempty"`
	CompletedAt  time.Time     `json:"completedAt,omitempty"`
	Written      bool          `json:"written,omitempty"`
}

// observedStatus is the status a reader sees at now.
func (t *exportTask) observedStatus(now time.Time) string {
	if t.Status != driver.ExportTaskCompleted {
		return t.Status
	}

	running := settle.Pending(driver.ExportTaskRunning, t.CreatedAt, t.PendingFor+t.RunFor).
		Observe(now, driver.ExportTaskCompleted)

	return settle.Pending(driver.ExportTaskPending, t.CreatedAt, t.PendingFor).Observe(now, running)
}

func (t *exportTask) active(now time.Time) bool {
	s := t.observedStatus(now)

	return s == driver.ExportTaskPending || s == driver.ExportTaskRunning
}

func (t *exportTask) describe(now time.Time) driver.ExportTask {
	out := driver.ExportTask{
		TaskID: t.ID, TaskName: t.Name, LogGroupName: t.LogGroup, From: t.From, To: t.To,
		Destination: t.Destination, DestinationPrefix: t.Prefix,
		StatusCode: t.observedStatus(now), CreationTime: t.CreatedAt,
	}

	switch out.StatusCode {
	case driver.ExportTaskPending:
		out.StatusMessage = exportMsgPending
	case driver.ExportTaskRunning:
		out.StatusMessage = exportMsgRunning
	case driver.ExportTaskCompleted:
		out.StatusMessage = exportMsgCompleted
		out.CompletionTime = t.CreatedAt.Add(t.PendingFor + t.RunFor)
	default:
		out.StatusMessage = t.Message
		out.CompletionTime = t.CompletedAt
	}

	return out
}

// CreateExportTask validates the request and the destination bucket, then
// records the task. Like CloudWatch Logs it writes a permission-check object
// under the prefix before accepting the task. The events are written once the
// task settles to COMPLETED: straight away when async settling is off,
// otherwise on the first export-task call after it settles.
func (m *Mock) CreateExportTask(ctx context.Context, in *driver.CreateExportTaskInput) (string, error) {
	if err := validateExportRequest(in); err != nil {
		return "", err
	}

	g, ok := m.groups.Get(in.LogGroupName)
	if !ok {
		return "", cerrors.New(cerrors.NotFound, "The specified log group does not exist.")
	}

	if created, err := time.Parse(time.RFC3339, g.info.CreatedAt); err == nil && in.To.Before(created) {
		return "", cerrors.New(cerrors.InvalidArgument,
			"Specified time range of '"+in.To.Format(time.RFC3339)+"' is before the log group creation time.")
	}

	prefix := strings.TrimSuffix(in.DestinationPrefix, "/")
	if prefix == "" {
		prefix = defaultExportPrefix
	}

	if err := m.checkExportLimit(); err != nil {
		return "", err
	}

	if err := m.checkDestination(ctx, in.Destination, prefix); err != nil {
		return "", err
	}

	now := m.opts.Clock.Now()
	task := exportTask{
		ID: idgen.UUID(), Name: in.TaskName, LogGroup: in.LogGroupName, StreamPrefix: in.LogStreamNamePrefix,
		From: in.From, To: in.To, Destination: in.Destination, Prefix: prefix, CreatedAt: now,
		PendingFor: m.opts.SettleDuration(settle.DefaultExportTaskPendingSettle),
		RunFor:     m.opts.SettleDuration(settle.DefaultExportTaskRunSettle),
		Status:     driver.ExportTaskCompleted,
	}

	m.exportMu.Lock()

	if m.activeExport(now) {
		m.exportMu.Unlock()

		return "", errExportLimit()
	}

	m.exports.Set(task.ID, task)
	m.exportMu.Unlock()

	m.flushExports(ctx)

	return task.ID, nil
}

func errExportLimit() error {
	return cerrors.New(cerrors.ResourceExhausted, "Resource limit exceeded.")
}

// checkExportLimit rejects a new task while one is still active. It is
// checked again when the task is recorded, since the destination checks in
// between run without the lock.
func (m *Mock) checkExportLimit() error {
	m.exportMu.Lock()
	defer m.exportMu.Unlock()

	if m.activeExport(m.opts.Clock.Now()) {
		return errExportLimit()
	}

	return nil
}

// activeExport reports whether a PENDING or RUNNING task exists. Callers
// hold exportMu.
func (m *Mock) activeExport(now time.Time) bool {
	all := m.exports.All()
	for id := range all {
		if t := all[id]; t.active(now) {
			return true
		}
	}

	return false
}

func validateExportRequest(in *driver.CreateExportTaskInput) error {
	if err := validateLogGroupName(in.LogGroupName); err != nil {
		return err
	}

	switch {
	case in.Destination == "" || len(in.Destination) > maxLogNameLength:
		return cerrors.Newf(cerrors.InvalidArgument,
			"1 validation error detected: Value at 'destination' failed to satisfy constraint: "+
				"Member must have length between 1 and %d", maxLogNameLength)
	case len(in.TaskName) > maxLogNameLength:
		return cerrors.Newf(cerrors.InvalidArgument,
			"1 validation error detected: Value at 'taskName' failed to satisfy constraint: "+
				"Member must have length less than or equal to %d", maxLogNameLength)
	case len(in.LogStreamNamePrefix) > maxLogNameLength || strings.ContainsAny(in.LogStreamNamePrefix, ":*"):
		return cerrors.New(cerrors.InvalidArgument,
			"1 validation error detected: Value at 'logStreamNamePrefix' failed to satisfy constraint: "+
				"Member must satisfy regular expression pattern: [^:*]*")
	case in.From.Before(time.UnixMilli(0)) || in.To.Before(time.UnixMilli(0)):
		return cerrors.New(cerrors.InvalidArgument,
			"1 validation error detected: Value at 'from' or 'to' failed to satisfy constraint: "+
				"Member must have value greater than or equal to 0")
	case in.To.Before(in.From):
		return cerrors.New(cerrors.InvalidArgument, "The value of 'to' must be greater than or equal to the value of 'from'.")
	}

	return nil
}

// DescribeExportTasks lists export tasks, newest first, narrowed by task ID
// and status code.
func (m *Mock) DescribeExportTasks(ctx context.Context, in driver.DescribeExportTasksInput) ([]driver.ExportTask, error) {
	if in.StatusCode != "" && !validExportStatus(in.StatusCode) {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'statusCode' failed to satisfy constraint: "+
				"Member must satisfy enum value set: [%s]", in.StatusCode, exportStatusEnum)
	}

	m.flushExports(ctx)

	now := m.opts.Clock.Now()

	var out []driver.ExportTask

	all := m.exports.All()
	for id := range all {
		t := all[id]
		if in.TaskID != "" && t.ID != in.TaskID {
			continue
		}

		d := t.describe(now)
		if in.StatusCode != "" && d.StatusCode != in.StatusCode {
			continue
		}

		out = append(out, d)
	}

	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreationTime.Equal(out[j].CreationTime) {
			return out[i].CreationTime.After(out[j].CreationTime)
		}

		return out[i].TaskID < out[j].TaskID
	})

	return out, nil
}

func validExportStatus(s string) bool {
	switch s {
	case driver.ExportTaskPending, driver.ExportTaskRunning, driver.ExportTaskCompleted,
		driver.ExportTaskCancelled, driver.ExportTaskFailed, "PENDING_CANCEL":
		return true
	}

	return false
}

// CancelExportTask cancels a PENDING or RUNNING task. A canceled task writes
// no log objects.
func (m *Mock) CancelExportTask(_ context.Context, taskID string) error {
	m.exportMu.Lock()
	defer m.exportMu.Unlock()

	t, ok := m.exports.Get(taskID)
	if !ok {
		return cerrors.New(cerrors.NotFound, "The specified export task does not exist.")
	}

	now := m.opts.Clock.Now()
	if !t.active(now) {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"The specified export task has already finished. Its status is %s.", t.observedStatus(now))
	}

	t.Status, t.Message, t.CompletedAt, t.Written = driver.ExportTaskCancelled, exportMsgCancelled, now, true
	m.exports.Set(taskID, t)

	return nil
}

// flushExports writes the objects of every task that has settled to
// COMPLETED and has not written them yet. Tasks are claimed under exportMu
// and written after it is released, so no lock is held across an S3 call. A
// write that fails marks the task FAILED.
func (m *Mock) flushExports(ctx context.Context) {
	if m.exportStore == nil {
		return
	}

	now := m.opts.Clock.Now()

	var due []exportTask

	m.exportMu.Lock()

	all := m.exports.All()
	for id := range all {
		t := all[id]
		if t.Written || t.observedStatus(now) != driver.ExportTaskCompleted {
			continue
		}

		t.Written = true
		m.exports.Set(id, t)
		due = append(due, t)
	}

	m.exportMu.Unlock()

	for i := range due {
		if err := m.writeExport(ctx, &due[i]); err != nil {
			m.failExport(due[i].ID, err)
		}
	}
}

func (m *Mock) failExport(id string, err error) {
	m.exportMu.Lock()
	defer m.exportMu.Unlock()

	m.exports.Update(id, func(t exportTask) exportTask {
		t.Status, t.Message, t.CompletedAt = driver.ExportTaskFailed, cerrors.Message(err), m.opts.Clock.Now()
		return t
	})
}

// writeExport writes one gzip object per log stream that has events in the
// task's range, at <prefix>/<taskId>/<logStreamName>/000000.gz. Each line is
// the event's timestamp and its message.
func (m *Mock) writeExport(ctx context.Context, t *exportTask) error {
	g, ok := m.groups.Get(t.LogGroup)
	if !ok {
		return nil
	}

	m.purgeExpired(t.LogGroup, g)

	for _, s := range g.streams.SortedValues() {
		if !strings.HasPrefix(s.info.Name, t.StreamPrefix) {
			continue
		}

		body, n, err := exportStreamBody(s, t.From, t.To)
		if err != nil {
			return err
		}

		if n == 0 {
			continue
		}

		key := path.Join(t.Prefix, t.ID, s.info.Name, exportChunkName)
		if err := m.exportStore.PutObject(ctx, t.Destination, key, body, exportContentType, nil); err != nil {
			return cerrors.New(cerrors.InvalidArgument, putObjectFailedMsg)
		}
	}

	return nil
}

// exportStreamBody gzips a stream's events with a timestamp in [from, to] and
// returns the body and how many events it holds.
func exportStreamBody(s *logStream, from, to time.Time) (body []byte, n int, err error) {
	var lines strings.Builder

	s.mu.RLock()

	for i := range s.events {
		e := &s.events[i]
		if e.Timestamp.Before(from) || e.Timestamp.After(to) {
			continue
		}

		lines.WriteString(e.Timestamp.UTC().Format(exportLineTimeLayout))
		lines.WriteByte(' ')
		lines.WriteString(e.Message)
		lines.WriteByte('\n')

		n++
	}

	s.mu.RUnlock()

	if n == 0 {
		return nil, 0, nil
	}

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	if _, werr := zw.Write([]byte(lines.String())); werr != nil {
		return nil, 0, werr
	}

	if cerr := zw.Close(); cerr != nil {
		return nil, 0, cerr
	}

	return buf.Bytes(), n, nil
}
