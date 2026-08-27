package eventgrid_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func newEGFactory(t *testing.T) (*armeventgrid.ClientFactory, *httptest.Server) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{EventGrid: cloudP.EventGrid})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud:     myCloud,
		Transport: ts.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}

	cf, err := armeventgrid.NewClientFactory(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewClientFactory: %v", err)
	}

	return cf, ts
}

func mkTopicLoc(t *testing.T, topics *armeventgrid.TopicsClient, name, location string) {
	t.Helper()

	poller, err := topics.BeginCreateOrUpdate(context.Background(), testRG, name, armeventgrid.Topic{
		Location: to.Ptr(location),
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(context.Background(), nil); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}
}

func TestSDKTopicLocationAndEndpoint(t *testing.T) {
	cf, _ := newEGFactory(t)
	topics := cf.NewTopicsClient()
	ctx := context.Background()

	mkTopicLoc(t, topics, "orders", "eastus")

	got, err := topics.Get(ctx, testRG, "orders", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Location == nil || *got.Location != "eastus" {
		t.Fatalf("location = %v, want eastus", got.Location)
	}

	if got.Properties == nil || got.Properties.Endpoint == nil {
		t.Fatal("endpoint missing")
	}

	want := "https://orders.eastus-1.eventgrid.azure.net/api/events"
	if *got.Properties.Endpoint != want {
		t.Fatalf("endpoint = %q, want %q", *got.Properties.Endpoint, want)
	}
}

func TestSDKTopicListSharedAccessKeys(t *testing.T) {
	cf, _ := newEGFactory(t)
	topics := cf.NewTopicsClient()
	ctx := context.Background()

	mkTopicLoc(t, topics, "orders", "eastus")

	keys, err := topics.ListSharedAccessKeys(ctx, testRG, "orders", nil)
	if err != nil {
		t.Fatalf("ListSharedAccessKeys: %v", err)
	}

	if keys.Key1 == nil || *keys.Key1 == "" || keys.Key2 == nil || *keys.Key2 == "" {
		t.Fatalf("keys = %+v, want non-empty key1/key2", keys)
	}

	if *keys.Key1 == *keys.Key2 {
		t.Fatal("key1 and key2 must differ")
	}
}

func TestSDKTopicEventSubscriptionRoundTrip(t *testing.T) {
	cf, _ := newEGFactory(t)
	topics := cf.NewTopicsClient()
	subs := cf.NewTopicEventSubscriptionsClient()
	ctx := context.Background()

	mkTopicLoc(t, topics, "orders", "eastus")

	sub := armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.WebHookEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeWebHook),
				Properties: &armeventgrid.WebHookEventSubscriptionDestinationProperties{
					EndpointURL: to.Ptr("https://example.com/webhook"),
				},
			},
			Filter: &armeventgrid.EventSubscriptionFilter{
				SubjectBeginsWith: to.Ptr("orders/"),
			},
		},
	}

	poller, err := subs.BeginCreateOrUpdate(ctx, testRG, "orders", "sub1", sub, nil)
	if err != nil {
		t.Fatalf("subscription BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("subscription PollUntilDone: %v", err)
	}

	got, err := subs.Get(ctx, testRG, "orders", "sub1", nil)
	if err != nil {
		t.Fatalf("subscription Get: %v", err)
	}

	if got.Name == nil || *got.Name != "sub1" {
		t.Fatalf("name = %v, want sub1", got.Name)
	}

	dest, ok := got.Properties.Destination.(*armeventgrid.WebHookEventSubscriptionDestination)
	if !ok {
		t.Fatalf("destination type = %T, want WebHook", got.Properties.Destination)
	}

	if dest.Properties == nil || dest.Properties.EndpointURL == nil ||
		*dest.Properties.EndpointURL != "https://example.com/webhook" {
		t.Fatalf("endpointUrl round-trip failed: %+v", dest.Properties)
	}

	if got.Properties.Filter == nil || got.Properties.Filter.SubjectBeginsWith == nil ||
		*got.Properties.Filter.SubjectBeginsWith != "orders/" {
		t.Fatalf("filter round-trip failed: %+v", got.Properties.Filter)
	}

	var names []string

	pager := subs.NewListPager(testRG, "orders", nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("list: %v", perr)
		}

		for _, s := range page.Value {
			names = append(names, *s.Name)
		}
	}

	if len(names) != 1 || names[0] != "sub1" {
		t.Fatalf("list = %v, want [sub1]", names)
	}

	delPoller, err := subs.BeginDelete(ctx, testRG, "orders", "sub1", nil)
	if err != nil {
		t.Fatalf("subscription BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("subscription delete PollUntilDone: %v", err)
	}
}

// TestSDKEventSubscriptionOnMissingTopic verifies the misrouting fix: a
// subscription PUT no longer silently upserts a topic.
func TestSDKEventSubscriptionOnMissingTopic(t *testing.T) {
	cf, _ := newEGFactory(t)
	subs := cf.NewTopicEventSubscriptionsClient()
	topics := cf.NewTopicsClient()
	ctx := context.Background()

	_, err := subs.BeginCreateOrUpdate(ctx, testRG, "no-such-topic", "sub1", armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{},
	}, nil)
	if err == nil {
		t.Fatal("expected error creating subscription on missing topic")
	}

	// The topic must NOT have been created as a side effect.
	if _, gerr := topics.Get(ctx, testRG, "no-such-topic", nil); gerr == nil {
		t.Fatal("subscription PUT must not create the topic")
	}
}
