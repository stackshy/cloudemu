// Real-user tests for the Event Grid system-topic and domain ARM surfaces,
// driving the official armeventgrid SDK clients against the CloudEmu Azure
// server mounted in an httptest TLS server.
package eventgrid_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

// TestSDKSystemTopicLifecycle walks CreateOrUpdate → Get → list (by RG and by
// subscription) → Delete through the SystemTopicsClient.
func TestSDKSystemTopicLifecycle(t *testing.T) {
	cf, _ := newEGFactory(t)
	st := cf.NewSystemTopicsClient()
	ctx := context.Background()

	const source = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Storage/storageAccounts/acct"

	poller, err := st.BeginCreateOrUpdate(ctx, testRG, "storage-events", armeventgrid.SystemTopic{
		Location: to.Ptr("global"),
		Tags:     map[string]*string{"env": to.Ptr("test")},
		Properties: &armeventgrid.SystemTopicProperties{
			Source:    to.Ptr(source),
			TopicType: to.Ptr("Microsoft.Storage.StorageAccounts"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("SystemTopics.BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("SystemTopics create PollUntilDone: %v", err)
	}

	if created.Properties == nil || created.Properties.Source == nil || *created.Properties.Source != source {
		t.Fatalf("created source round-trip failed: %+v", created.Properties)
	}

	if created.Properties.ProvisioningState == nil || *created.Properties.ProvisioningState != armeventgrid.ResourceProvisioningStateSucceeded {
		t.Fatalf("provisioningState = %v, want Succeeded", created.Properties.ProvisioningState)
	}

	got, err := st.Get(ctx, testRG, "storage-events", nil)
	if err != nil {
		t.Fatalf("SystemTopics.Get: %v", err)
	}

	if got.Properties == nil || got.Properties.TopicType == nil ||
		*got.Properties.TopicType != "Microsoft.Storage.StorageAccounts" {
		t.Fatalf("topicType round-trip failed: %+v", got.Properties)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("tags round-trip failed: %+v", got.Tags)
	}

	// List by resource group.
	var byRG []string

	rgPager := st.NewListByResourceGroupPager(testRG, nil)
	for rgPager.More() {
		page, perr := rgPager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("ListByResourceGroup: %v", perr)
		}

		for _, s := range page.Value {
			byRG = append(byRG, *s.Name)
		}
	}

	if len(byRG) != 1 || byRG[0] != "storage-events" {
		t.Fatalf("ListByResourceGroup = %v, want [storage-events]", byRG)
	}

	// List by subscription.
	var bySub []string

	subPager := st.NewListBySubscriptionPager(nil)
	for subPager.More() {
		page, perr := subPager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("ListBySubscription: %v", perr)
		}

		for _, s := range page.Value {
			bySub = append(bySub, *s.Name)
		}
	}

	if len(bySub) != 1 || bySub[0] != "storage-events" {
		t.Fatalf("ListBySubscription = %v, want [storage-events]", bySub)
	}

	delPoller, err := st.BeginDelete(ctx, testRG, "storage-events", nil)
	if err != nil {
		t.Fatalf("SystemTopics.BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("SystemTopics delete PollUntilDone: %v", err)
	}

	if _, err := st.Get(ctx, testRG, "storage-events", nil); err == nil {
		t.Fatal("Get after delete: expected error, got nil")
	}
}

// TestSDKSystemTopicEventSubscription drives the SystemTopicEventSubscriptions
// client: create a subscription with a webhook destination and filter, read it
// back, list it, then delete it.
func TestSDKSystemTopicEventSubscription(t *testing.T) {
	cf, _ := newEGFactory(t)
	st := cf.NewSystemTopicsClient()
	subs := cf.NewSystemTopicEventSubscriptionsClient()
	ctx := context.Background()

	mkSystemTopic(ctx, t, st, "storage-events")

	sub := armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.WebHookEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeWebHook),
				Properties: &armeventgrid.WebHookEventSubscriptionDestinationProperties{
					EndpointURL: to.Ptr("https://example.com/hook"),
				},
			},
			Filter: &armeventgrid.EventSubscriptionFilter{
				SubjectBeginsWith: to.Ptr("blobs/"),
			},
		},
	}

	poller, err := subs.BeginCreateOrUpdate(ctx, testRG, "storage-events", "to-fn", sub, nil)
	if err != nil {
		t.Fatalf("subscription BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("subscription PollUntilDone: %v", err)
	}

	got, err := subs.Get(ctx, testRG, "storage-events", "to-fn", nil)
	if err != nil {
		t.Fatalf("subscription Get: %v", err)
	}

	dest, ok := got.Properties.Destination.(*armeventgrid.WebHookEventSubscriptionDestination)
	if !ok {
		t.Fatalf("destination type = %T, want WebHook", got.Properties.Destination)
	}

	if dest.Properties == nil || dest.Properties.EndpointURL == nil ||
		*dest.Properties.EndpointURL != "https://example.com/hook" {
		t.Fatalf("endpointUrl round-trip failed: %+v", dest.Properties)
	}

	if got.Properties.Filter == nil || got.Properties.Filter.SubjectBeginsWith == nil ||
		*got.Properties.Filter.SubjectBeginsWith != "blobs/" {
		t.Fatalf("filter round-trip failed: %+v", got.Properties.Filter)
	}

	var names []string

	pager := subs.NewListBySystemTopicPager(testRG, "storage-events", nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("list: %v", perr)
		}

		for _, s := range page.Value {
			names = append(names, *s.Name)
		}
	}

	if len(names) != 1 || names[0] != "to-fn" {
		t.Fatalf("list = %v, want [to-fn]", names)
	}

	delPoller, err := subs.BeginDelete(ctx, testRG, "storage-events", "to-fn", nil)
	if err != nil {
		t.Fatalf("subscription BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("subscription delete PollUntilDone: %v", err)
	}
}

// TestSDKSystemTopicEventSubscriptionOnMissingTopic asserts a subscription
// create against a system topic that does not exist 404s.
func TestSDKSystemTopicEventSubscriptionOnMissingTopic(t *testing.T) {
	cf, _ := newEGFactory(t)
	subs := cf.NewSystemTopicEventSubscriptionsClient()
	ctx := context.Background()

	_, err := subs.BeginCreateOrUpdate(ctx, testRG, "no-such-topic", "s1", armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{},
	}, nil)
	if err == nil {
		t.Fatal("expected 404 creating subscription on missing system topic, got nil")
	}
}

// mkSystemTopic creates a system topic and waits for the LRO to complete.
func mkSystemTopic(ctx context.Context, t *testing.T, st *armeventgrid.SystemTopicsClient, name string) {
	t.Helper()

	poller, err := st.BeginCreateOrUpdate(ctx, testRG, name, armeventgrid.SystemTopic{
		Location: to.Ptr("global"),
		Properties: &armeventgrid.SystemTopicProperties{
			Source:    to.Ptr("/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Storage/storageAccounts/acct"),
			TopicType: to.Ptr("Microsoft.Storage.StorageAccounts"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("mkSystemTopic BeginCreateOrUpdate: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("mkSystemTopic PollUntilDone: %v", err)
	}
}
