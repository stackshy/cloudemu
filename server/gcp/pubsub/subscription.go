package pubsub

import (
	"net/http"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
)

// ---------- Subscriptions ----------

func (h *Handler) serveSubscription(w http.ResponseWriter, r *http.Request, project, name, action string) {
	switch action {
	case "pull":
		h.pull(w, r, name)
		return
	case "acknowledge":
		h.acknowledge(w, r, name)
		return
	case "modifyAckDeadline":
		h.modifyAckDeadline(w, r, name)
		return
	case "modifyPushConfig":
		h.modifyPushConfig(w, r, name)
		return
	case "seek":
		h.seek(w, r, name)
		return
	case verbGetIamPolicy, verbSetIamPolicy, verbTestIamPermissions:
		h.serveIam(w, r, resSubscriptions, name, action)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createSubscription(w, r, project, name)
	case http.MethodGet:
		h.getSubscription(w, project, name)
	case http.MethodDelete:
		h.deleteSubscription(w, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, reasonMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) createSubscription(w http.ResponseWriter, r *http.Request, project, name string) {
	var body subscription
	if !decodeJSON(w, r, &body) {
		return
	}

	topicShort := shortName(body.Topic)
	if topicShort == "" {
		topicShort = name
	}

	if _, err := h.findQueueByName(r, topicShort); err != nil {
		writeErr(w, err)
		return
	}

	if body.AckDeadlineSeconds == 0 {
		body.AckDeadlineSeconds = defaultAckDeadlineSeconds
	}

	cfg := body
	cfg.Name = subscriptionName(project, name)
	cfg.Topic = topicName(project, topicShort)

	h.mu.Lock()
	if _, exists := h.subs[name]; exists {
		h.mu.Unlock()
		writeError(w, http.StatusConflict, reasonAlreadyExists, "subscription "+name+" already exists")

		return
	}

	h.newSub(name, topicShort, &cfg)
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, cfg)
}

func (h *Handler) getSubscription(w http.ResponseWriter, _, name string) {
	h.mu.RLock()
	var cfg subscription

	meta, ok := h.subs[name]
	if ok {
		cfg = meta.cfg
	}
	h.mu.RUnlock()

	if !ok {
		writeError(w, http.StatusNotFound, reasonNotFound, "subscription "+name+" not found")
		return
	}

	writeJSON(w, http.StatusOK, cfg)
}

func (h *Handler) deleteSubscription(w http.ResponseWriter, name string) {
	h.mu.Lock()
	_, ok := h.subs[name]
	delete(h.subs, name)
	h.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, reasonNotFound, "subscription "+name+" not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) listSubscriptions(w http.ResponseWriter, r *http.Request, _ string) {
	h.mu.RLock()
	items := make([]subscription, 0, len(h.subs))

	for _, meta := range h.subs {
		items = append(items, meta.cfg)
	}
	h.mu.RUnlock()

	page, err := pagination.PaginateSorted(items, func(a, b subscription) bool { return a.Name < b.Name },
		r.URL.Query().Get("pageToken"), pageSize(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, reasonInvalidArgument, "invalid pageToken")
		return
	}

	writeJSON(w, http.StatusOK, listSubscriptionsResponse{Subscriptions: page.Items, NextPageToken: page.NextPageToken})
}

func (h *Handler) listTopicSubscriptions(w http.ResponseWriter, r *http.Request, project, topicShort string) {
	h.mu.RLock()
	names := make([]string, 0)

	for subName, meta := range h.subs {
		if meta.topic == topicShort {
			names = append(names, subscriptionName(project, subName))
		}
	}
	h.mu.RUnlock()

	sort.Strings(names)
	page, err := pagination.Paginate(names, r.URL.Query().Get("pageToken"), pageSize(r))

	if err != nil {
		writeError(w, http.StatusBadRequest, reasonInvalidArgument, "invalid pageToken")
		return
	}

	writeJSON(w, http.StatusOK, listTopicSubscriptionsResponse{Subscriptions: page.Items, NextPageToken: page.NextPageToken})
}

func (h *Handler) pull(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, reasonMethodNotAllowed, "method not allowed")
		return
	}

	var req pullRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.MaxMessages <= 0 {
		req.MaxMessages = 1
	}

	h.mu.Lock()

	sub, err := h.resolveSubForPull(r, name)
	if err != nil {
		h.mu.Unlock()
		writeErr(w, err)

		return
	}

	msgs := h.deliver(sub, req.MaxMessages)
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, pullResponse{ReceivedMessages: msgs})
}

// resolveSubForPull returns the named subscription, lazily materializing an
// implicit one when a same-named topic exists (legacy topic==subscription
// tolerance). The implicit subscription reads the topic log from the start. The
// caller holds h.mu.
func (h *Handler) resolveSubForPull(r *http.Request, name string) (*subState, error) {
	if sub, ok := h.subs[name]; ok {
		return sub, nil
	}

	if _, err := h.findQueueByName(r, name); err != nil {
		return nil, err
	}

	sub := &subState{
		cfg:              subscription{AckDeadlineSeconds: defaultAckDeadlineSeconds},
		topic:            name,
		createTime:       time.Now().UTC(),
		acked:            make(map[int]bool),
		outstanding:      make(map[string]*lease),
		deliveryAttempts: make(map[int]int),
	}
	h.subs[name] = sub

	return sub, nil
}

func (h *Handler) acknowledge(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, reasonMethodNotAllowed, "method not allowed")
		return
	}

	var req acknowledgeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	h.mu.Lock()
	sub, ok := h.subs[name]

	if ok {
		for _, ack := range req.AckIDs {
			if l, has := sub.outstanding[ack]; has {
				sub.acked[l.msgIdx] = true
				delete(sub.outstanding, ack)
			}
		}
	}
	h.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, reasonNotFound, "subscription "+name+" not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) modifyAckDeadline(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, reasonMethodNotAllowed, "method not allowed")
		return
	}

	var req modifyAckDeadlineRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	h.mu.Lock()
	sub, ok := h.subs[name]

	if ok {
		deadline := time.Now().UTC().Add(time.Duration(req.AckDeadlineSeconds) * time.Second)

		for _, ack := range req.AckIDs {
			l, has := sub.outstanding[ack]
			if !has {
				continue
			}

			// ackDeadlineSeconds == 0 nacks the message: drop the lease so the
			// next pull redelivers it immediately.
			if req.AckDeadlineSeconds <= 0 {
				delete(sub.outstanding, ack)
				continue
			}

			l.deadline = deadline
		}
	}
	h.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, reasonNotFound, "subscription "+name+" not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) modifyPushConfig(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, reasonMethodNotAllowed, "method not allowed")
		return
	}

	var req modifyPushConfigRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	h.mu.Lock()
	sub, ok := h.subs[name]

	if ok {
		sub.cfg.PushConfig = req.PushConfig
	}
	h.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, reasonNotFound, "subscription "+name+" not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{})
}
