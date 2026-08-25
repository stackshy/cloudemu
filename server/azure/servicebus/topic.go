package servicebus

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// serveTopicTree dispatches the topics / subscriptions / rules subtree. The
// segments after the namespace name select the depth.
func (h *Handler) serveTopicTree(w http.ResponseWriter, r *http.Request, sp sbPath) {
	switch len(sp.segs) {
	case depthTopics:
		h.serveTopicCollection(w, r, sp)
	case depthTopic:
		h.serveTopic(w, r, sp, sp.segs[1])
	default:
		h.serveSubtree(w, r, sp)
	}
}

// serveSubtree handles the subscriptions (and deeper) portion of the tree; it
// is entered only when there are at least depthSubColl segments.
func (h *Handler) serveSubtree(w http.ResponseWriter, r *http.Request, sp sbPath) {
	segs := sp.segs
	if !eq(segs[2], segSubs) {
		notImplemented(w)
		return
	}

	switch len(segs) {
	case depthSubColl:
		h.serveSubCollection(w, r, sp, segs[1])
	case depthSub:
		h.serveSubscription(w, r, sp, segs[1], segs[3])
	default:
		h.serveRuleSubtree(w, r, sp)
	}
}

// serveRuleSubtree handles the rules portion of the tree.
func (h *Handler) serveRuleSubtree(w http.ResponseWriter, r *http.Request, sp sbPath) {
	segs := sp.segs
	if len(segs) > depthRule || !eq(segs[4], segRules) {
		notImplemented(w)
		return
	}

	switch len(segs) {
	case depthRuleColl:
		h.serveRuleCollection(w, r, sp, segs[1], segs[3])
	case depthRule:
		h.serveRule(w, r, sp, segs[1], segs[3], segs[5])
	default:
		notImplemented(w)
	}
}

func notImplemented(w http.ResponseWriter) {
	azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "unsupported path")
}

func (h *Handler) serveTopicCollection(w http.ResponseWriter, r *http.Request, sp sbPath) {
	h.listChildren(w, r, sp, func(ns *namespaceState) []any {
		out := make([]any, 0, len(ns.Topics))
		for _, n := range sortedKeys(ns.Topics) {
			out = append(out, toTopicResource(sp, ns.Topics[n]))
		}

		return out
	})
}

func (h *Handler) serveTopic(w http.ResponseWriter, r *http.Request, sp sbPath, name string) {
	switch r.Method {
	case http.MethodPut:
		h.createTopic(w, r, sp, name)
	case http.MethodGet:
		h.withTopic(w, sp, name, func(t *topicRecord) {
			azurearm.WriteJSON(w, http.StatusOK, toTopicResource(sp, t))
		})
	case http.MethodDelete:
		h.deleteTopic(w, sp, name)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createTopic(w http.ResponseWriter, r *http.Request, sp sbPath, name string) {
	var req createTopicRequest
	if !decodeBody(w, r, &req) {
		return
	}

	h.mu.Lock()

	ns, ok := h.getNS(sp)
	if !ok {
		h.mu.Unlock()
		writeNSNotFound(w, sp.namespace)

		return
	}

	now := time.Now().UTC()

	rec, existed := ns.Topics[name]
	if !existed {
		rec = &topicRecord{Name: name, Subs: map[string]*subscriptionRecord{}, CreatedAt: now}
		ns.Topics[name] = rec
	}

	rec.Props = buildTopicProps(&req.Properties, rec.CreatedAt, now, len(rec.Subs))
	rec.UpdatedAt = now

	resource := toTopicResource(sp, rec)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) deleteTopic(w http.ResponseWriter, sp sbPath, name string) {
	h.mu.Lock()

	ns, ok := h.getNS(sp)
	if !ok {
		h.mu.Unlock()
		writeNSNotFound(w, sp.namespace)

		return
	}

	t, ok := ns.Topics[name]
	if !ok {
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

		return
	}

	// Cascade: drop the backing message store of every subscription plus its
	// paired dead-letter store.
	urls := make([]string, 0, len(t.Subs))
	for _, s := range t.Subs {
		urls = append(urls, s.DriverURL, s.DLQURL)
	}

	delete(ns.Topics, name)
	h.mu.Unlock()

	for _, u := range urls {
		h.deleteBackingQueue(u)
	}

	w.WriteHeader(http.StatusOK)
}

// withTopic runs fn with the named topic under a read lock, or writes 404.
func (h *Handler) withTopic(w http.ResponseWriter, sp sbPath, name string, fn func(*topicRecord)) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	ns, ok := h.getNS(sp)
	if !ok {
		writeNSNotFound(w, sp.namespace)
		return
	}

	rec, ok := ns.Topics[name]
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "topic not found: "+name)
		return
	}

	fn(rec)
}

func buildTopicProps(in *topicProperties, created, updated time.Time, subCount int) topicProperties {
	out := *in
	out.Status = statusActive
	out.SubscriptionCount = int32Count(subCount)

	if out.DefaultMessageTimeToLive == "" {
		out.DefaultMessageTimeToLive = maxTimeToLive
	}

	if out.MaxSizeInMegabytes == 0 {
		out.MaxSizeInMegabytes = defaultMaxSizeMB
	}

	out.CountDetails = &countDetails{}
	out.CreatedAt = &created
	out.UpdatedAt = &updated
	out.AccessedAt = &updated

	return out
}

func toTopicResource(sp sbPath, rec *topicRecord) topicResource {
	props := rec.Props
	props.SubscriptionCount = int32Count(len(rec.Subs))

	return topicResource{
		ID: azurearm.BuildResourceID(sp.sub, sp.rg, providerName, resourceType, sp.namespace) +
			"/topics/" + rec.Name,
		Name:       rec.Name,
		Type:       providerName + "/Namespaces/Topics",
		Properties: props,
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil && err != io.EOF {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return false
	}

	return true
}

func eq(a, b string) bool { return strings.EqualFold(a, b) }
