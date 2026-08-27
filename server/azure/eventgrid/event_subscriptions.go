package eventgrid

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// topicRegenerateKeyRequest is the Topics.RegenerateKey request body
// (armeventgrid.TopicRegenerateKeyRequest).
type topicRegenerateKeyRequest struct {
	KeyName string `json:"keyName"`
}

// eventSubscriptionJSON is the ARM EventSubscription resource shape. The
// properties are stored and echoed verbatim so the caller's destination/filter
// round-trip losslessly through the eventbus driver's rule model.
type eventSubscriptionJSON struct {
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties,omitempty"`
}

type eventSubscriptionListResult struct {
	Value []eventSubscriptionJSON `json:"value"`
}

// topicKey derives a deterministic topic shared access key. gen bumps on each
// regeneration so a rotated key differs from its predecessor.
func topicKey(topicName, which string, gen int) string {
	sum := sha256.Sum256([]byte("eventgrid" + "\x00" + topicName + "\x00" + which + "\x00" + strconv.Itoa(gen)))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// topicKeysFor returns the current key1/key2 for a topic at their present
// generations. The caller must hold at least a read lock.
func (h *Handler) topicKeysFor(rp *azurearm.ResourcePath) topicSharedAccessKeys {
	gens := h.topicKeyGens[storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)]

	g1, g2 := 0, 0
	if gens != nil {
		g1, g2 = gens.key1Gen, gens.key2Gen
	}

	return topicSharedAccessKeys{
		Key1: topicKey(rp.ResourceName, keyName1, g1),
		Key2: topicKey(rp.ResourceName, keyName2, g2),
	}
}

// listTopicKeys serves Topics.ListSharedAccessKeys.
func (h *Handler) listTopicKeys(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, err := h.bus.GetEventBus(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	h.mu.RLock()
	keys := h.topicKeysFor(rp)
	h.mu.RUnlock()

	azurearm.WriteJSON(w, http.StatusOK, keys)
}

// regenerateTopicKey serves Topics.RegenerateKey: it bumps the requested key's
// generation (leaving the other untouched) and returns the refreshed keys.
func (h *Handler) regenerateTopicKey(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, err := h.bus.GetEventBus(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var body topicRegenerateKeyRequest
	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	h.mu.Lock()

	key := storeKey(rp.Subscription, rp.ResourceGroup, rp.ResourceName)

	gens := h.topicKeyGens[key]
	if gens == nil {
		gens = &topicKeyGens{}
		h.topicKeyGens[key] = gens
	}

	switch body.KeyName {
	case keyName1:
		gens.key1Gen++
	case keyName2:
		gens.key2Gen++
	default:
		h.mu.Unlock()
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidParameter", "keyName must be key1 or key2")

		return
	}

	keys := h.topicKeysFor(rp)
	h.mu.Unlock()

	azurearm.WriteJSON(w, http.StatusOK, keys)
}

// enrichSubscriptionProperties parses stored properties, stamps the read-only
// topic id and provisioning state, and re-marshals. When props is empty a
// minimal object with just those read-only fields is produced.
func enrichSubscriptionProperties(props []byte, topicID string) json.RawMessage {
	obj := map[string]any{}
	if len(props) > 0 {
		_ = json.Unmarshal(props, &obj)
	}

	obj["topic"] = topicID
	obj["provisioningState"] = subscriptionProvisionedGood

	out, err := json.Marshal(obj)
	if err != nil {
		return props
	}

	return out
}

func subscriptionID(rp *azurearm.ResourcePath) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeTopics, rp.ResourceName) +
		"/" + subEventSubscriptions + "/" + rp.SubResourceName
}

func topicID(rp *azurearm.ResourcePath) string {
	return azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, typeTopics, rp.ResourceName)
}

func toEventSubscriptionJSON(rp *azurearm.ResourcePath, rule *ebdriver.Rule) eventSubscriptionJSON {
	return eventSubscriptionJSON{
		ID:         subscriptionID(rp),
		Name:       rp.SubResourceName,
		Type:       subscriptionResourceType,
		Properties: enrichSubscriptionProperties([]byte(rule.Description), topicID(rp)),
	}
}

// createOrUpdateEventSubscription maps TopicEventSubscriptions.CreateOrUpdate
// onto the eventbus driver's rule model, stashing the raw properties JSON so it
// round-trips unchanged.
func (h *Handler) createOrUpdateEventSubscription(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	var body struct {
		Properties json.RawMessage `json:"properties"`
	}

	if !azurearm.DecodeJSON(w, r, &body) {
		return
	}

	cfg := &ebdriver.RuleConfig{
		Name:        rp.SubResourceName,
		EventBus:    rp.ResourceName,
		Description: string(body.Properties),
		State:       "ENABLED",
	}

	rule, err := h.bus.PutRule(r.Context(), cfg)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusCreated, toEventSubscriptionJSON(rp, rule))
}

// mergeRawProperties overlays the top-level keys of patch onto existing,
// returning the merged properties JSON. Keys absent from patch are preserved
// (partial-merge PATCH semantics — no nil-mask data loss); keys present in patch
// replace their prior value. An empty patch leaves existing untouched. This
// covers the EventSubscriptionUpdateParameters fields (destination, filter,
// labels, deadLetterDestination, retryPolicy, eventDeliverySchema) without
// enumerating them, and preserves any unknown fields the caller round-trips.
func mergeRawProperties(existing, patch json.RawMessage) json.RawMessage {
	if len(patch) == 0 {
		return existing
	}

	base := map[string]json.RawMessage{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &base)
	}

	over := map[string]json.RawMessage{}
	if err := json.Unmarshal(patch, &over); err != nil {
		return existing
	}

	for k, v := range over {
		base[k] = v
	}

	out, err := json.Marshal(base)
	if err != nil {
		return existing
	}

	return out
}

// updateEventSubscription maps TopicEventSubscriptions.Update (PATCH) onto the
// rule model: it merges the supplied fields onto the stored properties,
// preserving fields the caller omitted, and re-stores the subscription (200).
// The SDK's EventSubscriptionUpdateParameters marshals its fields (destination,
// filter, labels, deadLetterDestination, retryPolicy, eventDeliverySchema) at
// the top level — no "properties" wrapper — matching the shape stored for the
// subscription, so the whole body is merged. 404 when the subscription does not
// exist, before any write.
func (h *Handler) updateEventSubscription(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	existing, err := h.bus.GetRule(r.Context(), rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	var patch json.RawMessage
	if !azurearm.DecodeJSON(w, r, &patch) {
		return
	}

	merged := mergeRawProperties(json.RawMessage(existing.Description), patch)

	cfg := &ebdriver.RuleConfig{
		Name:        rp.SubResourceName,
		EventBus:    rp.ResourceName,
		Description: string(merged),
		State:       existing.State,
	}

	rule, perr := h.bus.PutRule(r.Context(), cfg)
	if perr != nil {
		azurearm.WriteCErr(w, perr)
		return
	}

	// The armeventgrid Update LRO poller accepts only 201 for event
	// subscriptions; the terminal provisioningState completes it inline.
	azurearm.WriteJSON(w, http.StatusCreated, toEventSubscriptionJSON(rp, rule))
}

func (h *Handler) getEventSubscription(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	rule, err := h.bus.GetRule(r.Context(), rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toEventSubscriptionJSON(rp, rule))
}

// deleteEventSubscription removes the subscription. The SDK's BeginDelete LRO
// completes on a 200 first response.
func (h *Handler) deleteEventSubscription(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if err := h.bus.DeleteRule(r.Context(), rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) listEventSubscriptions(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	rules, err := h.bus.ListRules(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]eventSubscriptionJSON, 0, len(rules))
	for i := range rules {
		sub := *rp
		sub.SubResourceName = rules[i].Name
		out = append(out, toEventSubscriptionJSON(&sub, &rules[i]))
	}

	azurearm.WriteJSON(w, http.StatusOK, eventSubscriptionListResult{Value: out})
}
