package servicebus

import (
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
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
		resources = append(resources, toSubResource(sp, topic, t.Subs[n]))
	}

	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, paginate(resources))
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

	rec, existed := t.Subs[name]
	if !existed {
		rec = &subscriptionRecord{Name: name, Rules: map[string]*ruleRecord{}, CreatedAt: now}
		rec.Rules[defaultRuleName] = defaultRule()
		t.Subs[name] = rec
	}

	rec.Props = buildSubProps(&req.Properties, rec.CreatedAt, now)
	rec.UpdatedAt = now

	resource := toSubResource(sp, topic, rec)
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

	azurearm.WriteJSON(w, http.StatusOK, toSubResource(sp, topic, rec))
}

func (h *Handler) deleteSubscription(w http.ResponseWriter, sp sbPath, topic, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	t, ok := h.lookupTopic(sp, topic)
	if !ok {
		writeTopicNotFound(w, topic)
		return
	}

	if _, ok := t.Subs[name]; !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	delete(t.Subs, name)
	w.WriteHeader(http.StatusOK)
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

func toSubResource(sp sbPath, topic string, rec *subscriptionRecord) subscriptionResource {
	return subscriptionResource{
		ID: azurearm.BuildResourceID(sp.sub, sp.rg, providerName, resourceType, sp.namespace) +
			"/topics/" + topic + "/subscriptions/" + rec.Name,
		Name:       rec.Name,
		Type:       providerName + "/Namespaces/Topics/Subscriptions",
		Properties: rec.Props,
	}
}

func writeTopicNotFound(w http.ResponseWriter, name string) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "topic not found: "+name)
}
