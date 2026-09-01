package eventhub

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

func (h *Handler) serveNamespace(w http.ResponseWriter, r *http.Request, ep ehPath) {
	switch r.Method {
	case http.MethodPut:
		h.createNamespace(w, r, ep)
	case http.MethodPatch:
		h.updateNamespace(w, r, ep)
	case http.MethodGet:
		h.getNamespace(w, ep)
	case http.MethodDelete:
		h.deleteNamespace(w, ep)
	default:
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

// createNamespace serves the PUT LRO. armeventhub's NamespacesClient.
// BeginCreateOrUpdate wraps this in a poller; returning a synchronous 201/200
// whose body already carries provisioningState=Succeeded (and no
// Azure-AsyncOperation/Location header) makes PollUntilDone terminate on the
// first poll, so the poller never hangs.
func (h *Handler) createNamespace(w http.ResponseWriter, r *http.Request, ep ehPath) {
	var req createNamespaceRequest
	if !decodeBody(w, r, &req) {
		return
	}

	location := req.Location
	if location == "" {
		location = "eastus"
	}

	now := time.Now().UTC()

	h.mu.Lock()

	ns, existed := h.namespaces.Get(nsKey(ep.namespace))
	if !existed {
		ns = &namespaceState{
			Name:          ep.namespace,
			Subscription:  ep.sub,
			ResourceGroup: ep.rg,
			CreatedAt:     now,
			EventHubs:     map[string]*eventHubRecord{},
			AuthRules:     map[string]*authRuleRecord{},
		}
		ns.AuthRules[defaultRootRuleName] = newRootAuthRule()
	}

	ns.Location = location
	ns.Tags = maps.Clone(req.Tags)
	ns.SKU = normalizeSKU(req.SKU)
	ns.Properties = req.Properties
	ns.UpdatedAt = now
	h.namespaces.Set(nsKey(ep.namespace), ns)

	resource := toNamespaceResource(ns)
	h.mu.Unlock()

	// ARM PUT of a new resource returns 201 Created; an in-place update of an
	// existing one returns 200.
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}

	azurearm.WriteJSON(w, status, resource)
}

func (h *Handler) updateNamespace(w http.ResponseWriter, r *http.Request, ep ehPath) {
	var req createNamespaceRequest
	if !decodeBody(w, r, &req) {
		return
	}

	h.mu.Lock()

	ns, ok := h.getNS(ep)
	if !ok {
		h.mu.Unlock()
		writeNSNotFound(w, ep.namespace)

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

func (h *Handler) getNamespace(w http.ResponseWriter, ep ehPath) {
	h.mu.RLock()

	ns, ok := h.getNS(ep)
	if !ok {
		h.mu.RUnlock()
		writeNSNotFound(w, ep.namespace)

		return
	}

	resource := toNamespaceResource(ns)
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, resource)
}

// deleteNamespace serves the DELETE LRO. A synchronous 200 (deleted) or 204
// (already absent) with no async header makes armeventhub's BeginDelete poller
// terminate on the first poll. The cascade is implicit: the namespace state
// owns every child event hub, consumer group and authorization rule.
func (h *Handler) deleteNamespace(w http.ResponseWriter, ep ehPath) {
	h.mu.Lock()

	if _, ok := h.getNS(ep); !ok {
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

		return
	}

	h.namespaces.Delete(nsKey(ep.namespace))
	h.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listNamespaces(w http.ResponseWriter, r *http.Request, ep ehPath) {
	if r.Method != http.MethodGet {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	h.mu.RLock()

	resources := make([]any, 0)

	for _, ns := range h.namespaces.SortedValues() {
		if !strings.EqualFold(ns.Subscription, ep.sub) {
			continue
		}

		if ep.rg != "" && !strings.EqualFold(ns.ResourceGroup, ep.rg) {
			continue
		}

		resources = append(resources, toNamespaceResource(ns))
	}

	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, paginate(r, resources))
}

func (h *Handler) checkNameAvailability(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		azurearm.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}

	var req checkNameRequest
	if !decodeBody(w, r, &req) {
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

func normalizeSKU(in *ehSKU) ehSKU {
	if in == nil || in.Name == "" {
		return ehSKU{Name: "Standard", Tier: "Standard"}
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

	props := ns.Properties
	props.ProvisioningState = "Succeeded"
	props.Status = statusActive
	props.ServiceBusEndpoint = "https://" + ns.Name + ehHost + ":443/"
	props.MetricID = ns.Subscription + ":" + ns.Name
	props.CreatedAt = &created
	props.UpdatedAt = &updated

	return namespaceResource{
		ID:       azurearm.BuildResourceID(ns.Subscription, ns.ResourceGroup, providerName, resourceType, ns.Name),
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
