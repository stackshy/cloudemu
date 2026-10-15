package eventgrid_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

func wantHTTP(t *testing.T, err error, status int) {
	t.Helper()

	var re *azcore.ResponseError
	if !errors.As(err, &re) || re.StatusCode != status {
		t.Fatalf("want HTTP %d, got %v", status, err)
	}
}

func webhookWithSecret() armeventgrid.EventSubscription {
	return armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.WebHookEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeWebHook),
				Properties: &armeventgrid.WebHookEventSubscriptionDestinationProperties{
					EndpointURL: to.Ptr("https://example.test/hook?code=s3cret"),
					DeliveryAttributeMappings: []armeventgrid.DeliveryAttributeMappingClassification{
						&armeventgrid.StaticDeliveryAttributeMapping{
							Name: to.Ptr("auth"),
							Type: to.Ptr(armeventgrid.DeliveryAttributeMappingTypeStatic),
							Properties: &armeventgrid.StaticDeliveryAttributeMappingProperties{
								Value: to.Ptr("token"), IsSecret: to.Ptr(true),
							},
						},
					},
				},
			},
		},
	}
}

func storageQueueSub() armeventgrid.EventSubscription {
	return armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.StorageQueueEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeStorageQueue),
				Properties: &armeventgrid.StorageQueueEventSubscriptionDestinationProperties{
					QueueName:  to.Ptr("q"),
					ResourceID: to.Ptr("/subscriptions/" + testSub + "/resourceGroups/" + testRG + "/providers/Microsoft.Storage/storageAccounts/a"),
				},
			},
		},
	}
}

func putSub(t *testing.T, c *armeventgrid.EventSubscriptionsClient, scope, name string, sub armeventgrid.EventSubscription) {
	t.Helper()

	poller, err := c.BeginCreateOrUpdate(context.Background(), scope, name, sub, nil)
	if err != nil {
		t.Fatalf("create %s/%s: %v", scope, name, err)
	}

	if _, err := poller.PollUntilDone(context.Background(), nil); err != nil {
		t.Fatalf("poll %s/%s: %v", scope, name, err)
	}
}

// TestSDKEventSubscriptionGetFullURL covers the actions azurerm's
// eventgrid_event_subscription Read calls: getFullUrl at topic, resource group
// and subscription scope, and getDeliveryAttributes. The resource group scope
// returned 501 and the topic scope 400 before these routes existed.
func TestSDKEventSubscriptionGetFullURL(t *testing.T) {
	cf := newEventGridFactory(t)
	ctx := context.Background()
	c := cf.NewEventSubscriptionsClient()

	createTopic(t, cf.NewTopicsClient(), testRG, "tp", nil)

	rgScope := "/subscriptions/" + testSub + "/resourceGroups/" + testRG
	topicScope := rgScope + "/providers/Microsoft.EventGrid/topics/tp"
	subScope := "/subscriptions/" + testSub

	for _, scope := range []string{topicScope, rgScope, subScope} {
		putSub(t, c, scope, "hook", webhookWithSecret())

		got, err := c.GetFullURL(ctx, scope, "hook", nil)
		if err != nil || *got.EndpointURL != "https://example.test/hook?code=s3cret" {
			t.Fatalf("getFullUrl at %s: %v", scope, err)
		}

		attrs, err := c.GetDeliveryAttributes(ctx, scope, "hook", nil)
		if err != nil || len(attrs.Value) != 1 {
			t.Fatalf("getDeliveryAttributes at %s: %v", scope, err)
		}

		m, ok := attrs.Value[0].(*armeventgrid.StaticDeliveryAttributeMapping)
		if !ok || *m.Properties.Value != "token" || !*m.Properties.IsSecret {
			t.Fatalf("delivery attribute not verbatim at %s: %+v", scope, attrs.Value[0])
		}

		_, err = c.GetFullURL(ctx, scope, "missing", nil)
		wantHTTP(t, err, http.StatusNotFound)

		// An unset subject filter reads back as "" for both ends, which
		// azurerm treats as no subject_filter block.
		sub, err := c.Get(ctx, scope, "hook", nil)
		if err != nil || sub.Properties.Filter == nil || sub.Properties.Filter.SubjectBeginsWith == nil ||
			*sub.Properties.Filter.SubjectBeginsWith != "" || sub.Properties.Filter.SubjectEndsWith == nil {
			t.Fatalf("subject filter defaults at %s: %v %+v", scope, err, sub.Properties)
		}
	}

	putSub(t, c, rgScope, "sq", storageQueueSub())

	_, err := c.GetFullURL(ctx, rgScope, "sq", nil)
	wantHTTP(t, err, http.StatusBadRequest)

	attrs, err := c.GetDeliveryAttributes(ctx, rgScope, "sq", nil)
	if err != nil || attrs.Value == nil || len(attrs.Value) != 0 {
		t.Fatalf("empty delivery attributes: %v %+v", err, attrs.Value)
	}

	direct, err := cf.NewTopicEventSubscriptionsClient().GetFullURL(ctx, testRG, "tp", "hook", nil)
	if err != nil || *direct.EndpointURL != "https://example.test/hook?code=s3cret" {
		t.Fatalf("direct-form getFullUrl: %v", err)
	}
}

func TestSDKSystemTopicSubscriptionGetFullURL(t *testing.T) {
	cf := newEventGridFactory(t)
	ctx := context.Background()

	poller, err := cf.NewSystemTopicsClient().BeginCreateOrUpdate(ctx, testRG, "st", armeventgrid.SystemTopic{
		Location: to.Ptr("global"),
		Properties: &armeventgrid.SystemTopicProperties{
			Source:    to.Ptr("/subscriptions/" + testSub + "/resourceGroups/" + testRG + "/providers/Microsoft.Storage/storageAccounts/a"),
			TopicType: to.Ptr("Microsoft.Storage.StorageAccounts"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("system topic: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("system topic poll: %v", err)
	}

	subs := cf.NewSystemTopicEventSubscriptionsClient()

	sp, err := subs.BeginCreateOrUpdate(ctx, testRG, "st", "hook", webhookWithSecret(), nil)
	if err != nil {
		t.Fatalf("system topic sub: %v", err)
	}

	if _, err := sp.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("system topic sub poll: %v", err)
	}

	got, err := subs.GetFullURL(ctx, testRG, "st", "hook", nil)
	if err != nil || *got.EndpointURL != "https://example.test/hook?code=s3cret" {
		t.Fatalf("system topic getFullUrl: %v", err)
	}

	_, err = subs.GetFullURL(ctx, testRG, "st", "missing", nil)
	wantHTTP(t, err, http.StatusNotFound)
}
