package eventhub

import (
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// serveConsumerGroups dispatches .../eventhubs/{eh}/consumergroups[/{cg}]. rest
// is the path after "consumergroups".
func (h *Handler) serveConsumerGroups(w http.ResponseWriter, r *http.Request, ep ehPath, eh string, rest []string) {
	switch len(rest) {
	case 0:
		h.listConsumerGroups(w, r, ep, eh)
	case 1:
		h.serveConsumerGroup(w, r, ep, eh, rest[0])
	default:
		notImplemented(w)
	}
}

func (h *Handler) serveConsumerGroup(w http.ResponseWriter, r *http.Request, ep ehPath, eh, name string) {
	switch r.Method {
	case http.MethodPut:
		h.createConsumerGroup(w, r, ep, eh, name)
	case http.MethodGet:
		h.getConsumerGroup(w, ep, eh, name)
	case http.MethodDelete:
		h.deleteConsumerGroup(w, ep, eh, name)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createConsumerGroup(w http.ResponseWriter, r *http.Request, ep ehPath, eh, name string) {
	var req createConsumerGroupRequest
	if !decodeBody(w, r, &req) {
		return
	}

	now := time.Now().UTC()

	h.mu.Lock()

	rec, ok := h.eventHubLocked(ep, eh)
	if !ok {
		h.mu.Unlock()
		writeEHNotFound(w, eh)

		return
	}

	cg, existed := rec.ConsumerGroups[name]
	if !existed {
		cg = &consumerGroupRecord{Name: name, CreatedAt: now}
		rec.ConsumerGroups[name] = cg
	}

	cg.Props = consumerGroupProperties{UserMetadata: req.Properties.UserMetadata}
	cg.UpdatedAt = now

	resource := toConsumerGroupResource(ep, eh, cg)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) getConsumerGroup(w http.ResponseWriter, ep ehPath, eh, name string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	rec, ok := h.eventHubLocked(ep, eh)
	if !ok {
		writeEHNotFound(w, eh)
		return
	}

	cg, ok := rec.ConsumerGroups[name]
	if !ok {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "consumer group not found: "+name)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toConsumerGroupResource(ep, eh, cg))
}

func (h *Handler) deleteConsumerGroup(w http.ResponseWriter, ep ehPath, eh, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	rec, ok := h.eventHubLocked(ep, eh)
	if !ok {
		writeEHNotFound(w, eh)
		return
	}

	if _, ok := rec.ConsumerGroups[name]; !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	delete(rec.ConsumerGroups, name)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listConsumerGroups(w http.ResponseWriter, r *http.Request, ep ehPath, eh string) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.mu.RLock()

	rec, ok := h.eventHubLocked(ep, eh)
	if !ok {
		h.mu.RUnlock()
		writeEHNotFound(w, eh)

		return
	}

	out := make([]any, 0, len(rec.ConsumerGroups))
	for _, n := range sortedKeys(rec.ConsumerGroups) {
		out = append(out, toConsumerGroupResource(ep, eh, rec.ConsumerGroups[n]))
	}

	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, paginate(r, out))
}

// eventHubLocked returns the named event hub under ep's namespace. Callers hold
// h.mu.
func (h *Handler) eventHubLocked(ep ehPath, eh string) (*eventHubRecord, bool) {
	ns, ok := h.getNS(ep)
	if !ok {
		return nil, false
	}

	rec, ok := ns.EventHubs[eh]

	return rec, ok
}

func writeEHNotFound(w http.ResponseWriter, name string) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "event hub not found: "+name)
}

func toConsumerGroupResource(ep ehPath, eh string, cg *consumerGroupRecord) consumerGroupResource {
	props := cg.Props
	created := cg.CreatedAt
	updated := cg.UpdatedAt
	props.CreatedAt = &created
	props.UpdatedAt = &updated

	return consumerGroupResource{
		ID:         eventHubIDPrefix(ep, eh) + "/consumergroups/" + cg.Name,
		Name:       cg.Name,
		Type:       providerName + "/Namespaces/EventHubs/ConsumerGroups",
		Properties: props,
	}
}
