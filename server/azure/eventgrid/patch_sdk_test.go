package eventgrid_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

// TestSDKTopicEventSubscriptionPatch drives TopicEventSubscriptions.BeginUpdate
// (PATCH): the supplied field (filter) is updated while an omitted field
// (destination) is preserved — no nil-mask data loss.
func TestSDKTopicEventSubscriptionPatch(t *testing.T) {
	cf, _ := newEGFactory(t)
	ctx := context.Background()

	topics := cf.NewTopicsClient()
	mkTopicLoc(t, topics, "orders", "eastus")

	subs := cf.NewTopicEventSubscriptionsClient()

	createPoller, err := subs.BeginCreateOrUpdate(ctx, testRG, "orders", "sub1", armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.WebHookEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeWebHook),
				Properties: &armeventgrid.WebHookEventSubscriptionDestinationProperties{
					EndpointURL: to.Ptr("https://example.test/hook"),
				},
			},
			Filter: &armeventgrid.EventSubscriptionFilter{
				SubjectBeginsWith: to.Ptr("orders/"),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}
	if _, err = createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create PollUntilDone: %v", err)
	}

	// PATCH only the filter; the destination is omitted and must survive.
	upPoller, err := subs.BeginUpdate(ctx, testRG, "orders", "sub1", armeventgrid.EventSubscriptionUpdateParameters{
		Filter: &armeventgrid.EventSubscriptionFilter{
			SubjectBeginsWith: to.Ptr("invoices/"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}
	if _, err = upPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("update PollUntilDone: %v", err)
	}

	got, err := subs.Get(ctx, testRG, "orders", "sub1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.Filter == nil ||
		got.Properties.Filter.SubjectBeginsWith == nil ||
		*got.Properties.Filter.SubjectBeginsWith != "invoices/" {
		t.Fatalf("filter not updated by PATCH: %+v", got.Properties)
	}

	dest, ok := got.Properties.Destination.(*armeventgrid.WebHookEventSubscriptionDestination)
	if !ok {
		t.Fatalf("destination lost by PATCH (nil-mask): type = %T", got.Properties.Destination)
	}
	if dest.Properties == nil || dest.Properties.EndpointURL == nil ||
		*dest.Properties.EndpointURL != "https://example.test/hook" {
		t.Fatalf("destination not preserved by PATCH: %+v", dest.Properties)
	}
}

// TestSDKTopicEventSubscriptionPatchMissing asserts a PATCH against a
// non-existent subscription 404s before any write.
func TestSDKTopicEventSubscriptionPatchMissing(t *testing.T) {
	cf, _ := newEGFactory(t)
	ctx := context.Background()

	topics := cf.NewTopicsClient()
	mkTopicLoc(t, topics, "orders", "eastus")

	subs := cf.NewTopicEventSubscriptionsClient()

	poller, err := subs.BeginUpdate(ctx, testRG, "orders", "no-such-sub",
		armeventgrid.EventSubscriptionUpdateParameters{
			Filter: &armeventgrid.EventSubscriptionFilter{SubjectBeginsWith: to.Ptr("x/")},
		}, nil)
	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusNotFound {
		t.Fatalf("PATCH missing subscription: got %v, want 404", err)
	}
}

// TestSDKScopedEventSubscriptionPatch drives EventSubscriptions.BeginUpdate
// (PATCH) on an RG-scoped subscription: the supplied filter is updated while the
// omitted destination is preserved (no nil-mask).
func TestSDKScopedEventSubscriptionPatch(t *testing.T) {
	cf, _ := newEGFactory(t)
	client := cf.NewEventSubscriptionsClient()
	ctx := context.Background()

	scope := "/subscriptions/" + testSub + "/resourceGroups/" + testRG

	createPoller, err := client.BeginCreateOrUpdate(ctx, scope, "rg-sub", armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.WebHookEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeWebHook),
				Properties: &armeventgrid.WebHookEventSubscriptionDestinationProperties{
					EndpointURL: to.Ptr("https://example.test/hook"),
				},
			},
			Filter: &armeventgrid.EventSubscriptionFilter{
				SubjectBeginsWith: to.Ptr("orders/"),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}
	if _, err = createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create PollUntilDone: %v", err)
	}

	upPoller, err := client.BeginUpdate(ctx, scope, "rg-sub", armeventgrid.EventSubscriptionUpdateParameters{
		Filter: &armeventgrid.EventSubscriptionFilter{SubjectBeginsWith: to.Ptr("invoices/")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}
	if _, err = upPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("update PollUntilDone: %v", err)
	}

	got, err := client.Get(ctx, scope, "rg-sub", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.Filter == nil ||
		got.Properties.Filter.SubjectBeginsWith == nil ||
		*got.Properties.Filter.SubjectBeginsWith != "invoices/" {
		t.Fatalf("filter not updated by PATCH: %+v", got.Properties)
	}

	if _, ok := got.Properties.Destination.(*armeventgrid.WebHookEventSubscriptionDestination); !ok {
		t.Fatalf("destination lost by PATCH (nil-mask): type = %T", got.Properties.Destination)
	}
}

// TestSDKSystemTopicPatch drives SystemTopics.BeginUpdate (PATCH): the supplied
// tag is merged while the pre-existing tag is preserved.
func TestSDKSystemTopicPatch(t *testing.T) {
	cf, _ := newEGFactory(t)
	st := cf.NewSystemTopicsClient()
	ctx := context.Background()

	poller, err := st.BeginCreateOrUpdate(ctx, testRG, "storage-events", armeventgrid.SystemTopic{
		Location: to.Ptr("global"),
		Tags:     map[string]*string{"env": to.Ptr("test")},
		Properties: &armeventgrid.SystemTopicProperties{
			Source:    to.Ptr("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Storage/storageAccounts/a"),
			TopicType: to.Ptr("Microsoft.Storage.StorageAccounts"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}
	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create PollUntilDone: %v", err)
	}

	upPoller, err := st.BeginUpdate(ctx, testRG, "storage-events", armeventgrid.SystemTopicUpdateParameters{
		Tags: map[string]*string{"team": to.Ptr("a")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}
	if _, err = upPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("update PollUntilDone: %v", err)
	}

	got, err := st.Get(ctx, testRG, "storage-events", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Tags["team"] == nil || *got.Tags["team"] != "a" {
		t.Fatalf("PATCH did not apply supplied tag: %+v", got.Tags)
	}
	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("PATCH masked pre-existing tag (nil-mask): %+v", got.Tags)
	}
}

// TestSDKDomainPatch drives Domains.BeginUpdate (PATCH): publicNetworkAccess is
// updated and a supplied tag is merged, while the immutable inputSchema and the
// pre-existing tag are preserved.
func TestSDKDomainPatch(t *testing.T) {
	cf, _ := newEGFactory(t)
	dc := cf.NewDomainsClient()
	ctx := context.Background()

	poller, err := dc.BeginCreateOrUpdate(ctx, testRG, "dom1", armeventgrid.Domain{
		Location: to.Ptr("global"),
		Tags:     map[string]*string{"env": to.Ptr("test")},
		Properties: &armeventgrid.DomainProperties{
			PublicNetworkAccess: to.Ptr(armeventgrid.PublicNetworkAccessEnabled),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}
	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create PollUntilDone: %v", err)
	}

	upPoller, err := dc.BeginUpdate(ctx, testRG, "dom1", armeventgrid.DomainUpdateParameters{
		Tags: map[string]*string{"team": to.Ptr("a")},
		Properties: &armeventgrid.DomainUpdateParameterProperties{
			PublicNetworkAccess: to.Ptr(armeventgrid.PublicNetworkAccessDisabled),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}
	if _, err = upPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("update PollUntilDone: %v", err)
	}

	got, err := dc.Get(ctx, testRG, "dom1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.PublicNetworkAccess == nil ||
		*got.Properties.PublicNetworkAccess != armeventgrid.PublicNetworkAccessDisabled {
		t.Fatalf("PATCH did not update publicNetworkAccess: %+v", got.Properties)
	}
	if got.Properties.InputSchema == nil || *got.Properties.InputSchema != armeventgrid.InputSchemaEventGridSchema {
		t.Fatalf("PATCH lost immutable inputSchema: %+v", got.Properties)
	}
	if got.Tags["team"] == nil || *got.Tags["team"] != "a" {
		t.Fatalf("PATCH did not apply supplied tag: %+v", got.Tags)
	}
	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("PATCH masked pre-existing tag (nil-mask): %+v", got.Tags)
	}
}

// TestSDKPublishPreservesIDAndDataVersion locks the data-plane fixes end-to-end:
// a publish carrying an explicit id and dataVersion delivers them verbatim, with
// metadataVersion stamped "1".
func TestSDKPublishPreservesIDAndDataVersion(t *testing.T) {
	cf, ts := newEGFactory(t)
	ctx := context.Background()

	topics := cf.NewTopicsClient()
	mkTopicLoc(t, topics, "orders", "eastus")

	receiver := newLiveWebhookReceiver(t)

	subs := cf.NewTopicEventSubscriptionsClient()
	poller, err := subs.BeginCreateOrUpdate(ctx, testRG, "orders", "sub1", armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.WebHookEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeWebHook),
				Properties: &armeventgrid.WebHookEventSubscriptionDestinationProperties{
					EndpointURL: to.Ptr(receiver.URL),
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("subscription BeginCreateOrUpdate: %v", err)
	}
	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("subscription PollUntilDone: %v", err)
	}

	body := `[{"id":"e1","subject":"orders/1","eventType":"Order.Created",` +
		`"eventTime":"2024-01-02T03:04:05Z","data":{"total":42},"dataVersion":"2.0"}]`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/events", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new publish request: %v", err)
	}
	req.Host = "orders.eastus-1.eventgrid.azure.net"
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish status = %d, want 200", resp.StatusCode)
	}

	got := receiver.all()
	if len(got) != 1 {
		t.Fatalf("webhook received %d events, want 1: %+v", len(got), got)
	}
	if got[0]["id"] != "e1" {
		t.Fatalf("delivered id = %v, want e1", got[0]["id"])
	}
	if got[0]["dataVersion"] != "2.0" {
		t.Fatalf("delivered dataVersion = %v, want 2.0", got[0]["dataVersion"])
	}
	if got[0]["metadataVersion"] != "1" {
		t.Fatalf("delivered metadataVersion = %v, want 1", got[0]["metadataVersion"])
	}
}
