package blobstorage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/eventgrid"
	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deliveredEvent mirrors the Event Grid schema envelope POSTed to a WebHook
// subscriber, decoded from the blob event the producer emits.
type deliveredEvent struct {
	ID          string          `json:"id"`
	Topic       string          `json:"topic"`
	Subject     string          `json:"subject"`
	EventType   string          `json:"eventType"`
	Data        json.RawMessage `json:"data"`
	DataVersion string          `json:"dataVersion"`
}

// blobWebhookReceiver records every Event Grid batch delivered to it.
type blobWebhookReceiver struct {
	*httptest.Server

	mu    sync.Mutex
	calls []deliveredEvent
}

func newBlobWebhookReceiver(t *testing.T) *blobWebhookReceiver {
	t.Helper()

	rc := &blobWebhookReceiver{}
	rc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch []deliveredEvent
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		rc.mu.Lock()
		rc.calls = append(rc.calls, batch...)
		rc.mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(rc.Close)

	return rc
}

func (rc *blobWebhookReceiver) events() []deliveredEvent {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	out := make([]deliveredEvent, len(rc.calls))
	copy(out, rc.calls)

	return out
}

// blobSubscriptionProps builds the raw ARM EventSubscription properties JSON a
// system-topic subscription carries: an optional filter plus a WebHook
// destination pointing at the receiver.
func blobSubscriptionProps(endpointURL string, filter map[string]any) string {
	props := map[string]any{
		"destination": map[string]any{
			"endpointType": "WebHook",
			"properties":   map[string]any{"endpointUrl": endpointURL},
		},
	}
	if filter != nil {
		props["filter"] = filter
	}

	b, _ := json.Marshal(props)

	return string(b)
}

// newWiredMocks builds a blob mock wired to an Event Grid mock that share a
// clock, plus a system topic (a bus named for the storage account) that blob
// events route to.
func newWiredMocks(t *testing.T) (*Mock, *eventgrid.Mock) {
	t.Helper()

	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithRegion("eastus"))

	eg := eventgrid.New(opts)
	bm := New(opts)
	bm.SetEventGridPublisher(eg)

	_, err := eg.CreateEventBus(context.Background(), ebdriver.EventBusConfig{Name: AccountName})
	require.NoError(t, err)

	return bm, eg
}

func subscribe(t *testing.T, eg *eventgrid.Mock, name, url string, filter map[string]any) {
	t.Helper()

	_, err := eg.PutRule(context.Background(), &ebdriver.RuleConfig{
		Name:        name,
		EventBus:    AccountName,
		Description: blobSubscriptionProps(url, filter),
	})
	require.NoError(t, err)
}

// TestBlobCreatedEmitsToSubscriber covers case (a): a PUT delivers a
// Microsoft.Storage.BlobCreated event to a system-topic subscriber with the
// correct subject and data.
func TestBlobCreatedEmitsToSubscriber(t *testing.T) {
	receiver := newBlobWebhookReceiver(t)
	bm, eg := newWiredMocks(t)
	ctx := context.Background()

	subscribe(t, eg, "sub1", receiver.URL, map[string]any{
		"includedEventTypes": []string{eventTypeBlobCreated},
	})

	require.NoError(t, bm.CreateBucket(ctx, "images"))
	require.NoError(t, bm.PutObject(ctx, "images", "cat.png", []byte("hello"), "image/png", nil))

	got := receiver.events()
	require.Len(t, got, 1)
	assert.Equal(t, eventTypeBlobCreated, got[0].EventType)
	assert.Equal(t, "/blobServices/default/containers/images/blobs/cat.png", got[0].Subject)
	assert.Equal(t, blobEventDataVersion, got[0].DataVersion)

	var data map[string]any
	require.NoError(t, json.Unmarshal(got[0].Data, &data))
	assert.Equal(t, blobEventAPICreate, data["api"])
	assert.Equal(t, "BlockBlob", data["blobType"])
	assert.Equal(t, "image/png", data["contentType"])
	assert.EqualValues(t, 5, data["contentLength"])
	assert.Equal(t, "https://cloudemu.blob.core.windows.net/images/cat.png", data["url"])
	assert.NotEmpty(t, data["eTag"])
	assert.NotEmpty(t, data["sequencer"])
	// topic is the storage account's ARM id (the source resource), not the Event
	// Grid topic the subscription hangs off.
	assert.Contains(t, got[0].Topic, "/providers/Microsoft.Storage/storageAccounts/cloudemu")
}

// TestBlobDeletedEmitsToSubscriber covers case (b): a DELETE delivers a
// Microsoft.Storage.BlobDeleted event.
func TestBlobDeletedEmitsToSubscriber(t *testing.T) {
	receiver := newBlobWebhookReceiver(t)
	bm, eg := newWiredMocks(t)
	ctx := context.Background()

	subscribe(t, eg, "sub1", receiver.URL, map[string]any{
		"includedEventTypes": []string{eventTypeBlobDeleted},
	})

	require.NoError(t, bm.CreateBucket(ctx, "docs"))
	require.NoError(t, bm.PutObject(ctx, "docs", "a.txt", []byte("hi"), "text/plain", nil))
	require.NoError(t, bm.DeleteObject(ctx, "docs", "a.txt"))

	got := receiver.events()
	require.Len(t, got, 1)
	assert.Equal(t, eventTypeBlobDeleted, got[0].EventType)
	assert.Equal(t, "/blobServices/default/containers/docs/blobs/a.txt", got[0].Subject)

	var data map[string]any
	require.NoError(t, json.Unmarshal(got[0].Data, &data))
	assert.Equal(t, blobEventAPIDelete, data["api"])
	// A delete omits eTag/contentLength (the bytes are gone), matching real Azure.
	assert.NotContains(t, data, "eTag")
	assert.NotContains(t, data, "contentLength")
}

// TestBlobEventSubjectPrefixFilterDropsOtherContainers covers case (c): a
// subscription filtering on a container path prefix drops events from other
// containers.
func TestBlobEventSubjectPrefixFilterDropsOtherContainers(t *testing.T) {
	receiver := newBlobWebhookReceiver(t)
	bm, eg := newWiredMocks(t)
	ctx := context.Background()

	subscribe(t, eg, "sub1", receiver.URL, map[string]any{
		"subjectBeginsWith": "/blobServices/default/containers/wanted/",
	})

	require.NoError(t, bm.CreateBucket(ctx, "wanted"))
	require.NoError(t, bm.CreateBucket(ctx, "other"))
	require.NoError(t, bm.PutObject(ctx, "other", "skip.txt", []byte("x"), "text/plain", nil))
	require.NoError(t, bm.PutObject(ctx, "wanted", "keep.txt", []byte("y"), "text/plain", nil))

	got := receiver.events()
	require.Len(t, got, 1)
	assert.Equal(t, "/blobServices/default/containers/wanted/blobs/keep.txt", got[0].Subject)
}

// TestBlobWriteSucceedsWithoutPublisher covers case (d): with no Event Grid
// publisher wired, blob writes and deletes still succeed (no panic).
func TestBlobWriteSucceedsWithoutPublisher(t *testing.T) {
	bm := newTestMock() // no SetEventGridPublisher
	ctx := context.Background()

	require.NoError(t, bm.CreateBucket(ctx, "c"))
	require.NoError(t, bm.PutObject(ctx, "c", "k", []byte("v"), "text/plain", nil))

	obj, err := bm.GetObject(ctx, "c", "k")
	require.NoError(t, err)
	assert.Equal(t, []byte("v"), obj.Data)
	require.NoError(t, bm.DeleteObject(ctx, "c", "k"))
}

// TestBlobEventNoSubscriptionNoDelivery covers case (e) on the producer side:
// with a wired publisher but no matching subscription, the blob write still
// succeeds and nothing is delivered.
func TestBlobEventNoSubscriptionNoDelivery(t *testing.T) {
	receiver := newBlobWebhookReceiver(t)
	bm, eg := newWiredMocks(t)
	ctx := context.Background()

	// Subscription only accepts BlobDeleted; a BlobCreated must not reach it.
	subscribe(t, eg, "sub1", receiver.URL, map[string]any{
		"includedEventTypes": []string{eventTypeBlobDeleted},
	})

	require.NoError(t, bm.CreateBucket(ctx, "c"))
	require.NoError(t, bm.PutObject(ctx, "c", "k", []byte("v"), "text/plain", nil))

	assert.Empty(t, receiver.events())
}
