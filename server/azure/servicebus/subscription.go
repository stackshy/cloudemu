package servicebus

import (
	"context"
	"net/http"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

const defaultRuleName = "$Default"

func (h *Handler) serveSubCollection(w http.ResponseWriter, r *http.Request, sp sbPath, topic string) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.mu.RLock()

	t, ok := h.lookupTopic(sp, topic)
	if !ok {
		h.mu.RUnlock()
		writeTopicNotFound(w, topic)

		return
	}

	resources := make([]any, 0, len(t.Subs))
	for _, n := range sortedKeys(t.Subs) {
		resources = append(resources, h.toSubResource(sp, topic, t.Subs[n]))
	}

	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, paginate(r, resources))
}

func (h *Handler) serveSubscription(w http.ResponseWriter, r *http.Request, sp sbPath, topic, name string) {
	switch r.Method {
	case http.MethodPut:
		h.createSubscription(w, r, sp, topic, name)
	case http.MethodGet:
		h.getSubscription(w, sp, topic, name)
	case http.MethodDelete:
		h.deleteSubscription(w, sp, topic, name)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createSubscription(w http.ResponseWriter, r *http.Request, sp sbPath, topic, name string) {
	var req createSubscriptionRequest
	if !decodeBody(w, r, &req) {
		return
	}

	h.mu.Lock()

	t, ok := h.lookupTopic(sp, topic)
	if !ok {
		h.mu.Unlock()
		writeTopicNotFound(w, topic)

		return
	}

	now := time.Now().UTC()
	lockSeconds := lockDurationSeconds(req.Properties.LockDuration)

	rec, existed := t.Subs[name]
	if !existed {
		// Duplicate detection is a topic-level property; the subscription backing
		// store inherits it so a repeated MessageId published to the topic is
		// deduplicated per subscription (the same observable result as topic-level
		// detection).
		dup := topicDupDetection(t)

		url, dlqURL, err := h.createSubQueue(r, sp.namespace, topic, name, lockSeconds, &req.Properties, dup)
		if err != nil {
			h.mu.Unlock()
			azurearm.WriteCErr(w, err)

			return
		}

		rec = &subscriptionRecord{Name: name, DriverURL: url, DLQURL: dlqURL, Rules: map[string]*ruleRecord{}, CreatedAt: now}
		rec.Rules[defaultRuleName] = defaultRule()
		t.Subs[name] = rec
	} else if rec.DriverURL != "" {
		// PUT is create-or-update: propagate a LockDuration, MaxDeliveryCount or
		// deadLetteringOnMessageExpiration change onto the backing store so, e.g.,
		// lowering maxDeliveryCount dead-letters at the new threshold.
		_ = h.mq.SetQueueAttributes(r.Context(), rec.DriverURL, map[string]int{
			"VisibilityTimeout":      lockSeconds,
			"MaxDeliveryCount":       effectiveSubMaxDeliveryCount(&req.Properties),
			"DeadLetterOnExpiration": boolToInt(req.Properties.DeadLetteringOnExpiration),
		})
	}

	rec.Props = buildSubProps(&req.Properties, rec.CreatedAt, now)
	rec.UpdatedAt = now

	resource := h.toSubResource(sp, topic, rec)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) getSubscription(w http.ResponseWriter, sp sbPath, topic, name string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	rec, ok := h.lookupSub(sp, topic, name)
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "subscription not found: "+name)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, h.toSubResource(sp, topic, rec))
}

func (h *Handler) deleteSubscription(w http.ResponseWriter, sp sbPath, topic, name string) {
	h.mu.Lock()

	t, ok := h.lookupTopic(sp, topic)
	if !ok {
		h.mu.Unlock()
		writeTopicNotFound(w, topic)

		return
	}

	rec, ok := t.Subs[name]
	if !ok {
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

		return
	}

	url := rec.DriverURL
	dlqURL := rec.DLQURL

	delete(t.Subs, name)
	h.mu.Unlock()

	h.deleteBackingQueue(url)
	h.deleteBackingQueue(dlqURL)
	w.WriteHeader(http.StatusOK)
}

// dupConfig captures the parent topic's duplicate-detection settings that a
// subscription backing store inherits.
type dupConfig struct {
	enabled bool
	window  time.Duration
}

// topicDupDetection resolves a topic's duplicate-detection settings for its
// subscriptions' backing stores.
func topicDupDetection(t *topicRecord) dupConfig {
	return dupConfig{
		enabled: t.Props.RequiresDuplicateDetection,
		window:  dupDetectionWindow(t.Props.DuplicateDetectionHistoryTimeWindow),
	}
}

// createSubQueue provisions the backing message store for a new subscription
// plus its paired $DeadLetterQueue, honoring the subscription's LockDuration,
// MaxDeliveryCount and deadLetteringOnMessageExpiration, plus the parent topic's
// duplicate detection. It returns the primary and dead-letter store URLs.
func (h *Handler) createSubQueue(
	r *http.Request, namespace, topic, sub string, lockSeconds int, props *subscriptionProperties, dup dupConfig,
) (url, dlqURL string, err error) {
	name := namespace + "/" + topic + "/" + segSubs + "/" + sub

	dlqURL, err = h.provisionDLQ(r, name)
	if err != nil {
		return "", "", err
	}

	info, err := h.mq.CreateQueue(r.Context(), mqdriver.QueueConfig{
		Name:              name,
		VisibilityTimeout: lockSeconds,
		DeadLetterQueue: &mqdriver.DeadLetterConfig{
			TargetQueueURL:  dlqURL,
			MaxReceiveCount: effectiveSubMaxDeliveryCount(props),
		},
		DeadLetterOnExpiration:     props.DeadLetteringOnExpiration,
		RequiresDuplicateDetection: dup.enabled,
		DuplicateDetectionWindow:   dup.window,
		RequiresSession:            props.RequiresSession,
	})
	if err != nil && !cerrors.IsAlreadyExists(err) {
		return "", "", err
	}

	if info != nil {
		return info.URL, dlqURL, nil
	}

	return "", dlqURL, nil
}

// effectiveSubMaxDeliveryCount mirrors effectiveMaxDeliveryCount for a
// subscription's MaxDeliveryCount.
func effectiveSubMaxDeliveryCount(p *subscriptionProperties) int {
	if p.MaxDeliveryCount > 0 {
		return int(p.MaxDeliveryCount)
	}

	return defaultMaxDeliveryCount
}

// lookupTopic returns the topic record; caller must hold h.mu.
func (h *Handler) lookupTopic(sp sbPath, topic string) (*topicRecord, bool) {
	ns, ok := h.getNS(sp)
	if !ok {
		return nil, false
	}

	t, ok := ns.Topics[topic]

	return t, ok
}

// lookupSub returns the subscription record; caller must hold h.mu.
func (h *Handler) lookupSub(sp sbPath, topic, name string) (*subscriptionRecord, bool) {
	t, ok := h.lookupTopic(sp, topic)
	if !ok {
		return nil, false
	}

	s, ok := t.Subs[name]

	return s, ok
}

func buildSubProps(in *subscriptionProperties, created, updated time.Time) subscriptionProperties {
	out := *in
	out.Status = statusActive

	if out.LockDuration == "" {
		out.LockDuration = defaultLockDuration
	}

	if out.DefaultMessageTimeToLive == "" {
		out.DefaultMessageTimeToLive = maxTimeToLive
	}

	if out.MaxDeliveryCount == 0 {
		out.MaxDeliveryCount = defaultMaxDeliveryCount
	}

	out.CountDetails = &countDetails{}
	out.CreatedAt = &created
	out.UpdatedAt = &updated
	out.AccessedAt = &updated

	return out
}

// toSubResource renders a subscription's ARM shape, refreshing its live message
// counts from the backing stores (mirroring toQueueResource): activeMessageCount
// from the subscription store and deadLetterMessageCount from its paired
// $DeadLetterQueue, with messageCount as their sum.
func (h *Handler) toSubResource(sp sbPath, topic string, rec *subscriptionRecord) subscriptionResource {
	props := rec.Props

	active := h.approxCount(rec.DriverURL)
	dead := h.approxCount(rec.DLQURL)

	props.MessageCount = active + dead
	props.CountDetails = &countDetails{ActiveMessageCount: active, DeadLetterMessageCount: dead}

	return subscriptionResource{
		ID: azurearm.BuildResourceID(sp.sub, sp.rg, providerName, resourceType, sp.namespace) +
			"/topics/" + topic + "/subscriptions/" + rec.Name,
		Name:       rec.Name,
		Type:       providerName + "/Namespaces/Topics/Subscriptions",
		Properties: props,
	}
}

// approxCount reports the approximate visible-message count of a backing store,
// or 0 when the URL is empty or the store is unavailable.
func (h *Handler) approxCount(url string) int64 {
	if url == "" {
		return 0
	}

	if info, err := h.mq.GetQueueInfo(context.Background(), url); err == nil && info != nil {
		return int64(info.ApproxMessageCount)
	}

	return 0
}

// boolToInt encodes a bool as 0/1 for the int-only SetQueueAttributes map.
func boolToInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

func writeTopicNotFound(w http.ResponseWriter, name string) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "topic not found: "+name)
}
