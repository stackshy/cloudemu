package kinesis

import "time"

// SetSubscriptionTimings shortens the SubscribeToShard lifetime and poll
// interval so tests exercise the live push without waiting real minutes.
func (h *Handler) SetSubscriptionTimings(lifetime, poll time.Duration) {
	h.subscriptionLifetime = lifetime
	h.subscriptionPoll = poll
}

// SetSubscriptionHeartbeat shortens how long an idle subscription waits before
// pushing an empty event.
func (h *Handler) SetSubscriptionHeartbeat(d time.Duration) {
	h.subscriptionHeartbeat = d
}
