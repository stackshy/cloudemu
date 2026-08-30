package servicebus

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"strconv"
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

	// ARM PUT of a new resource returns 201 Created; an in-place update of an
	// existing one returns 200.
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}

	azurearm.WriteJSON(w, status, resource)
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

func (h *Handler) listNamespaces(w http.ResponseWriter, r *http.Request, sp sbPath) {
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

	azurearm.WriteJSON(w, http.StatusOK, paginate(r, resources))
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

// paginate returns the listPageSize-sized window of resources that starts at the
// request's $skip offset. When more items remain it emits a nextLink — an
// absolute URL that repeats the request with $skip advanced — that armservicebus
// pagers follow until the collection is exhausted. A collection that fits a
// single page (skip 0, len <= listPageSize) returns no nextLink.
func paginate(r *http.Request, resources []any) listResponse {
	skip := paginationSkip(r)
	if skip >= len(resources) {
		return listResponse{Value: []any{}}
	}

	end := skip + listPageSize
	if end >= len(resources) {
		return listResponse{Value: resources[skip:]}
	}

	return listResponse{Value: resources[skip:end], NextLink: nextPageLink(r, end)}
}

// paginationSkip reads the $skip offset a follow-up page request carries. A
// missing or malformed value pages from the start.
func paginationSkip(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get(skipParam))
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// nextPageLink builds the absolute URL that continues a listing at offset skip,
// preserving the request path and query (api-version included) and overriding
// $skip. armservicebus pagers GET this URL verbatim, so it must carry scheme and
// host — a server request URL has neither.
func nextPageLink(r *http.Request, skip int) string {
	next := *r.URL
	next.Host = r.Host

	next.Scheme = "http"
	if r.TLS != nil {
		next.Scheme = "https"
	}

	q := next.Query()
	q.Set(skipParam, strconv.Itoa(skip))
	next.RawQuery = q.Encode()

	return next.String()
}
