package blobstorage

import "context"

// BlobFunctionTriggerSink delivers a just-written blob's content to any Azure
// Function whose function.json declares a blobTrigger input binding on the
// blob's container. The Azure Functions provider implements it. It is wired
// per Mock instance via SetFunctionTriggerSink, mirroring how
// servicebus.FunctionTriggerSink wires Queue Storage / Service Bus delivery
// (see providers/azure/servicebus) and how SetEventGridPublisher wires Blob
// Storage event emission.
type BlobFunctionTriggerSink interface {
	DeliverBlobFunctionTrigger(ctx context.Context, container, blobName string, body []byte)
}

// SetFunctionTriggerSink wires the Azure Functions provider as the
// destination for this blob surface's automatic blobTrigger deliveries: a
// successful blob create or update asks sink to invoke any function bound to
// the written blob's container. Real Azure's blobTrigger fires on blob create
// and update only, never on delete, so no delete path calls
// dispatchFunctionTrigger (see DeleteObject). A nil sink disables delivery
// (the default), so every non-Azure-Functions deployment is unaffected. This
// is the cross-service seam, analogous to Event Grid's SetEventGridPublisher.
func (m *Mock) SetFunctionTriggerSink(sink BlobFunctionTriggerSink) {
	m.functionSink = sink
}

// dispatchFunctionTrigger forwards a just-written blob to any Azure Function
// bound to its container by a blobTrigger path binding. It is a no-op when no
// sink is wired (the default). Called after the container/blob store has
// already been updated and with no lock held, mirroring emitBlobEvent: a
// function invoked by the trigger may write back into Blob Storage, and
// dispatch must never fail or block the write that already succeeded, so a
// failure loading the blob's bytes for delivery is swallowed.
func (m *Mock) dispatchFunctionTrigger(ctx context.Context, obj *blobObject, container string) {
	if m.functionSink == nil {
		return
	}

	data, err := m.loadObjectData(ctx, container, obj)
	if err != nil {
		return
	}

	m.functionSink.DeliverBlobFunctionTrigger(ctx, container, obj.Key, data)
}
