package pubsub

import (
	"net/http"
	"sort"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
)

// ---------- Subscriptions ----------

func (h *Handler) serveSubscription(w http.ResponseWriter, r *http.Request, project, name, action string) {
	if h.serveSubscriptionVerb(w, r, name, action) {
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createSubscription(w, r, project, name)
	case http.MethodGet:
		h.getSubscription(w, project, name)
	case http.MethodPatch:
		h.patchSubscription(w, r, name)
	case http.MethodDelete:
		h.deleteSubscription(w, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, reasonMethodNotAllowed, "method not allowed")
	}
}

// serveSubscriptionVerb dispatches the :verb sub-resource actions, returning
// true when it handled the request.
func (h *Handler) serveSubscriptionVerb(w http.ResponseWriter, r *http.Request, name, action string) bool {
	switch action {
	case "pull":
		h.pull(w, r, name)
	case "acknowledge":
		h.acknowledge(w, r, name)
	case "modifyAckDeadline":
		h.modifyAckDeadline(w, r, name)
	case "modifyPushConfig":
		h.modifyPushConfig(w, r, name)
	case "seek":
		h.seek(w, r, name)
	case "detach":
		h.detachSubscription(w, r, name)
	case verbGetIamPolicy, verbSetIamPolicy, verbTestIamPermissions:
		h.serveIam(w, r, resSubscriptions, name, action)
	default:
		return false
	}

	return true
}

func (h *Handler) createSubscription(w http.ResponseWriter, r *http.Request, project, name string) {
	var body subscription
	if !decodeJSON(w, r, &body) {
		return
	}

	if body.Topic == "" {
		writeError(w, http.StatusBadRequest, reasonInvalidArgument, "topic must be specified")
		return
	}

	topicShort := shortName(body.Topic)

	if _, err := h.findQueueByName(r, topicShort); err != nil {
		writeErr(w, err)
		return
	}

	if body.AckDeadlineSeconds == 0 {
		body.AckDeadlineSeconds = defaultAckDeadlineSeconds
	} else if !validAckDeadline(body.AckDeadlineSeconds) {
		writeError(w, http.StatusBadRequest, reasonInvalidArgument, ackDeadlineRangeMsg)
		return
	}

	filter, err := parseFilter(body.Filter)
	if err != nil {
		writeError(w, http.StatusBadRequest, reasonInvalidArgument, "invalid filter: "+err.Error())
		return
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

	h.newSub(name, topicShort, &cfg, filter)
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, cfg)
}

// patchSubscription applies subscriptions.patch: it merges only the fields named
// by updateMask into the stored config. filter/topic/name are immutable.
func (h *Handler) patchSubscription(w http.ResponseWriter, r *http.Request, name string) {
	var req updateSubscriptionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	masks := parseMask(req.UpdateMask)
	if len(masks) == 0 {
		writeError(w, http.StatusBadRequest, reasonInvalidArgument, "updateMask must be specified and non-empty")
		return
	}

	h.mu.Lock()

	sub, ok := h.subs[name]
	if !ok {
		h.mu.Unlock()
		writeError(w, http.StatusNotFound, reasonNotFound, "subscription "+name+" not found")

		return
	}

	if err := applySubMask(&sub.cfg, &req.Subscription, masks); err != nil {
		h.mu.Unlock()
		writeError(w, http.StatusBadRequest, reasonInvalidArgument, err.Error())

		return
	}

	cfg := sub.cfg
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, cfg)
}

// applySubMask merges the masked fields of src into dst. filter, topic and name
// are immutable in real Pub/Sub, so naming them in the mask is rejected;
// unsupported masks are ignored.
func applySubMask(dst, src *subscription, masks []string) error {
	setters := subMaskSetters()

	for _, m := range masks {
		switch m {
		case "filter", "topic", "name":
			return cerrors.Newf(cerrors.InvalidArgument, "field %q is immutable and cannot be updated", m)
		case "ackDeadlineSeconds":
			if !validAckDeadline(src.AckDeadlineSeconds) {
				return cerrors.New(cerrors.InvalidArgument, ackDeadlineRangeMsg)
			}
		}

		if set, ok := setters[m]; ok {
			set(dst, src)
		}
	}

	return nil
}

const (
	minAckDeadlineSeconds = 10
	maxAckDeadlineSeconds = 600
	ackDeadlineRangeMsg   = "ackDeadlineSeconds must be between 10 and 600"
)

// validAckDeadline reports whether s is within Pub/Sub's allowed ack-deadline
// range (10..600 seconds inclusive).
func validAckDeadline(s int) bool {
	return s >= minAckDeadlineSeconds && s <= maxAckDeadlineSeconds
}

// subMaskSetters maps a subscriptions.patch field-mask path to the merge that
// copies that one field from the request into the stored config.
func subMaskSetters() map[string]func(dst, src *subscription) {
	return map[string]func(dst, src *subscription){
		"ackDeadlineSeconds":       func(d, s *subscription) { d.AckDeadlineSeconds = s.AckDeadlineSeconds },
		"retryPolicy":              func(d, s *subscription) { d.RetryPolicy = s.RetryPolicy },
		"deadLetterPolicy":         func(d, s *subscription) { d.DeadLetterPolicy = s.DeadLetterPolicy },
		"pushConfig":               func(d, s *subscription) { d.PushConfig = s.PushConfig },
		"labels":                   func(d, s *subscription) { d.Labels = s.Labels },
		"messageRetentionDuration": func(d, s *subscription) { d.MessageRetentionDuration = s.MessageRetentionDuration },
		"expirationPolicy":         func(d, s *subscription) { d.ExpirationPolicy = s.ExpirationPolicy },
		"retainAckedMessages":      func(d, s *subscription) { d.RetainAckedMessages = s.RetainAckedMessages },
		"enableMessageOrdering":    func(d, s *subscription) { d.EnableMessageOrdering = s.EnableMessageOrdering },
		"detached":                 func(d, s *subscription) { d.Detached = s.Detached },
		"enableExactlyOnceDelivery": func(d, s *subscription) {
			d.EnableExactlyOnceDelivery = s.EnableExactlyOnceDelivery
		},
	}
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

// detachSubscription applies subscriptions.detach: it marks the subscription
// detached (it stops receiving messages) and returns an empty body, matching the
// real DetachSubscriptionResponse.
func (h *Handler) detachSubscription(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, reasonMethodNotAllowed, "method not allowed")
		return
	}

	h.mu.Lock()
	sub, ok := h.subs[name]

	if ok {
		sub.cfg.Detached = true
	}
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
