package eventgrid

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

const (
	domainResourceType        = "Microsoft.EventGrid/domains"
	domainProvisioningState   = "Succeeded"
	domainDefaultInputSchema  = "EventGridSchema"
	domainDefaultNetworkAcces = "Enabled"
	subActionListKeys         = "listKeys"
	subActionRegenerateKey    = "regenerateKey"
	// keyName1 and keyName2 are the two shared-access-key identifiers a topic
	// or domain exposes, used both to derive keys and to route RegenerateKey.
	keyName1 = "key1"
	keyName2 = "key2"
)

// domainRecord is the wire-handler-owned state for one Event Grid domain. A
// domain groups topics under a single publishing endpoint and owns two shared
// access keys that RegenerateKey rotates; none of that maps onto the generic
// eventbus driver, so the wire handler holds it (mirroring the Cosmos /offers
// precedent).
type domainRecord struct {
	name     string
	location string
	sub      string
	rg       string
	tags     map[string]string
	key1     string
	key2     string
	// inputSchema is fixed at creation (immutable, like a topic's);
	// publicNetworkAccess is mutable across CreateOrUpdate calls.
	inputSchema         string
	publicNetworkAccess string
	// topics holds the DomainTopics sub-resources created under this domain.
	// A domain topic carries no state of its own beyond existing (real Event
	// Grid domain topics are typically auto-created on first publish and
	// exist only to route to per-topic subscriptions), so presence in this
	// set is all CRUD needs to track.
	topics map[string]struct{}
}

type domainJSON struct {
	ID         string             `json:"id,omitempty"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Location   string             `json:"location"`
	Tags       map[string]*string `json:"tags,omitempty"`
	Properties *domainProperties  `json:"properties,omitempty"`
}

type domainProperties struct {
	Endpoint            string `json:"endpoint,omitempty"`
	MetricResourceID    string `json:"metricResourceId,omitempty"`
	ProvisioningState   string `json:"provisioningState,omitempty"`
	InputSchema         string `json:"inputSchema,omitempty"`
	PublicNetworkAccess string `json:"publicNetworkAccess,omitempty"`
}

type domainListResult struct {
	Value []domainJSON `json:"value"`
}

// domainSharedAccessKeys is the ListSharedAccessKeys / RegenerateKey response
// (armeventgrid.DomainSharedAccessKeys).
type domainSharedAccessKeys struct {
	Key1 string `json:"key1"`
	Key2 string `json:"key2"`
}

type domainRegenerateKeyRequest struct {
	KeyName string `json:"keyName"`
}

// domainKey derives a deterministic shared access key. gen bumps on each
// regeneration so a rotated key differs from its predecessor.
func domainKey(name, which string, gen int) string {
	sum := sha256.Sum256([]byte("eventgrid-domain\x00" + name + "\x00" + which + "\x00" + strconv.Itoa(gen)))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func domainID(rp *azurearm.ResourcePath) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeDomains, rp.ResourceName)
}

// orDefault returns v, or def when v is empty.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

// domainInputSchemaFromBody reads the caller's requested InputSchema off a
// Domains CreateOrUpdate body (empty when properties are omitted).
func domainInputSchemaFromBody(body *domainJSON) string {
	if body.Properties == nil {
		return ""
	}

	return body.Properties.InputSchema
}

// domainPublicNetworkAccessFromBody reads the caller's requested
// PublicNetworkAccess off a Domains CreateOrUpdate body (empty when omitted).
func domainPublicNetworkAccessFromBody(body *domainJSON) string {
	if body.Properties == nil {
		return ""
	}

	return body.Properties.PublicNetworkAccess
}

func (rec *domainRecord) toJSON(rp *azurearm.ResourcePath) domainJSON {
	id := domainID(rp)

	return domainJSON{
		ID:       id,
		Name:     rec.name,
		Type:     domainResourceType,
		Location: rec.location,
		Tags:     tagsToPtr(rec.tags),
		Properties: &domainProperties{
			Endpoint:            topicEndpoint(rec.name, rec.location),
			MetricResourceID:    id,
			ProvisioningState:   domainProvisioningState,
			InputSchema:         orDefault(rec.inputSchema, domainDefaultInputSchema),
			PublicNetworkAccess: orDefault(rec.publicNetworkAccess, domainDefaultNetworkAcces),
		},
	}
}

// serveDomains routes .../domains[/{name}[/{listKeys|regenerateKey}]].
func (h *Handler) serveDomains(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if rp.ResourceName == "" {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w)
			return
		}

		h.listDomains(w, rp)

		return
	}

	switch rp.SubResource {
	case "":
		h.serveDomainResource(w, r, rp)
	case subActionListKeys:
		h.listDomainKeys(w, r, rp)
	case subActionRegenerateKey:
		h.regenerateDomainKey(w, r, rp)
	case typeTopics:
		// domains/{domain}/topics[/{topicName}] — DomainTopics, a distinct
		// sub-resource from the top-level Microsoft.EventGrid/topics
		// collection sharing the same "topics" path segment.
		h.serveDomainTopics(w, r, rp)
	default:
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "unsupported domain sub-resource")
	}
}

func (h *Handler) serveDomainResource(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	switch r.Method {
	case http.MethodPut:
		h.createOrUpdateDomain(w, r, rp)
	case http.MethodPatch:
		h.updateDomain(w, r, rp)
	case http.MethodGet:
		h.getDomain(w, rp)
	case http.MethodDelete:
		h.deleteDomain(w, rp)
	default:
		writeMethodNotAllowed(w)
	}
}

func (h *Handler) createOrUpdateDomain(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body domainJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	loc := body.Location
	if loc == "" {
		loc = defaultSystemTopicLocation
	}

	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.Lock()

	rec := h.domains[key]
	if rec == nil {
		rec = &domainRecord{
			name:   rp.ResourceName,
			sub:    rp.Subscription,
			rg:     rp.ResourceGroup,
			key1:   domainKey(rp.ResourceName, keyName1, 0),
			key2:   domainKey(rp.ResourceName, keyName2, 0),
			topics: make(map[string]struct{}),
			// inputSchema is immutable, so it is stamped only on creation.
			inputSchema: orDefault(domainInputSchemaFromBody(&body), domainDefaultInputSchema),
		}
		h.domains[key] = rec
	}

	rec.location = loc
	rec.tags = tagsFromPtr(body.Tags)

	if pna := domainPublicNetworkAccessFromBody(&body); pna != "" {
		rec.publicNetworkAccess = pna
	}

	out := rec.toJSON(rp)
	h.mu.Unlock()

	// Domains.CreateOrUpdate accepts only 201; the terminal provisioningState
	// completes the SDK's LRO poller on the first response.
	azurearm.WriteJSON(w, http.StatusCreated, out)
}

// domainUpdateJSON is the Domains.Update (PATCH) request body: mutable tags plus
// mutable properties (publicNetworkAccess). Input schema is immutable, matching
// real Event Grid.
type domainUpdateJSON struct {
	Tags       map[string]*string `json:"tags,omitempty"`
	Properties *struct {
		PublicNetworkAccess string `json:"publicNetworkAccess,omitempty"`
	} `json:"properties,omitempty"`
}

// updateDomain maps Domains.Update (PATCH) onto the wire-owned record: it merges
// the supplied tags onto the existing tags and applies the mutable
// publicNetworkAccess, preserving anything the caller omitted, and returns the
// updated domain (200). 404 when the domain does not exist, before any write.
func (h *Handler) updateDomain(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body domainUpdateJSON
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.Lock()

	rec := h.domains[key]
	if rec == nil {
		h.mu.Unlock()
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "domain not found")

		return
	}

	if body.Tags != nil {
		rec.tags = mergeTags(rec.tags, tagsFromPtr(body.Tags))
	}

	if body.Properties != nil && body.Properties.PublicNetworkAccess != "" {
		rec.publicNetworkAccess = body.Properties.PublicNetworkAccess
	}

	out := rec.toJSON(rp)
	h.mu.Unlock()

	// 201 with a terminal provisioningState completes the SDK's Update LRO
	// poller on the first response (the poller accepts 200 or 201).
	azurearm.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) getDomain(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.RLock()
	rec := h.domains[key]

	if rec == nil {
		h.mu.RUnlock()
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "domain not found")

		return
	}

	out := rec.toJSON(rp)
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) deleteDomain(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.Lock()
	_, found := h.domains[key]
	delete(h.domains, key)
	h.mu.Unlock()

	if !found {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listDomains(w http.ResponseWriter, rp *azurearm.ResourcePath) {
	h.mu.RLock()
	recs := recordsInScope(h.domains, rp.Subscription, rp.ResourceGroup)
	out := scopedList(recs, rp, (*domainRecord).toJSON)
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, domainListResult{Value: out})
}

func (h *Handler) listDomainKeys(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.RLock()
	rec := h.domains[key]

	if rec == nil {
		h.mu.RUnlock()
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "domain not found")

		return
	}

	keys := domainSharedAccessKeys{Key1: rec.key1, Key2: rec.key2}
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, keys)
}

func (h *Handler) regenerateDomainKey(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}

	var body domainRegenerateKeyRequest
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	h.mu.Lock()

	rec := h.domains[key]
	if rec == nil {
		h.mu.Unlock()
		azurearm.WriteError(w, http.StatusNotFound, "ResourceNotFound", "domain not found")

		return
	}

	switch body.KeyName {
	case keyName1:
		rec.key1 = domainKey(rec.name, keyName1, nextKeyGen(rec.key1))
	case keyName2:
		rec.key2 = domainKey(rec.name, keyName2, nextKeyGen(rec.key2))
	default:
		h.mu.Unlock()
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "keyName must be key1 or key2")

		return
	}

	keys := domainSharedAccessKeys{Key1: rec.key1, Key2: rec.key2}
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, keys)
}

// nextKeyGen produces a fresh generation counter for a rotated key. The prior
// key's bytes seed it so consecutive regenerations keep yielding new values.
func nextKeyGen(prev string) int {
	sum := sha256.Sum256([]byte(prev))
	// Fold a few bytes into a positive int; the exact value only needs to change.
	return int(sum[0])<<16 | int(sum[1])<<8 | int(sum[2]) | 1
}
