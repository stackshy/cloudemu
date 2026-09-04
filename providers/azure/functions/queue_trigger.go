package functions

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/recursionguard"
)

// DeliverFunctionTrigger invokes every deployed function whose function.json
// declares an input trigger binding of bindingType (queueTrigger for Queue
// Storage, serviceBusTrigger for Service Bus) bound to queueName, passing body
// as the invocation payload. It is the cross-service seam the Azure Queue
// Storage / Service Bus mocks call when a message is enqueued, mirroring how
// Event Grid delivery calls InvokeExternal.
//
// Delivery is synchronous and recursion-guarded: a function that re-enqueues to
// its own trigger queue would otherwise invoke itself unbounded. InvokeExternal
// increments the ctx depth per invocation and drops once recursionguard.MaxDepth
// is reached; the early check here also short-circuits the binding scan.
func (m *Mock) DeliverFunctionTrigger(ctx context.Context, bindingType, queueName string, body []byte) {
	if bindingType == "" || queueName == "" {
		return
	}

	if recursionguard.Depth(ctx) >= recursionguard.MaxDepth {
		return
	}

	match := func(b map[string]any) bool {
		qn, _ := b["queueName"].(string)
		return qn == queueName
	}

	for _, app := range m.appsBoundBy(bindingType, match) {
		_ = m.InvokeExternal(ctx, app, body)
	}
}

// DeliverTopicFunctionTrigger invokes every deployed function whose
// function.json declares a serviceBusTrigger binding on (topicName,
// subscriptionName), passing body as the invocation payload. It is the
// topic/subscription counterpart of DeliverFunctionTrigger: a message
// published to a Service Bus topic is fanned out to each subscription, and a
// function bound to one subscription fires once per message that subscription
// receives. See DeliverFunctionTrigger for delivery semantics (synchronous,
// recursion-guarded).
func (m *Mock) DeliverTopicFunctionTrigger(ctx context.Context, bindingType, topicName, subscriptionName string, body []byte) {
	if bindingType == "" || topicName == "" || subscriptionName == "" {
		return
	}

	if recursionguard.Depth(ctx) >= recursionguard.MaxDepth {
		return
	}

	match := func(b map[string]any) bool {
		tn, _ := b["topicName"].(string)
		sn, _ := b["subscriptionName"].(string)

		return tn == topicName && sn == subscriptionName
	}

	for _, app := range m.appsBoundBy(bindingType, match) {
		_ = m.InvokeExternal(ctx, app, body)
	}
}

// appsBoundBy returns, sorted for deterministic delivery order, the names of
// the function apps that have at least one deployed, non-disabled function
// whose bindings include an input trigger of bindingType satisfying match.
func (m *Mock) appsBoundBy(bindingType string, match func(b map[string]any) bool) []string {
	m.sitesMu.RLock()
	defer m.sitesMu.RUnlock()

	var apps []string

	for _, meta := range m.sites.All() {
		if siteHasTrigger(meta, bindingType, match) {
			apps = append(apps, meta.Name)
		}
	}

	sort.Strings(apps)

	return apps
}

// siteHasTrigger reports whether any non-disabled function of the site
// declares a matching trigger binding.
func siteHasTrigger(meta *SiteMeta, bindingType string, match func(b map[string]any) bool) bool {
	for _, fn := range meta.Functions {
		if fn.IsDisabled {
			continue
		}

		if bindingMatches(fn.Config, bindingType, match) {
			return true
		}
	}

	return false
}

// bindingMatches reports whether a deployed function's function.json config
// declares an input trigger of bindingType satisfying match. Azure discovers
// these bindings from the deployed code; here they arrive verbatim in the ARM
// CreateFunction body's "config" object.
func bindingMatches(config map[string]any, bindingType string, match func(b map[string]any) bool) bool {
	bindings, ok := config["bindings"].([]any)
	if !ok {
		return false
	}

	for _, raw := range bindings {
		b, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		if t, _ := b["type"].(string); t != bindingType {
			continue
		}

		// A *Trigger binding is direction "in"; treat an omitted direction as "in"
		// (trigger bindings always are) and skip an explicit output binding.
		if dir, ok := b["direction"].(string); ok && dir != "in" {
			continue
		}

		if match(b) {
			return true
		}
	}

	return false
}
