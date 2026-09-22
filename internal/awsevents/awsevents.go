// Package awsevents is the shared seam through which AWS service mocks emit
// their native lifecycle events (e.g. "EC2 Instance State-change Notification")
// onto the account's default EventBridge event bus, the way real AWS services
// publish them automatically.
//
// It mirrors the SetMonitoring cross-service wiring: each emitting service
// embeds an Emitter, the provider factory wires the EventBridge mock into it
// via the service's SetEventPublisher, and an Emitter left unwired is a no-op
// so a service constructed standalone behaves exactly as before.
package awsevents

import "context"

// Publisher puts a service-originated event on the default event bus, building
// the real EventBridge envelope (version, id, detail-type, source, account,
// time, region, resources, detail) and routing it through rule matching. The
// AWS EventBridge mock satisfies it.
type Publisher interface {
	PublishServiceEvent(ctx context.Context, source, detailType string, detail any, resources []string)
}

// Emitter is the nil-safe holder a service mock embeds. Its zero value is
// inactive: Emit does nothing until SetPublisher wires a Publisher.
//
// Like the other provider seams it is wired once by the provider factory before
// the mock serves requests, so the field needs no lock of its own.
type Emitter struct {
	publisher Publisher
}

// SetPublisher wires the event bus the emitter publishes to. Passing nil
// disables emission.
func (e *Emitter) SetPublisher(p Publisher) {
	e.publisher = p
}

// Emit publishes one lifecycle event. It is a no-op when no publisher is wired.
// Callers must not hold their own service locks while emitting: delivery runs
// matched rule targets synchronously, and a target may call back into the
// emitting service.
func (e *Emitter) Emit(ctx context.Context, source, detailType string, detail any, resources ...string) {
	if e == nil || e.publisher == nil {
		return
	}

	e.publisher.PublishServiceEvent(ctx, source, detailType, detail, resources)
}
