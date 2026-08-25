package servicebus

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

func (h *Handler) serveNamespace(w http.ResponseWriter, r *http.Request, sp sbPath) {
	switch r.Method {
	case http.MethodPut:
		h.createNamespace(w, r, sp)
	case http.MethodPatch:
		h.updateNamespace(w, r, sp)
	case http.MethodGet:
		h.getNamespace(w, sp)
	case http.MethodDelete:
		h.deleteNamespace(w, sp)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (h *Handler) createNamespace(w http.ResponseWriter, r *http.Request, sp sbPath) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req createNamespaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}

	location := req.Location
	if location == "" {
		location = "eastus"
	}

	now := time.Now().UTC()

	h.mu.Lock()

	ns, existed := h.namespaces.Get(nsKey(sp.namespace))
	if !existed {
		ns = &namespaceState{
			Name:          sp.namespace,
			Subscription:  sp.sub,
			ResourceGroup: sp.rg,
			CreatedAt:     now,
			Queues:        map[string]*queueRecord{},
			Topics:        map[string]*topicRecord{},
			AuthRules:     map[string]*authRuleRecord{},
		}
		ns.AuthRules[defaultRootRuleName] = newRootAuthRule()
	}

	ns.Location = location
	ns.Tags = maps.Clone(req.Tags)
	ns.SKU = normalizeSKU(req.SKU)
	ns.UpdatedAt = now
	h.namespaces.Set(nsKey(sp.namespace), ns)

	resource := toNamespaceResource(ns)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) updateNamespace(w http.ResponseWriter, r *http.Request, sp sbPath) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req updateNamespaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}

	h.mu.Lock()

	ns, ok := h.getNS(sp)
	if !ok {
		h.mu.Unlock()
		writeNSNotFound(w, sp.namespace)

		return
	}

	if req.Location != "" {
		ns.Location = req.Location
	}

	if req.Tags != nil {
		ns.Tags = maps.Clone(req.Tags)
	}

	if req.SKU != nil {
		ns.SKU = normalizeSKU(req.SKU)
	}

	ns.UpdatedAt = time.Now().UTC()
	resource := toNamespaceResource(ns)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) getNamespace(w http.ResponseWriter, sp sbPath) {
	h.mu.RLock()

	ns, ok := h.getNS(sp)
	if !ok {
		h.mu.RUnlock()
		writeNSNotFound(w, sp.namespace)

		return
	}

	resource := toNamespaceResource(ns)
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

func (h *Handler) deleteNamespace(w http.ResponseWriter, sp sbPath) {
	h.mu.Lock()

	ns, ok := h.getNS(sp)
	if !ok {
		h.mu.Unlock()
		// A delete of a non-existent namespace returns 204 No Content in ARM.
		w.WriteHeader(http.StatusNoContent)

		return
	}

	// Cascade: drop the message store (and paired dead-letter store) for every
	// child queue and subscription.
	urls := make([]string, 0, len(ns.Queues))
	for _, q := range ns.Queues {
		urls = append(urls, q.DriverURL, q.DLQURL)
	}

	for _, t := range ns.Topics {
		for _, s := range t.Subs {
			urls = append(urls, s.DriverURL, s.DLQURL)
		}
	}

	h.namespaces.Delete(nsKey(sp.namespace))
	h.mu.Unlock()

	for _, u := range urls {
		h.deleteBackingQueue(u)
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listNamespaces(w http.ResponseWriter, sp sbPath) {
	h.mu.RLock()

	resources := make([]any, 0)

	for _, ns := range h.namespaces.SortedValues() {
		if !strings.EqualFold(ns.Subscription, sp.sub) {
			continue
		}

		if sp.rg != "" && !strings.EqualFold(ns.ResourceGroup, sp.rg) {
			continue
		}

		resources = append(resources, toNamespaceResource(ns))
	}

	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, paginate(resources))
}

func (h *Handler) checkNameAvailability(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req checkNameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}

	h.mu.RLock()
	_, taken := h.namespaces.Get(nsKey(req.Name))
	h.mu.RUnlock()

	result := checkNameResult{NameAvailable: true, Reason: "None"}
	if taken {
		result.NameAvailable = false
		result.Reason = "NameInUse"
		result.Message = "The specified name is already in use."
	}

	azurearm.WriteJSON(w, http.StatusOK, result)
}

func newRootAuthRule() *authRuleRecord {
	return &authRuleRecord{
		Name:         defaultRootRuleName,
		Rights:       []string{"Listen", "Send", "Manage"},
		PrimaryKey:   generateKey(),
		SecondaryKey: generateKey(),
	}
}

func normalizeSKU(in *sbSKU) sbSKU {
	if in == nil || in.Name == "" {
		return sbSKU{Name: "Standard", Tier: "Standard"}
	}

	out := *in
	if out.Tier == "" {
		out.Tier = out.Name
	}

	return out
}

func toNamespaceResource(ns *namespaceState) namespaceResource {
	created := ns.CreatedAt
	updated := ns.UpdatedAt
	sku := ns.SKU

	return namespaceResource{
		ID: azurearm.BuildResourceID(ns.Subscription, ns.ResourceGroup,
			providerName, resourceType, ns.Name),
		Name:     ns.Name,
		Type:     providerName + "/Namespaces",
		Location: ns.Location,
		Tags:     ns.Tags,
		SKU:      &sku,
		SystemData: &systemData{
			CreatedAt:      &created,
			CreatedByType:  "Application",
			LastModifiedAt: &updated,
		},
		Properties: namespaceProperties{
			ProvisioningState:  "Succeeded",
			Status:             "Active",
			ServiceBusEndpoint: "https://" + ns.Name + sbHost + ":443/",
			MetricID:           ns.Subscription + ":" + ns.Name,
			CreatedAt:          &created,
			UpdatedAt:          &updated,
		},
	}
}

func writeNSNotFound(w http.ResponseWriter, name string) {
	azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound",
		"namespace not found: "+name)
}

// paginate splits resources into a first page plus a nextLink placeholder when
// the collection exceeds listPageSize.
func paginate(resources []any) listResponse {
	if len(resources) <= listPageSize {
		return listResponse{Value: resources}
	}

	return listResponse{Value: resources[:listPageSize], NextLink: "cloudemu-nextpage"}
}
