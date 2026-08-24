package sqs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

// Message move task status values, matching the AWS SQS API.
const (
	moveTaskRunning   = "RUNNING"
	moveTaskCompleted = "COMPLETED"
	moveTaskCancelled = "CANCELLED" //nolint:misspell // AWS SQS API status literal (British spelling).
	moveTaskFailed    = "FAILED"
)

const (
	listMoveTasksDefault = 1
	listMoveTasksMax     = 10
)

// moveTask is the internal record for an SQS DLQ redrive task.
type moveTask struct {
	handle        string
	sourceARN     string
	sourceURL     string
	destARN       string
	maxRate       int
	status        string
	moved         int64
	toMove        int64
	failureReason string
	startedAt     time.Time
}

// StartMessageMoveTask starts an SQS message move (DLQ redrive) task. cloudemu
// runs the move synchronously: it drains the source (dead-letter) queue into the
// destination queue -- or, when destARN is empty, back to each message's original
// source queue -- and records a COMPLETED task. It is AWS-specific and reached
// via the SQS wire handler's optional messageMover interface.
func (m *Mock) StartMessageMoveTask(_ context.Context, sourceARN, destARN string, maxRate int) (string, error) {
	if sourceARN == "" {
		return "", errors.New(errors.InvalidArgument, "SourceArn is required")
	}

	sourceURL := m.urlForARN(sourceARN)
	if sourceURL == "" {
		return "", errors.Newf(errors.NotFound, "no queue found for source arn %q", sourceARN)
	}

	// Real SQS requires the source to be a dead-letter queue that some other queue
	// redrives to. cloudemu deliberately relaxes this: it accepts any existing
	// queue as a redrive source, so a user can seed a queue and redrive it without
	// first configuring a maxReceiveCount RedrivePolicy. This is a permissive
	// divergence (cloudemu accepts what AWS would reject), never the reverse.

	var destURL string

	if destARN != "" {
		destURL = m.urlForARN(destARN)
		if destURL == "" {
			return "", errors.Newf(errors.NotFound, "no queue found for destination arn %q", destARN)
		}
	}

	if err := m.rejectActiveMoveTask(sourceARN); err != nil {
		return "", err
	}

	moved, toMove, failure := m.drainSourceQueue(sourceURL, destURL)

	status := moveTaskCompleted
	if failure != "" {
		status = moveTaskFailed
	}

	handle := moveTaskHandle(idgen.GenerateID("mmt-"), sourceARN)

	m.moveTasks.Set(handle, &moveTask{
		handle:        handle,
		sourceARN:     sourceARN,
		sourceURL:     sourceURL,
		destARN:       destARN,
		maxRate:       maxRate,
		status:        status,
		moved:         moved,
		toMove:        toMove,
		failureReason: failure,
		startedAt:     m.opts.Clock.Now(),
	})

	return handle, nil
}

// rejectActiveMoveTask returns FailedPrecondition if a non-terminal move task
// already exists for the given source (SQS allows one active task per source).
func (m *Mock) rejectActiveMoveTask(sourceARN string) error {
	for _, t := range m.moveTasks.SortedValues() {
		if t.sourceARN == sourceARN && t.status == moveTaskRunning {
			return errors.Newf(errors.FailedPrecondition, "a message move task is already running for source %q", sourceARN)
		}
	}

	return nil
}

// drainSourceQueue moves every message out of the source queue into the
// destination (or, when destURL is empty, each message's recorded origin queue).
// It returns the number moved, the total that was queued to move, and a failure
// reason for any message with no resolvable destination.
func (m *Mock) drainSourceQueue(sourceURL, destURL string) (moved, toMove int64, failure string) {
	src, ok := m.queues.Get(sourceURL)
	if !ok {
		return 0, 0, ""
	}

	src.mu.Lock()
	pending := append([]*sqsMessage(nil), src.messages...)
	src.mu.Unlock()

	toMove = int64(len(pending))

	// Track the exact messages successfully moved (by pointer identity) rather
	// than rebuilding the source from a stale snapshot: messages delivered to the
	// DLQ while the move is in flight must survive, not be clobbered.
	movedSet := make(map[*sqsMessage]struct{}, len(pending))

	for _, msg := range pending {
		target := destURL
		if target == "" {
			target = m.redriveTargetURL(msg, sourceURL)
		}

		if target == "" || !m.appendMessageToQueue(target, msg) {
			failure = "no destination queue for one or more messages"

			continue
		}

		movedSet[msg] = struct{}{}
		moved++
	}

	src.mu.Lock()

	kept := make([]*sqsMessage, 0, len(src.messages))
	for _, msg := range src.messages {
		if _, done := movedSet[msg]; !done {
			kept = append(kept, msg)
		}
	}

	src.messages = kept
	src.mu.Unlock()

	return moved, toMove, failure
}

// redriveTargetURL resolves where a DLQ message should be redriven when no
// explicit destination is given: its recorded origin queue, else the single
// queue whose RedrivePolicy targets this DLQ.
func (m *Mock) redriveTargetURL(msg *sqsMessage, dlqURL string) string {
	if msg.sourceQueueURL != "" {
		return msg.sourceQueueURL
	}

	for _, qd := range m.queues.SortedValues() {
		qd.mu.Lock()
		match := qd.dlqConfig != nil && qd.dlqConfig.TargetQueueURL == dlqURL
		qd.mu.Unlock()

		if match {
			return qd.info.URL
		}
	}

	return ""
}

// appendMessageToQueue appends a copy of msg to the queue at url, resetting the
// visibility/receipt state so it is immediately receivable. Returns false if the
// target queue does not exist.
func (m *Mock) appendMessageToQueue(url string, msg *sqsMessage) bool {
	qd, ok := m.queues.Get(url)
	if !ok {
		return false
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	now := m.opts.Clock.Now()

	qd.messages = append(qd.messages, &sqsMessage{
		ID:                msg.ID,
		Body:              msg.Body,
		GroupID:           msg.GroupID,
		DeduplicationID:   msg.DeduplicationID,
		Attributes:        msg.Attributes,
		MessageAttributes: msg.MessageAttributes,
		SenderID:          msg.SenderID,
		SequenceNumber:    msg.SequenceNumber,
		VisibleAt:         now,
		SentAt:            now,
	})

	return true
}

// CancelMessageMoveTask cancels a RUNNING move task and returns the number of
// messages already moved. Because cloudemu completes moves synchronously, tasks
// are terminal (COMPLETED/FAILED) by the time a caller sees the handle, so this
// returns NotFound for any already-finished or unknown handle -- matching SQS,
// which only cancels tasks in the RUNNING state.
func (m *Mock) CancelMessageMoveTask(_ context.Context, taskHandle string) (int64, error) {
	task, ok := m.moveTasks.Get(taskHandle)
	if !ok {
		return 0, errors.Newf(errors.NotFound, "no message move task found for handle %q", taskHandle)
	}

	if task.status != moveTaskRunning {
		return 0, errors.Newf(errors.NotFound, "message move task %q is not running", taskHandle)
	}

	task.status = moveTaskCancelled

	return task.moved, nil
}

// ListMessageMoveTasks returns the move tasks for a source ARN, newest first,
// bounded by maxResults (default 1, max 10).
func (m *Mock) ListMessageMoveTasks(_ context.Context, sourceARN string, maxResults int) ([]driver.MessageMoveTask, error) {
	if sourceARN == "" {
		return nil, errors.New(errors.InvalidArgument, "SourceArn is required")
	}

	if m.urlForARN(sourceARN) == "" {
		return nil, errors.Newf(errors.NotFound, "no queue found for source arn %q", sourceARN)
	}

	limit := maxResults
	if limit <= 0 {
		limit = listMoveTasksDefault
	}

	if limit > listMoveTasksMax {
		limit = listMoveTasksMax
	}

	var tasks []*moveTask

	for _, t := range m.moveTasks.SortedValues() {
		if t.sourceARN == sourceARN {
			tasks = append(tasks, t)
		}
	}

	sort.SliceStable(tasks, func(i, j int) bool {
		return tasks[i].startedAt.After(tasks[j].startedAt)
	})

	if len(tasks) > limit {
		tasks = tasks[:limit]
	}

	out := make([]driver.MessageMoveTask, 0, len(tasks))
	for _, t := range tasks {
		// Real SQS only returns a TaskHandle for a RUNNING task (the handle exists
		// to cancel it); terminal tasks omit it. cloudemu completes moves
		// synchronously, so listed tasks are terminal and carry no handle.
		handle := ""
		if t.status == moveTaskRunning {
			handle = t.handle
		}

		out = append(out, driver.MessageMoveTask{
			TaskHandle:                   handle,
			SourceARN:                    t.sourceARN,
			DestinationARN:               t.destARN,
			MaxNumberOfMessagesPerSecond: t.maxRate,
			Status:                       t.status,
			ApproxMessagesMoved:          t.moved,
			ApproxMessagesToMove:         t.toMove,
			FailureReason:                t.failureReason,
			StartedAt:                    t.startedAt,
		})
	}

	return out, nil
}

// moveTaskHandle builds the opaque task handle real SQS returns: base64 of a
// small JSON object carrying the task id and source ARN.
func moveTaskHandle(taskID, sourceARN string) string {
	raw, _ := json.Marshal(struct {
		TaskID    string `json:"taskId"`
		SourceArn string `json:"sourceArn"`
	}{TaskID: taskID, SourceArn: sourceARN})

	return base64.StdEncoding.EncodeToString(raw)
}
