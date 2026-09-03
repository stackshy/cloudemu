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

	for _, app := range m.appsBoundToQueue(bindingType, queueName) {
		_ = m.InvokeExternal(ctx, app, body)
	}
}

// appsBoundToQueue returns, sorted for deterministic delivery order, the names of
// the function apps that have at least one deployed function whose bindings
// include a matching input trigger.
func (m *Mock) appsBoundToQueue(bindingType, queueName string) []string {
	m.sitesMu.RLock()
	defer m.sitesMu.RUnlock()

	var apps []string

	for _, meta := range m.sites.All() {
		if siteHasQueueTrigger(meta, bindingType, queueName) {
			apps = append(apps, meta.Name)
		}
	}

	sort.Strings(apps)

	return apps
}

// siteHasQueueTrigger reports whether any non-disabled function of the site
// declares a matching trigger binding.
func siteHasQueueTrigger(meta *SiteMeta, bindingType, queueName string) bool {
	for _, fn := range meta.Functions {
		if fn.IsDisabled {
			continue
		}

		if bindingMatchesQueue(fn.Config, bindingType, queueName) {
			return true
		}
	}

	return false
}

// bindingMatchesQueue reports whether a deployed function's function.json config
// declares an input trigger of bindingType bound to queueName. Azure discovers
// these bindings from the deployed code; here they arrive verbatim in the ARM
// CreateFunction body's "config" object.
func bindingMatchesQueue(config map[string]any, bindingType, queueName string) bool {
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

		if qn, _ := b["queueName"].(string); qn == queueName {
			return true
		}
	}

	return false
}
