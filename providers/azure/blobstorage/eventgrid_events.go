package blobstorage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// Blob event types and the api operation names real Azure Blob Storage stamps
// on the events it emits into Event Grid.
const (
	eventTypeBlobCreated = "Microsoft.Storage.BlobCreated"
	eventTypeBlobDeleted = "Microsoft.Storage.BlobDeleted"
	blobEventAPICreate   = "PutBlob"
	blobEventAPIDelete   = "DeleteBlob"
	// blobEventAPIPutBlockList / blobEventAPICopyBlob are the api operation names
	// real Azure stamps on the BlobCreated event when a blob is written via
	// Commit Block List (large / SDK-chunked uploads) or a blob copy.
	blobEventAPIPutBlockList = "PutBlockList"
	blobEventAPICopyBlob     = "CopyBlob"
	// blobEventDataVersion is the schema version real Azure Blob Storage stamps
	// on its BlobCreated/BlobDeleted event data.
	blobEventDataVersion = "2"
	defaultBlobType      = "BlockBlob"
	storageResourceType  = "storageAccounts"
	storageProvider      = "Microsoft.Storage"
	defaultResourceGroup = "default"
)

// EventGridPublisher is the subset of the Event Grid mock the blob data plane
// uses to emit Blob Storage events. The eventgrid.Mock satisfies it via
// PutEvents, so a blob write routes through Event Grid's existing subscription
// matching + delivery pipeline rather than a parallel path.
type EventGridPublisher interface {
	PutEvents(ctx context.Context, events []ebdriver.Event) (*ebdriver.PublishResult, error)
}

// SetEventGridPublisher wires the Event Grid backend so blob writes/deletes emit
// Microsoft.Storage.BlobCreated/BlobDeleted events into the account's system
// topic. Best-effort: with no publisher set, blob operations proceed unchanged.
func (m *Mock) SetEventGridPublisher(p EventGridPublisher) {
	m.eventgrid = p
}

// blobEventFacts carries the blob-level values a Blob Storage event reports, so
// the emit helper takes one value instead of a long positional parameter list.
type blobEventFacts struct {
	container     string
	key           string
	eTag          string
	contentType   string
	contentLength int64
	blobType      string
}

// storageAccountID builds the ARM resource id of the storage account the blob
// data plane models — the "topic" real Azure stamps on a Blob Storage event
// (its source resource), distinct from the Event Grid topic the subscription
// hangs off. The resource group comes from the account's ARM attributes when it
// was created through the storage-account ARM surface, else a stable default.
func (m *Mock) storageAccountID() string {
	rg := defaultResourceGroup
	if attrs, ok := m.bucketAttrs.Get(AccountName); ok && attrs.ResourceGroup != "" {
		rg = attrs.ResourceGroup
	}

	return idgen.AzureID(m.opts.AccountID, rg, storageProvider, storageResourceType, AccountName)
}

// emitBlobCreated fires a Microsoft.Storage.BlobCreated event for a blob written
// via the Put Blob path (api=PutBlob).
func (m *Mock) emitBlobCreated(ctx context.Context, obj *blobObject, container string) {
	m.emitBlobCreatedAPI(ctx, obj, container, blobEventAPICreate)
}

// emitBlobCreatedAPI fires a Microsoft.Storage.BlobCreated event for a written
// blob, stamping the given api operation name (PutBlob / PutBlockList / CopyBlob)
// so consumers see which write path produced the blob, as real Azure does.
func (m *Mock) emitBlobCreatedAPI(ctx context.Context, obj *blobObject, container, api string) {
	m.emitBlobEvent(ctx, eventTypeBlobCreated, api, &blobEventFacts{
		container: container, key: obj.Key, eTag: obj.ETag,
		contentType: obj.ContentType, contentLength: obj.Size, blobType: obj.BlobType,
	})
}

// emitBlobDeleted fires a Microsoft.Storage.BlobDeleted event for a removed blob.
func (m *Mock) emitBlobDeleted(ctx context.Context, obj *blobObject, container string) {
	m.emitBlobEvent(ctx, eventTypeBlobDeleted, blobEventAPIDelete, &blobEventFacts{
		container: container, key: obj.Key, eTag: obj.ETag,
		contentType: obj.ContentType, contentLength: obj.Size, blobType: obj.BlobType,
	})
}

// emitBlobEvent builds a Blob Storage Event Grid event and publishes it into the
// account's system topic (a bus named for the account on the Event Grid mock).
// No-op when no publisher is wired. Publish failures are swallowed: event
// emission must never fail the blob operation, matching Azure's decoupled,
// asynchronous eventing.
func (m *Mock) emitBlobEvent(ctx context.Context, eventType, api string, f *blobEventFacts) {
	if m.eventgrid == nil {
		return
	}

	sequencer := fmt.Sprintf("%016X", m.blobEventSeq.Add(1))
	accountID := m.storageAccountID()

	event := ebdriver.Event{
		Source:      accountID,
		Topic:       accountID,
		DetailType:  eventType,
		Subject:     fmt.Sprintf("/blobServices/default/containers/%s/blobs/%s", f.container, f.key),
		Detail:      m.blobEventData(eventType, api, sequencer, f),
		DataVersion: blobEventDataVersion,
		EventBus:    AccountName,
		Time:        m.opts.Clock.Now().UTC(),
	}

	_, _ = m.eventgrid.PutEvents(ctx, []ebdriver.Event{event})
}

// blobEventData renders the "data" body of a Blob Storage event, matching real
// Azure's schema. A BlobCreated event reports eTag/contentLength; a BlobDeleted
// event omits them (the bytes are gone), the same distinction real Azure draws.
func (m *Mock) blobEventData(eventType, api, sequencer string, f *blobEventFacts) string {
	blobType := f.blobType
	if blobType == "" {
		blobType = defaultBlobType
	}

	requestID := m.blobEventRequestID(sequencer, f)

	data := map[string]any{
		"api":         api,
		"requestId":   requestID,
		"contentType": f.contentType,
		"blobType":    blobType,
		"url": fmt.Sprintf(
			"https://%s.blob.core.windows.net/%s/%s", AccountName, f.container, f.key,
		),
		"sequencer":          sequencer,
		"storageDiagnostics": map[string]any{"batchId": requestID},
	}

	if eventType == eventTypeBlobCreated {
		data["eTag"] = f.eTag
		data["contentLength"] = f.contentLength
	}

	b, err := json.Marshal(data)
	if err != nil {
		return "{}"
	}

	return string(b)
}

// blobEventRequestID derives a stable GUID-shaped request id for one blob event
// from the account id, blob subject, and the event's sequencer.
func (*Mock) blobEventRequestID(sequencer string, f *blobEventFacts) string {
	sum := sha256.Sum256([]byte(f.container + "/" + f.key + ":" + sequencer))

	return fmt.Sprintf(
		"%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16],
	)
}
