package kinesis

import "time"

// SetSubscriptionTimings shortens the SubscribeToShard lifetime and poll
// interval so tests exercise the live push without waiting real minutes.
func (h *Handler) SetSubscriptionTimings(lifetime, poll time.Duration) {
	h.subscriptionLifetime = lifetime
	h.subscriptionPoll = poll
}
