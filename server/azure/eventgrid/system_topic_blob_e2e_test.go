// Full real-user HTTP end-to-end tests for the Blob Storage -> Event Grid
// system-topic path: a system topic and its event subscription are created over
// the real armeventgrid ARM SDK, a blob is written over the real azblob SDK, and
// the subscription's destination (WebHook / ServiceBusQueue) actually receives
// the Microsoft.Storage.BlobCreated/BlobDeleted event — proving wire-created
// system-topic subscriptions are bridged to the delivery path.
package eventgrid_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
)

// blobSource is a Microsoft.Storage system-topic source whose leaf is the
// emulator's fixed storage account name ("cloudemu"), which is the key the Blob
// Storage producer stamps on the events it emits — so a subscription registered
// against this source's delivery bus receives them.
const blobSource = "/subscriptions/" + testSub +
	"/resourceGroups/" + testRG + "/providers/Microsoft.Storage/storageAccounts/cloudemu"

// egWebhookReceiver records every Event Grid batch POSTed to it.
type egWebhookReceiver struct {
	*httptest.Server

	mu    sync.Mutex
	calls []deliveredBlobEvent
}

type deliveredBlobEvent struct {
	ID        string          `json:"id"`
	Topic     string          `json:"topic"`
	Subject   string          `json:"subject"`
	EventType string          `json:"eventType"`
	Data      json.RawMessage `json:"data"`
}

func newEGWebhookReceiver(t *testing.T) *egWebhookReceiver {
	t.Helper()

	rc := &egWebhookReceiver{}
	rc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch []deliveredBlobEvent
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

func (rc *egWebhookReceiver) events() []deliveredBlobEvent {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	out := make([]deliveredBlobEvent, len(rc.calls))
	copy(out, rc.calls)

	return out
}

// blobEGServer bundles the shared provider and the two real SDK surfaces (ARM
// Event Grid + azblob data plane) mounted on one server, so a blob write routes
// through the same in-memory Event Grid the subscriptions were created against.
type blobEGServer struct {
	serviceBus sbQueueReader
	systems    *armeventgrid.SystemTopicsClient
	subs       *armeventgrid.SystemTopicEventSubscriptionsClient
	topics     *armeventgrid.TopicsClient
	topicSubs  *armeventgrid.TopicEventSubscriptionsClient
	blob       *azblob.Client
	ts         *httptest.Server
}

// sbQueueReader is the slice of the Service Bus mock the ServiceBus e2e case
// uses to create and drain the peer queue (satisfied by *servicebus.Mock).
type sbQueueReader interface {
	CreateQueue(context.Context, mqdriver.QueueConfig) (*mqdriver.QueueInfo, error)
	ReceiveMessages(context.Context, mqdriver.ReceiveMessageInput) ([]mqdriver.Message, error)
}

func newBlobEGServer(t *testing.T) *blobEGServer {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		BlobStorage: cloudP.BlobStorage,
		EventGrid:   cloudP.EventGrid,
		ServiceBus:  cloudP.ServiceBus,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	armOpts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armeventgrid.NewClientFactory(testSub, fakeCred{}, armOpts)
	if err != nil {
		t.Fatalf("armeventgrid.NewClientFactory: %v", err)
	}

	blobClient, err := azblob.NewClientWithNoCredential(ts.URL+"/", &azblob.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		t.Fatalf("azblob.NewClientWithNoCredential: %v", err)
	}

	return &blobEGServer{
		serviceBus: cloudP.ServiceBus,
		systems:    cf.NewSystemTopicsClient(),
		subs:       cf.NewSystemTopicEventSubscriptionsClient(),
		topics:     cf.NewTopicsClient(),
		topicSubs:  cf.NewTopicEventSubscriptionsClient(),
		blob:       blobClient,
		ts:         ts,
	}
}

// createCustomTopic creates a user-facing custom Event Grid topic over the ARM
// SDK (used to force the "cloudemu" name collision with the system delivery bus).
func (s *blobEGServer) createCustomTopic(ctx context.Context, t *testing.T, name string) {
	t.Helper()

	poller, err := s.topics.BeginCreateOrUpdate(ctx, testRG, name,
		armeventgrid.Topic{Location: to.Ptr("eastus")}, nil)
	if err != nil {
		t.Fatalf("custom topic BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("custom topic PollUntilDone: %v", err)
	}
}

// createCustomWebhookSub creates a custom-topic event subscription delivering to
// url over the ARM SDK.
func (s *blobEGServer) createCustomWebhookSub(ctx context.Context, t *testing.T, topic, name, url string) {
	t.Helper()

	sub := armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.WebHookEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeWebHook),
				Properties: &armeventgrid.WebHookEventSubscriptionDestinationProperties{
					EndpointURL: to.Ptr(url),
				},
			},
		},
	}

	poller, err := s.topicSubs.BeginCreateOrUpdate(ctx, testRG, topic, name, sub, nil)
	if err != nil {
		t.Fatalf("custom subscription BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("custom subscription PollUntilDone: %v", err)
	}
}

// publishCustomEvent posts one EventGridEvent to a custom topic's data-plane
// endpoint over HTTP, addressed by Host (matching how a real publisher reaches a
// topic). Custom-topic events carry no Topic override, so they route to the
// user-facing bus store — never the system delivery store.
func (s *blobEGServer) publishCustomEvent(ctx context.Context, t *testing.T, topic string) {
	t.Helper()

	body := `[{"id":"c1","subject":"orders/1","eventType":"Order.Created",` +
		`"eventTime":"2024-01-02T03:04:05Z","data":{"total":42},"dataVersion":"1.0"}]`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.ts.URL+"/api/events", strings.NewReader(body))
	if err != nil {
		t.Fatalf("publish new request: %v", err)
	}

	req.Host = topic + ".eastus-1.eventgrid.azure.net"
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("publish custom event: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish status = %d, want 200", resp.StatusCode)
	}
}

// createSystemTopic creates the Blob Storage system topic over the ARM SDK.
func (s *blobEGServer) createSystemTopic(ctx context.Context, t *testing.T, name string) {
	t.Helper()

	poller, err := s.systems.BeginCreateOrUpdate(ctx, testRG, name, armeventgrid.SystemTopic{
		Location: to.Ptr("global"),
		Properties: &armeventgrid.SystemTopicProperties{
			Source:    to.Ptr(blobSource),
			TopicType: to.Ptr("Microsoft.Storage.StorageAccounts"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("system topic BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("system topic PollUntilDone: %v", err)
	}
}

// createWebhookSub creates a system-topic subscription delivering to url, with
// an optional includedEventTypes filter.
func (s *blobEGServer) createWebhookSub(ctx context.Context, t *testing.T, topic, name, url string, eventTypes []string) {
	t.Helper()

	var filter *armeventgrid.EventSubscriptionFilter
	if eventTypes != nil {
		filter = &armeventgrid.EventSubscriptionFilter{IncludedEventTypes: to.SliceOfPtrs(eventTypes...)}
	}

	sub := armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.WebHookEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeWebHook),
				Properties: &armeventgrid.WebHookEventSubscriptionDestinationProperties{
					EndpointURL: to.Ptr(url),
				},
			},
			Filter: filter,
		},
	}

	poller, err := s.subs.BeginCreateOrUpdate(ctx, testRG, topic, name, sub, nil)
	if err != nil {
		t.Fatalf("subscription BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("subscription PollUntilDone: %v", err)
	}
}

func (s *blobEGServer) uploadBlob(ctx context.Context, t *testing.T, container, key string, body []byte) {
	t.Helper()

	if _, err := s.blob.CreateContainer(ctx, container, nil); err != nil {
		t.Fatalf("CreateContainer(%s): %v", container, err)
	}

	if _, err := s.blob.UploadBuffer(ctx, container, key, body, nil); err != nil {
		t.Fatalf("UploadBuffer(%s/%s): %v", container, key, err)
	}
}

// eventuallyEvents polls the receiver until it has at least want events or the
// deadline elapses, returning whatever it saw (delivery is synchronous, so this
// only guards against scheduling jitter, not real asynchrony).
func eventuallyEvents(rc *egWebhookReceiver, want int) []deliveredBlobEvent {
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := rc.events()
		if len(got) >= want || time.Now().After(deadline) {
			return got
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// TestSDKSystemTopicBlobWebhookE2E is case (a): a WebHook system-topic
// subscription created over ARM receives a BlobCreated event when a blob is
// PUT over the real azblob surface.
func TestSDKSystemTopicBlobWebhookE2E(t *testing.T) {
	ctx := context.Background()
	s := newBlobEGServer(t)
	rc := newEGWebhookReceiver(t)

	s.createSystemTopic(ctx, t, "storage-events")
	s.createWebhookSub(ctx, t, "storage-events", "to-hook", rc.URL,
		[]string{"Microsoft.Storage.BlobCreated"})

	s.uploadBlob(ctx, t, "images", "cat.png", []byte("hello"))

	got := eventuallyEvents(rc, 1)
	if len(got) != 1 {
		t.Fatalf("delivered events = %d, want 1", len(got))
	}

	if got[0].EventType != "Microsoft.Storage.BlobCreated" {
		t.Fatalf("eventType = %q, want Microsoft.Storage.BlobCreated", got[0].EventType)
	}

	if got[0].Subject != "/blobServices/default/containers/images/blobs/cat.png" {
		t.Fatalf("subject = %q, unexpected", got[0].Subject)
	}

	// The delivered topic is the storage account (the source), not the Event
	// Grid resource the subscription hangs off.
	if !strings.Contains(got[0].Topic, "/providers/Microsoft.Storage/storageAccounts/cloudemu") {
		t.Fatalf("topic = %q, want the storage account id", got[0].Topic)
	}
}

// TestSDKSystemTopicBlobServiceBusE2E is case (b): a ServiceBusQueue
// system-topic subscription lands the blob event in the peer Service Bus queue.
func TestSDKSystemTopicBlobServiceBusE2E(t *testing.T) {
	ctx := context.Background()
	s := newBlobEGServer(t)

	queue, err := s.serviceBus.CreateQueue(ctx, mqdriver.QueueConfig{Name: "blob-events-q"})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	s.createSystemTopic(ctx, t, "storage-events")

	resourceID := "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.ServiceBus/namespaces/ns1/queues/blob-events-q"

	sub := armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.ServiceBusQueueEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeServiceBusQueue),
				Properties: &armeventgrid.ServiceBusQueueEventSubscriptionDestinationProperties{
					ResourceID: to.Ptr(resourceID),
				},
			},
		},
	}

	poller, err := s.subs.BeginCreateOrUpdate(ctx, testRG, "storage-events", "to-sbq", sub, nil)
	if err != nil {
		t.Fatalf("subscription BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("subscription PollUntilDone: %v", err)
	}

	s.uploadBlob(ctx, t, "docs", "a.txt", []byte("hi"))

	msgs, err := s.serviceBus.ReceiveMessages(ctx,
		mqdriver.ReceiveMessageInput{QueueURL: queue.URL, MaxMessages: 10})
	if err != nil {
		t.Fatalf("ReceiveMessages: %v", err)
	}

	if len(msgs) != 1 {
		t.Fatalf("queue messages = %d, want 1", len(msgs))
	}

	var batch []deliveredBlobEvent
	if err := json.Unmarshal([]byte(msgs[0].Body), &batch); err != nil {
		t.Fatalf("decode enqueued envelope: %v", err)
	}

	if len(batch) != 1 || batch[0].EventType != "Microsoft.Storage.BlobCreated" {
		t.Fatalf("enqueued event = %+v, want a BlobCreated", batch)
	}
}

// TestSDKSystemTopicBlobFilterDrops is case (c): a subscription filtered to
// BlobDeleted must not receive a BlobCreated, but does receive the later delete.
func TestSDKSystemTopicBlobFilterDrops(t *testing.T) {
	ctx := context.Background()
	s := newBlobEGServer(t)
	rc := newEGWebhookReceiver(t)

	s.createSystemTopic(ctx, t, "storage-events")
	s.createWebhookSub(ctx, t, "storage-events", "deletes-only", rc.URL,
		[]string{"Microsoft.Storage.BlobDeleted"})

	s.uploadBlob(ctx, t, "c", "k", []byte("v"))

	if got := eventuallyEvents(rc, 1); len(got) != 0 {
		t.Fatalf("BlobCreated leaked past a BlobDeleted-only filter: %+v", got)
	}

	if _, err := s.blob.DeleteBlob(ctx, "c", "k", nil); err != nil {
		t.Fatalf("DeleteBlob: %v", err)
	}

	got := eventuallyEvents(rc, 1)
	if len(got) != 1 || got[0].EventType != "Microsoft.Storage.BlobDeleted" {
		t.Fatalf("delete delivery = %+v, want one BlobDeleted", got)
	}
}

// TestSDKSystemTopicBlobDeleteStopsDelivery is case (d): deleting the
// subscription over ARM stops further delivery.
func TestSDKSystemTopicBlobDeleteStopsDelivery(t *testing.T) {
	ctx := context.Background()
	s := newBlobEGServer(t)
	rc := newEGWebhookReceiver(t)

	s.createSystemTopic(ctx, t, "storage-events")
	s.createWebhookSub(ctx, t, "storage-events", "to-hook", rc.URL, nil)

	s.uploadBlob(ctx, t, "c", "first", []byte("1"))

	if got := eventuallyEvents(rc, 1); len(got) != 1 {
		t.Fatalf("pre-delete delivery = %d, want 1", len(got))
	}

	delPoller, err := s.subs.BeginDelete(ctx, testRG, "storage-events", "to-hook", nil)
	if err != nil {
		t.Fatalf("subscription BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("subscription delete PollUntilDone: %v", err)
	}

	if _, err := s.blob.UploadBuffer(ctx, "c", "second", []byte("2"), nil); err != nil {
		t.Fatalf("second UploadBuffer: %v", err)
	}

	if got := eventuallyEvents(rc, 2); len(got) != 1 {
		t.Fatalf("post-delete delivery = %d, want it to stay at 1", len(got))
	}
}

// TestSDKSystemTopicBlobCollisionForward is the forward collision case: a real
// custom Event Grid topic named "cloudemu" (the same name the Blob system
// delivery bus keys on) coexists with the system topic without either leaking
// into the other. Blob events reach only the system subscriber; a custom-topic
// publish reaches only the custom subscriber; the custom topic stays a normal,
// independent, listable topic.
func TestSDKSystemTopicBlobCollisionForward(t *testing.T) {
	ctx := context.Background()
	s := newBlobEGServer(t)
	systemRC := newEGWebhookReceiver(t)
	customRC := newEGWebhookReceiver(t)

	// A user creates a custom topic that collides with the storage-account name.
	s.createCustomTopic(ctx, t, "cloudemu")
	s.createCustomWebhookSub(ctx, t, "cloudemu", "cust-sub", customRC.URL)

	// The Blob system topic + subscription is created alongside it.
	s.createSystemTopic(ctx, t, "storage-events")
	s.createWebhookSub(ctx, t, "storage-events", "sys-sub", systemRC.URL,
		[]string{"Microsoft.Storage.BlobCreated"})

	// The custom topic was neither clobbered nor re-scoped: it is still gettable
	// and listed exactly once.
	if _, err := s.topics.Get(ctx, testRG, "cloudemu", nil); err != nil {
		t.Fatalf("custom topic Get after system topic create: %v", err)
	}

	if n := s.countCustomTopics(ctx, t); n != 1 {
		t.Fatalf("custom topic list count = %d, want 1", n)
	}

	// A blob write reaches the system subscriber only — never the custom one.
	s.uploadBlob(ctx, t, "images", "cat.png", []byte("hello"))

	sysGot := eventuallyEvents(systemRC, 1)
	if len(sysGot) != 1 || sysGot[0].EventType != "Microsoft.Storage.BlobCreated" {
		t.Fatalf("system delivery = %+v, want one BlobCreated", sysGot)
	}

	if got := eventuallyEvents(customRC, 1); len(got) != 0 {
		t.Fatalf("blob event leaked to the custom topic subscriber: %+v", got)
	}

	// A custom-topic publish reaches the custom subscriber only — never the
	// system one.
	s.publishCustomEvent(ctx, t, "cloudemu")

	custGot := eventuallyEvents(customRC, 1)
	if len(custGot) != 1 || custGot[0].EventType != "Order.Created" {
		t.Fatalf("custom delivery = %+v, want one Order.Created", custGot)
	}

	if got := eventuallyEvents(systemRC, 2); len(got) != 1 {
		t.Fatalf("custom event leaked to the system subscriber: %+v", got)
	}
}

// TestSDKSystemTopicBlobCollisionReverse is the reverse collision case: the
// system topic exists first, then a user creates — and deletes — a custom topic
// named "cloudemu". Neither operation disturbs system-topic Blob delivery.
func TestSDKSystemTopicBlobCollisionReverse(t *testing.T) {
	ctx := context.Background()
	s := newBlobEGServer(t)
	rc := newEGWebhookReceiver(t)

	s.createSystemTopic(ctx, t, "storage-events")
	s.createWebhookSub(ctx, t, "storage-events", "sys-sub", rc.URL,
		[]string{"Microsoft.Storage.BlobCreated"})

	s.uploadBlob(ctx, t, "c", "first", []byte("1"))
	if got := eventuallyEvents(rc, 1); len(got) != 1 {
		t.Fatalf("baseline system delivery = %d, want 1", len(got))
	}

	// A user creates then deletes a colliding custom topic.
	s.createCustomTopic(ctx, t, "cloudemu")

	delPoller, err := s.topics.BeginDelete(ctx, testRG, "cloudemu", nil)
	if err != nil {
		t.Fatalf("custom topic BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("custom topic delete PollUntilDone: %v", err)
	}

	// System delivery survives the custom topic's create+delete unchanged.
	if _, err := s.blob.UploadBuffer(ctx, "c", "second", []byte("2"), nil); err != nil {
		t.Fatalf("second UploadBuffer: %v", err)
	}

	if got := eventuallyEvents(rc, 2); len(got) != 2 {
		t.Fatalf("system delivery after custom topic churn = %d, want 2", len(got))
	}
}

// countCustomTopics returns how many custom topics the ARM list surface reports
// in the test resource group.
func (s *blobEGServer) countCustomTopics(ctx context.Context, t *testing.T) int {
	t.Helper()

	n := 0

	pager := s.topics.NewListByResourceGroupPager(testRG, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("custom topic list: %v", err)
		}

		n += len(page.Value)
	}

	return n
}
