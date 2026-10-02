package eventgrid

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/scope"
)

// rgPurger is the optional resource-group purge of the eventbus driver; the
// Azure eventgrid.Mock implements it.
type rgPurger interface {
	PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error
}

// deliveryRule names a system-topic subscription's rule on its delivery bus.
type deliveryRule struct{ bus, name string }

// PurgeResourceGroup deletes every topic, system topic, domain and scope-bound
// event subscription in subscription/resourceGroup, so a resource-group delete
// cascades into them. Topics live in the driver. System topics, domains,
// scoped subscriptions and topic key generations live in this handler and are
// not persisted.
func (h *Handler) PurgeResourceGroup(ctx context.Context, subscription, resourceGroup string) error {
	if p, ok := h.bus.(rgPurger); ok {
		if err := p.PurgeResourceGroup(ctx, subscription, resourceGroup); err != nil {
			return err
		}
	}

	h.mu.Lock()
	rules := h.purgeSystemTopicsLocked(subscription, resourceGroup)
	h.purgeHandlerStateLocked(subscription, resourceGroup)
	h.mu.Unlock()

	for _, r := range rules {
		h.unregisterSystemTopicSubscription(r.bus, r.name)
	}

	return nil
}

func inGroup(sub, rg, subscription, resourceGroup string) bool {
	return strings.EqualFold(sub, subscription) && strings.EqualFold(rg, resourceGroup)
}

// purgeSystemTopicsLocked drops the group's system topics and returns the
// delivery rules their subscriptions registered. The caller holds h.mu.
func (h *Handler) purgeSystemTopicsLocked(subscription, resourceGroup string) []deliveryRule {
	var rules []deliveryRule

	for k, rec := range h.systemTopics {
		if !inGroup(rec.sub, rec.rg, subscription, resourceGroup) {
			continue
		}

		for name := range rec.subscriptions {
			rules = append(rules, deliveryRule{bus: systemTopicBusName(rec.source), name: name})
		}

		delete(h.systemTopics, k)
	}

	return rules
}

// purgeHandlerStateLocked drops the group's domains, scope-bound event
// subscriptions and topic key generations. The caller holds h.mu.
func (h *Handler) purgeHandlerStateLocked(subscription, resourceGroup string) {
	for k, rec := range h.domains {
		if inGroup(rec.sub, rec.rg, subscription, resourceGroup) {
			delete(h.domains, k)
		}
	}

	for k, rec := range h.scopedSubs {
		if scope.IDInResourceGroup(rec.scope, subscription, resourceGroup) {
			delete(h.scopedSubs, k)
		}
	}

	for k := range h.topicKeyGens {
		if sub, rest, ok := strings.Cut(k, "|"); ok {
			if rg, _, ok := strings.Cut(rest, "|"); ok && inGroup(sub, rg, subscription, resourceGroup) {
				delete(h.topicKeyGens, k)
			}
		}
	}
}
