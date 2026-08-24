package eventgrid

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

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

// subscriptionKey derives the deterministic topic access keys.
func topicKey(topicName, which string) string {
	sum := sha256.Sum256([]byte("eventgrid" + "\x00" + topicName + "\x00" + which))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// listTopicKeys serves Topics.ListSharedAccessKeys.
func (h *Handler) listTopicKeys(w http.ResponseWriter, r *http.Request, rp *azurearm.ResourcePath) {
	if _, err := h.bus.GetEventBus(r.Context(), rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, topicSharedAccessKeys{
		Key1: topicKey(rp.ResourceName, "key1"),
		Key2: topicKey(rp.ResourceName, "key2"),
	})
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
