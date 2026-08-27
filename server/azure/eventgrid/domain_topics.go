package eventgrid

import (
	"net/http"
	"sort"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	domainTopicResourceType      = "Microsoft.EventGrid/domains/topics"
	domainTopicProvisioningState = "Succeeded"
)

// domainTopicJSON is the ARM DomainTopic resource shape
// (armeventgrid.DomainTopic): id/name/type plus a provisioningState-only
// properties object — a domain topic carries no other configurable state.
type domainTopicJSON struct {
	ID         string                 `json:"id,omitempty"`
	Name       string                 `json:"name"`
	Type       string                 `json:"type"`
	Properties *domainTopicProperties `json:"properties,omitempty"`
}

type domainTopicProperties struct {
	ProvisioningState string `json:"provisioningState,omitempty"`
}

type domainTopicListResult struct {
	Value []domainTopicJSON `json:"value"`
}

func domainTopicID(rp *azurearm.ResourcePath) string {
	return domainID(rp) + "/" + typeTopics + "/" + rp.SubResourceName
}

func domainTopicJSONFor(rp *azurearm.ResourcePath) domainTopicJSON {
	return domainTopicJSON{
		ID:         domainTopicID(rp),
		Name:       rp.SubResourceName,
		Type:       domainTopicResourceType,
		Properties: &domainTopicProperties{ProvisioningState: domainTopicProvisioningState},
	}
}

// serveDomainTopics routes .../domains/{domain}/topics[/{topicName}].
func (h *Handler) serveDomainTopics(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.SubResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listDomainTopics(w, rp)

		return
	}

	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateDomainTopic(w, rp)
	case http.MethodGet:
		h.getDomainTopic(w, rp)
	case http.MethodDelete:
		h.deleteDomainTopic(w, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

// createOrUpdateDomainTopic requires the parent domain to already exist —
// real Event Grid answers ParentResourceNotFound (404) for a domain topic
// created under an absent domain.
func (h *Handler) createOrUpdateDomainTopic(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.Lock()

	rec := h.domains[key]
	if rec == nil {
		h.mu.Unlock()
		azurearm.WriteError(w, http.StatusNotFound, "ParentResourceNotFound",
			"domain "+rp.ResourceName+" not found")

		return
	}

	rec.topics[rp.SubResourceName] = struct{}{}
	h.mu.Unlock()

	// 201 with a terminal provisioningState completes the SDK's LRO poller on
	// the first response.
	azurearm.WriteJSON(w, http.StatusCreated, domainTopicJSONFor(rp))
}

func (h *Handler) getDomainTopic(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.RLock()
	defer h.mu.RUnlock()

	rec := h.domains[key]
	if rec == nil {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "domain "+rp.ResourceName+" not found")
		return
	}

	if _, found := rec.topics[rp.SubResourceName]; !found {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "domain topic not found")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, domainTopicJSONFor(rp))
}

// deleteDomainTopic is idempotent, matching real ARM delete semantics: a
// delete of an already-missing domain topic (or one under a missing domain)
// still completes 200.
func (h *Handler) deleteDomainTopic(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.Lock()

	if rec := h.domains[key]; rec != nil {
		delete(rec.topics, rp.SubResourceName)
	}

	h.mu.Unlock()

	// The SDK's BeginDelete LRO completes on a 200 first response.
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listDomainTopics(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.RLock()
	defer h.mu.RUnlock()

	rec := h.domains[key]
	if rec == nil {
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "domain "+rp.ResourceName+" not found")
		return
	}

	names := make([]string, 0, len(rec.topics))
	for name := range rec.topics {
		names = append(names, name)
	}

	sort.Strings(names)

	out := make([]domainTopicJSON, 0, len(names))

	for _, name := range names {
		scoped := *rp
		scoped.SubResourceName = name
		out = append(out, domainTopicJSONFor(&scoped))
	}

	azurearm.WriteJSON(w, http.StatusOK, domainTopicListResult{Value: out})
}
