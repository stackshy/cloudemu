package eventhub

import (
	"net/http"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// serveEventHubTree dispatches the eventhubs / consumergroups / authorizationRules
// subtree. segs starts at the "eventhubs" segment.
func (h *Handler) serveEventHubTree(w http.ResponseWriter, r *http.Request, ep ehPath) {
	switch {
	case len(ep.segs) == 1:
		h.listEventHubs(w, r, ep)
	case len(ep.segs) == namePairLen:
		h.serveEventHub(w, r, ep, ep.segs[1])
	default:
		h.serveEventHubSub(w, r, ep)
	}
}

// serveEventHubSub handles the consumergroups and event-hub-scoped
// authorizationRules subtrees. Entered only with at least 3 segments
// (eventhubs/{eh}/{sub}...).
func (h *Handler) serveEventHubSub(w http.ResponseWriter, r *http.Request, ep ehPath) {
	eh := ep.segs[1]

	switch {
	case eq(ep.segs[2], segConsumerGroups):
		h.serveConsumerGroups(w, r, ep, eh, ep.segs[3:])
	case eq(ep.segs[2], segAuthRules):
		h.authRuleDispatch(w, r, ep.segs[3:], func() (authTarget, bool) { return h.ehAuthTargetLocked(ep, eh) })
	default:
		notImplemented(w)
	}
}

func (h *Handler) serveEventHub(w http.ResponseWriter, r *http.Request, ep ehPath, name string) {
	switch r.Method {
	case http.MethodPut:
		h.createEventHub(w, r, ep, name)
	case http.MethodGet:
		h.getEventHub(w, ep, name)
	case http.MethodDelete:
		h.deleteEventHub(w, ep, name)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createEventHub(w http.ResponseWriter, r *http.Request, ep ehPath, name string) {
	var req createEventHubRequest
	if !decodeBody(w, r, &req) {
		return
	}

	now := time.Now().UTC()

	h.mu.Lock()

	ns, ok := h.getNS(ep)
	if !ok {
		h.mu.Unlock()
		writeNSNotFound(w, ep.namespace)

		return
	}

	rec, existed := ns.EventHubs[name]
	if !existed {
		rec = &eventHubRecord{
			Name:           name,
			ConsumerGroups: map[string]*consumerGroupRecord{},
			AuthRules:      map[string]*authRuleRecord{},
			CreatedAt:      now,
		}
		// Every event hub is created with the built-in $Default consumer group.
		rec.ConsumerGroups[defaultConsumerGroup] = &consumerGroupRecord{
			Name: defaultConsumerGroup, CreatedAt: now, UpdatedAt: now,
		}
		ns.EventHubs[name] = rec
	}

	rec.Props = buildEventHubProps(&req.Properties, rec.CreatedAt, now)
	rec.UpdatedAt = now

	resource := toEventHubResource(ep, rec)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) getEventHub(w http.ResponseWriter, ep ehPath, name string) {
	h.withEventHub(w, ep, name, func(rec *eventHubRecord) {
		azurearm.WriteJSON(w, http.StatusOK, toEventHubResource(ep, rec))
	})
}

func (h *Handler) deleteEventHub(w http.ResponseWriter, ep ehPath, name string) {
	h.mu.Lock()

	ns, ok := h.getNS(ep)
	if !ok {
		h.mu.Unlock()
		writeNSNotFound(w, ep.namespace)

		return
	}

	if _, ok := ns.EventHubs[name]; !ok {
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

		return
	}

	delete(ns.EventHubs, name)
	h.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listEventHubs(w http.ResponseWriter, r *http.Request, ep ehPath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.mu.RLock()

	ns, ok := h.getNS(ep)
	if !ok {
		h.mu.RUnlock()
		writeNSNotFound(w, ep.namespace)

		return
	}

	out := make([]any, 0, len(ns.EventHubs))
	for _, n := range sortedKeys(ns.EventHubs) {
		out = append(out, toEventHubResource(ep, ns.EventHubs[n]))
	}

	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, paginate(r, out))
}

// withEventHub runs fn with the named event hub under a read lock, or writes 404.
func (h *Handler) withEventHub(w http.ResponseWriter, ep ehPath, name string, fn func(*eventHubRecord)) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	ns, ok := h.getNS(ep)
	if !ok {
		writeNSNotFound(w, ep.namespace)
		return
	}

	rec, ok := ns.EventHubs[name]
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "event hub not found: "+name)
		return
	}

	fn(rec)
}

// buildEventHubProps synthesizes the server-computed fields and defaults a real
// event hub reports when the client omits them.
func buildEventHubProps(in *eventHubProperties, created, updated time.Time) eventHubProperties {
	out := *in
	out.Status = statusActive

	if out.PartitionCount == nil || *out.PartitionCount <= 0 {
		n := int64(defaultPartitionCount)
		out.PartitionCount = &n
	}

	if out.MessageRetentionInDays == nil || *out.MessageRetentionInDays <= 0 {
		d := int64(defaultRetentionDays)
		out.MessageRetentionInDays = &d
	}

	out.PartitionIDs = partitionIDs(*out.PartitionCount)
	out.CreatedAt = &created
	out.UpdatedAt = &updated

	return out
}

// partitionIDs returns the "0".."n-1" shard ids a real event hub reports.
func partitionIDs(count int64) []string {
	ids := make([]string, 0, count)
	for i := int64(0); i < count; i++ {
		ids = append(ids, strconv.FormatInt(i, 10))
	}

	return ids
}

func toEventHubResource(ep ehPath, rec *eventHubRecord) eventHubResource {
	return eventHubResource{
		ID:         eventHubIDPrefix(ep, rec.Name),
		Name:       rec.Name,
		Type:       providerName + "/Namespaces/EventHubs",
		Properties: rec.Props,
	}
}

// eventHubIDPrefix is the full ARM id of an event hub under ep's namespace.
func eventHubIDPrefix(ep ehPath, name string) string {
	return nsIDPrefix(ep) + "/eventhubs/" + name
}

// nsIDPrefix is the full ARM id of ep's namespace.
func nsIDPrefix(ep ehPath) string {
	return azurearm.BuildResourceID(ep.sub, ep.rg, providerName, resourceType, ep.namespace)
}
