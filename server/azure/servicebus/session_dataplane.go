package servicebus

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

// Session data-plane surface. Azure Service Bus sessions have no real REST data
// plane — the official SDKs drive sessions over AMQP, which cloudemu does not
// speak — so this /{entity}/sessions/… route family is a cloudemu-proprietary
// REST extension. No real SDK exercises it; it lets a REST client drive the
// session model (accept-next-session, session-scoped receive, session lock,
// session state) that the faithful send-side SessionId enforcement pairs with.
const (
	segSessions        = "sessions"
	segState           = "state"
	segHead            = "head"
	sessionOwnerHeader = "X-Cloudemu-Session-Owner"
)

// sessionTarget is a parsed /{entity}/sessions/… data-plane URL.
type sessionTarget struct {
	namespace string
	entity    string
	sub       string // subscription name; "" for a queue
	sessionID string // "" on /sessions/head means accept-next-session
	head      bool   // .../sessions[/{sid}]/head — receive
	state     bool   // .../sessions/{sid}/state — get/set session state
}

// isSessionPlanePath reports whether p is a session data-plane URL (contains a
// /sessions segment) rather than an ARM control-plane path.
func isSessionPlanePath(p string) bool {
	if strings.HasPrefix(p, "/subscriptions/") {
		return false
	}

	return strings.Contains(p, "/"+segSessions)
}

// parseSessionPath resolves the entity scope before the /sessions segment
// (reusing the /messages scope parser) and the session id / sub-route after it.
func parseSessionPath(p string) (sessionTarget, bool) {
	segs := strings.Split(strings.Trim(p, "/"), "/")

	si := -1

	for i, s := range segs {
		if s == segSessions {
			si = i
			break
		}
	}

	if si < 1 {
		return sessionTarget{}, false
	}

	scope := parseEntityScope(segs, si)

	tgt := sessionTarget{namespace: scope.namespace, entity: scope.entity, sub: scope.sub}
	if !parseSessionTail(segs[si+1:], &tgt) {
		return sessionTarget{}, false
	}

	return tgt, true
}

// parseSessionTail fills the session id / head / state fields from the segments
// after /sessions, reporting whether the shape is valid.
func parseSessionTail(tail []string, tgt *sessionTarget) bool {
	switch {
	case len(tail) == 1 && tail[0] == segHead:
		tgt.head = true // accept-next-session + receive
	case len(tail) == 1:
		tgt.sessionID = tail[0] // renew lock on a named session
	case len(tail) == 2 && tail[1] == segHead:
		tgt.sessionID, tgt.head = tail[0], true
	case len(tail) == 2 && tail[1] == segState:
		tgt.sessionID, tgt.state = tail[0], true
	default:
		return false
	}

	return true
}

// serveSessionPlane routes the session data-plane extension.
func (h *Handler) serveSessionPlane(w http.ResponseWriter, r *http.Request) {
	tgt, ok := parseSessionPath(r.URL.Path)
	if !ok || tgt.entity == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "malformed session data-plane path")
		return
	}

	sess, ok := h.mq.(mqdriver.AzureSessionQueue)
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "session data plane not supported")
		return
	}

	url, lockSecs, ok := h.resolveSessionEntity(&tgt)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "entity not found: "+tgt.entity)
		return
	}

	switch {
	case tgt.state:
		serveSessionState(w, r, sess, url, tgt.sessionID)
	case tgt.head:
		serveSessionReceive(w, r, sess, url, tgt.sessionID, lockSecs)
	case tgt.sessionID != "" && r.Method == http.MethodPost:
		renewSessionLock(w, r, sess, url, tgt.sessionID)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// resolveSessionEntity resolves the backing store URL and lock duration for a
// session-addressed queue or topic subscription.
func (h *Handler) resolveSessionEntity(tgt *sessionTarget) (url string, lockSecs int, ok bool) {
	if tgt.sub != "" {
		cfg, found := h.resolveSubscription(tgt.namespace, tgt.entity, tgt.sub)
		if !found {
			return "", 0, false
		}

		return cfg.url, cfg.lockSecs, true
	}

	cfg, found := h.resolveQueue(tgt.namespace, tgt.entity)
	if !found {
		return "", 0, false
	}

	return cfg.url, cfg.lockSecs, true
}

// serveSessionReceive handles POST (peek-lock) / DELETE (receive-and-delete) on
// .../sessions[/{sid}]/head. An empty session id accepts the next unlocked
// session. The accepted session and its lock owner are returned in headers.
func serveSessionReceive(
	w http.ResponseWriter, r *http.Request, sess mqdriver.AzureSessionQueue, url, sessionID string, lockSecs int,
) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	owner := sessionOwner(r)
	peekLock := r.Method == http.MethodPost

	// The accepted session id travels back in BrokerProperties (SessionId,
	// preserved from the send), so an accept-next caller learns which session it
	// holds without a separate field.
	_, msgs, err := sess.ReceiveSession(r.Context(), url, sessionID, owner, 1, lockSecs, peekLock)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.Header().Set(sessionOwnerHeader, owner)

	if len(msgs) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	msg := msgs[0]

	lockToken := ""
	status := http.StatusOK

	if peekLock {
		lockToken = msg.ReceiptHandle
		status = http.StatusCreated

		// Settle a peek-locked session message via the plain message-lock URL: the
		// message lock is separate from the session lock, and a /sessions/{sid}
		// infix would not route back to serveLockedMessage for completion.
		w.Header().Set("Location",
			"/"+sessionEntityBase(url)+"/"+segMessages+"/"+msg.MessageID+"/"+msg.ReceiptHandle)
	}

	w.Header().Set("BrokerProperties", brokerPropertiesHeader(&msg, lockToken))
	writeMessage(w, &msg, status)
}

// serveSessionState handles GET/PUT on .../sessions/{sid}/state.
func serveSessionState(
	w http.ResponseWriter, r *http.Request, sess mqdriver.AzureSessionQueue, url, sessionID string,
) {
	if sessionID == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "session id required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		state, err := sess.GetSessionState(r.Context(), url, sessionID)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(state)) //nolint:gosec // session state is an opaque octet-stream blob, not HTML
	case http.MethodPut:
		body, ok := readSendBody(w, r)
		if !ok {
			return
		}

		if err := sess.SetSessionState(r.Context(), url, sessionID, body); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// renewSessionLock handles POST on .../sessions/{sid} to extend the session lock
// held by the X-Cloudemu-Session-Owner the caller was granted at accept time.
func renewSessionLock(
	w http.ResponseWriter, r *http.Request, sess mqdriver.AzureSessionQueue, url, sessionID string,
) {
	owner := sessionOwner(r)
	if err := sess.RenewSessionLock(r.Context(), url, sessionID, owner, 0); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.Header().Set(sessionOwnerHeader, owner)
	w.WriteHeader(http.StatusOK)
}

// sessionOwner reads the caller's session-lock owner token, minting one when
// absent so an accept-next receiver gets a token to renew and complete with.
func sessionOwner(r *http.Request) string {
	if o := r.Header.Get(sessionOwnerHeader); o != "" {
		return o
	}

	return idgen.GenerateID("sb-session-")
}

// sessionEntityBase is the "{namespace}/{entity}" (or "/…/subscriptions/{sub}")
// URL prefix of a backing store URL, used for the peek-lock Location header. The
// backing URL is https://{acct}.servicebus.windows.net/{namespace}/{entity}[…].
func sessionEntityBase(backingURL string) string {
	if i := strings.Index(backingURL, ".windows.net/"); i >= 0 {
		return strings.Trim(backingURL[i+len(".windows.net/"):], "/")
	}

	return backingURL
}
