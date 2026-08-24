package sns

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/internal/eventmatch"
	"github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// rawDeliveryEnabled reports whether the subscription has RawMessageDelivery set
// to "true", in which case SNS delivers the bare message body to the endpoint.
func rawDeliveryEnabled(sub *driver.SubscriptionInfo) bool {
	return sub.Attributes["RawMessageDelivery"] == "true"
}

// subscriptionAccepts reports whether a publish satisfies the subscription's
// filter policy. A subscription with no filter policy accepts every message.
// FilterPolicyScope selects whether the policy is matched against the message
// attributes (default) or the JSON message body.
func subscriptionAccepts(sub *driver.SubscriptionInfo, input *driver.PublishInput) bool {
	raw := sub.Attributes["FilterPolicy"]
	if raw == "" {
		return true
	}

	policy, ok := eventmatch.ParsePattern(raw)
	if !ok {
		return true
	}

	if sub.Attributes["FilterPolicyScope"] == "MessageBody" {
		return matchesMessageBody(policy, input.Message)
	}

	return matchesMessageAttributes(policy, input.Attributes)
}

// matchesMessageAttributes evaluates a filter policy against the message
// attributes. Each policy key must name an attribute whose value satisfies the
// key's constraint list; a key absent from the message only matches via an
// {"exists": false} constraint.
func matchesMessageAttributes(policy map[string]any, attrs map[string]string) bool {
	for key, constraint := range policy {
		allowed, ok := constraint.([]any)
		if !ok {
			return false
		}

		value, present := attrs[key]
		if !eventmatch.MatchLeaf(allowed, value, present) {
			return false
		}
	}

	return true
}

// matchesMessageBody evaluates a filter policy against the JSON message body,
// recursing into nested body properties like an EventBridge pattern.
func matchesMessageBody(policy map[string]any, message string) bool {
	var body map[string]any
	if err := json.Unmarshal([]byte(message), &body); err != nil {
		return false
	}

	return eventmatch.MatchEvent(policy, body)
}
