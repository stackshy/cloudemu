package servicebus

import (
	"context"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

// Compile-time check that Mock implements the optional session surface.
var _ driver.AzureSessionQueue = (*Mock)(nil)

// ReceiveSession returns up to maxMessages currently-visible messages for a
// session in FIFO order and acquires or refreshes the session lock for
// lockOwner. An empty sessionID accepts the next unlocked session that has a
// visible message; the accepted session id is returned. peekLock=false completes
// the returned messages immediately (receive-and-delete).
//
// Service Bus sessions have no real REST data plane (real SDKs drive them over
// AMQP), so this receive side is a CloudEmu REST extension; the send-side
// SessionId enforcement it pairs with is faithful to real Azure.
func (m *Mock) ReceiveSession(
	_ context.Context, queueURL, sessionID, lockOwner string, maxMessages, lockSecs int, peekLock bool,
) (string, []driver.Message, error) {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return "", nil, cerrors.Newf(cerrors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	if !qd.requiresSession {
		return "", nil, cerrors.New(cerrors.InvalidArgument, "entity is not session-enabled")
	}

	qd.ensureSessions()

	now := m.opts.Clock.Now()

	if lockSecs <= 0 {
		lockSecs = qd.visibilityTimeout
	}

	sid := sessionID
	if sid == "" {
		if sid = qd.nextAvailableSession(lockOwner, now); sid == "" {
			return "", nil, nil // no session with a visible message is available
		}
	}

	if !qd.acquireSessionLock(sid, lockOwner, now, lockSecs) {
		return "", nil, cerrors.Newf(cerrors.FailedPrecondition, "session %q is locked by another receiver", sid)
	}

	results, toRemove := m.collectVisibleMessages(qd, clampMaxMessages(maxMessages), lockSecs, now,
		func(msg *sbMessage) bool { return msg.SessionID == sid })
	removeByIndices(qd, toRemove)

	if !peekLock {
		deleteMessagesByID(qd, results)
	}

	if results == nil {
		results = []driver.Message{}
	}

	return sid, results, nil
}

// GetSessionState returns the opaque state blob for a session (empty when the
// session has no stored state).
func (m *Mock) GetSessionState(_ context.Context, queueURL, sessionID string) (string, error) {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return "", cerrors.Newf(cerrors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	if s := qd.sessions[sessionID]; s != nil {
		return s.State, nil
	}

	return "", nil
}

// SetSessionState writes a session's opaque state blob.
func (m *Mock) SetSessionState(_ context.Context, queueURL, sessionID, state string) error {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	if !qd.requiresSession {
		return cerrors.New(cerrors.InvalidArgument, "entity is not session-enabled")
	}

	qd.ensureSessions()

	qd.session(sessionID).State = state

	return nil
}

// RenewSessionLock extends the session lock held by lockOwner.
func (m *Mock) RenewSessionLock(_ context.Context, queueURL, sessionID, lockOwner string, lockSecs int) error {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	now := m.opts.Clock.Now()

	s := qd.sessions[sessionID]
	if s == nil || s.LockOwner != lockOwner || !s.LockedUntil.After(now) {
		return cerrors.Newf(cerrors.FailedPrecondition, "session %q is not locked by this receiver", sessionID)
	}

	if lockSecs <= 0 {
		lockSecs = qd.visibilityTimeout
	}

	s.LockedUntil = now.Add(time.Duration(lockSecs) * time.Second)

	return nil
}

// ensureSessions lazily allocates the session map (a restored session queue with
// no sessions yet carries a nil map from the omitempty snapshot).
func (qd *queueData) ensureSessions() {
	if qd.sessions == nil {
		qd.sessions = make(map[string]*sessionState)
	}
}

// session returns the state for sid, creating an unlocked entry if absent.
func (qd *queueData) session(sid string) *sessionState {
	s := qd.sessions[sid]
	if s == nil {
		s = &sessionState{}
		qd.sessions[sid] = s
	}

	return s
}

// nextAvailableSession returns the SessionId of the first message (in FIFO order)
// that is currently visible and belongs to a session that is unlocked, has an
// expired lock, or is already held by lockOwner. Empty when none is available.
func (qd *queueData) nextAvailableSession(lockOwner string, now time.Time) string {
	for _, msg := range qd.messages {
		if msg.SessionID == "" || msg.VisibleAt.After(now) {
			continue
		}

		if s := qd.sessions[msg.SessionID]; s != nil &&
			s.LockOwner != "" && s.LockOwner != lockOwner && s.LockedUntil.After(now) {
			continue // locked by another receiver
		}

		return msg.SessionID
	}

	return ""
}

// acquireSessionLock grants or refreshes the session lock for lockOwner,
// reporting false when another receiver already holds an unexpired lock.
func (qd *queueData) acquireSessionLock(sid, lockOwner string, now time.Time, lockSecs int) bool {
	s := qd.session(sid)
	if s.LockOwner != "" && s.LockOwner != lockOwner && s.LockedUntil.After(now) {
		return false
	}

	s.LockOwner = lockOwner
	s.LockedUntil = now.Add(time.Duration(lockSecs) * time.Second)

	return true
}

// deleteMessagesByID drops the messages just returned by a receive-and-delete
// (matched by id).
func deleteMessagesByID(qd *queueData, msgs []driver.Message) {
	if len(msgs) == 0 {
		return
	}

	ids := make(map[string]bool, len(msgs))
	for i := range msgs {
		ids[msgs[i].MessageID] = true
	}

	kept := qd.messages[:0]

	for _, msg := range qd.messages {
		if !ids[msg.ID] {
			kept = append(kept, msg)
		}
	}

	qd.messages = kept
}
