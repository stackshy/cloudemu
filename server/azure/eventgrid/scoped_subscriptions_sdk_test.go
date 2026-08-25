package eventgrid_test

import (
	"context"
	"errors"
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

// newEventGridFactory builds one Event Grid backend behind a TLS server and
// returns a client factory so multiple clients (Topics + EventSubscriptions)
// share the same state.
func newEventGridFactory(t *testing.T) *armeventgrid.ClientFactory {
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

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armeventgrid.NewClientFactory(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("armeventgrid.NewClientFactory: %v", err)
	}

	return cf
}

// webhookSubscription builds an EventSubscription with a WebHook destination
// and a subject filter that must round-trip through GET.
func webhookSubscription(subjectPrefix string) armeventgrid.EventSubscription {
	return armeventgrid.EventSubscription{
		Properties: &armeventgrid.EventSubscriptionProperties{
			Destination: &armeventgrid.WebHookEventSubscriptionDestination{
				EndpointType: to.Ptr(armeventgrid.EndpointTypeWebHook),
				Properties: &armeventgrid.WebHookEventSubscriptionDestinationProperties{
					EndpointURL: to.Ptr("https://example.test/hook"),
				},
			},
			Filter: &armeventgrid.EventSubscriptionFilter{
				SubjectBeginsWith: to.Ptr(subjectPrefix),
			},
		},
	}
}

func createEventSubscription(t *testing.T, c *armeventgrid.EventSubscriptionsClient, scope, name, subjectPrefix string) {
	t.Helper()
	ctx := context.Background()

	poller, err := c.BeginCreateOrUpdate(ctx, scope, name, webhookSubscription(subjectPrefix), nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate(%s/%s): %v", scope, name, err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("CreateOrUpdate PollUntilDone(%s/%s): %v", scope, name, err)
	}
}

// TestSDKEventSubscriptionResourceGroupScope proves an RG-scoped event
// subscription (the Terraform azurerm_eventgrid_event_subscription shape) is no
// longer 501: it round-trips through create/get/list/delete.
func TestSDKEventSubscriptionResourceGroupScope(t *testing.T) {
	client := newEventGridFactory(t).NewEventSubscriptionsClient()
	ctx := context.Background()

	scope := "/subscriptions/" + testSub + "/resourceGroups/" + testRG

	createEventSubscription(t, client, scope, "rg-sub", "/blobServices/default")

	got, err := client.Get(ctx, scope, "rg-sub", nil)
	if err != nil {
		t.Fatalf("Get rg-scope sub: %v", err)
	}

	if got.Properties == nil || got.Properties.Filter == nil ||
		got.Properties.Filter.SubjectBeginsWith == nil ||
		*got.Properties.Filter.SubjectBeginsWith != "/blobServices/default" {
		t.Fatalf("filter did not round-trip: %+v", got.Properties)
	}

	names := listGlobalByResourceGroup(t, client, testRG)
	if len(names) != 1 || names[0] != "rg-sub" {
		t.Fatalf("ListGlobalByResourceGroup = %v, want [rg-sub]", names)
	}

	delPoller, err := client.BeginDelete(ctx, scope, "rg-sub", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}
	if _, err = delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}

	_, err = client.Get(ctx, scope, "rg-sub", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}

// TestSDKEventSubscriptionSubscriptionScope proves a subscription-scoped event
// subscription round-trips and is listed by ListGlobalBySubscription.
func TestSDKEventSubscriptionSubscriptionScope(t *testing.T) {
	client := newEventGridFactory(t).NewEventSubscriptionsClient()
	ctx := context.Background()

	scope := "/subscriptions/" + testSub

	createEventSubscription(t, client, scope, "sub-scope-sub", "/foo")

	if _, err := client.Get(ctx, scope, "sub-scope-sub", nil); err != nil {
		t.Fatalf("Get subscription-scope sub: %v", err)
	}

	var names []string
	pager := client.NewListGlobalBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListGlobalBySubscription: %v", err)
		}
		for _, es := range page.Value {
			names = append(names, *es.Name)
		}
	}

	if len(names) != 1 || names[0] != "sub-scope-sub" {
		t.Fatalf("ListGlobalBySubscription = %v, want [sub-scope-sub]", names)
	}
}

// TestSDKEventSubscriptionResourceScope proves the resource-extension form on an
// arbitrary (non-Event-Grid) resource works and is listed by ListByResource.
func TestSDKEventSubscriptionResourceScope(t *testing.T) {
	client := newEventGridFactory(t).NewEventSubscriptionsClient()
	ctx := context.Background()

	scope := "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.Storage/storageAccounts/acct1"

	createEventSubscription(t, client, scope, "res-sub", "/blobServices")

	if _, err := client.Get(ctx, scope, "res-sub", nil); err != nil {
		t.Fatalf("Get resource-scope sub: %v", err)
	}

	var names []string
	pager := client.NewListByResourcePager(testRG, "Microsoft.Storage", "storageAccounts", "acct1", nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListByResource: %v", err)
		}
		for _, es := range page.Value {
			names = append(names, *es.Name)
		}
	}

	if len(names) != 1 || names[0] != "res-sub" {
		t.Fatalf("ListByResource = %v, want [res-sub]", names)
	}
}

// TestSDKEventSubscriptionTopicExtensionForm proves the topic extension form
// (.../topics/{t}/providers/Microsoft.EventGrid/eventSubscriptions/{n}) is
// unified with the direct TopicEventSubscriptions form: a subscription created
// via the scope-bound client is visible through the topic's own list.
func TestSDKEventSubscriptionTopicExtensionForm(t *testing.T) {
	cf := newEventGridFactory(t)
	topics := cf.NewTopicsClient()
	subs := cf.NewEventSubscriptionsClient()
	topicSubs := cf.NewTopicEventSubscriptionsClient()
	ctx := context.Background()

	createTopic(t, topics, testRG, "orders", nil)

	scope := "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.EventGrid/topics/orders"

	createEventSubscription(t, subs, scope, "ext-sub", "/orders")

	// Visible through the direct topic-scoped client → same underlying resource.
	if _, err := topicSubs.Get(ctx, testRG, "orders", "ext-sub", nil); err != nil {
		t.Fatalf("TopicEventSubscriptions.Get(ext-sub): %v", err)
	}

	var names []string
	pager := topicSubs.NewListPager(testRG, "orders", nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("TopicEventSubscriptions.List: %v", err)
		}
		for _, es := range page.Value {
			names = append(names, *es.Name)
		}
	}

	if len(names) != 1 || names[0] != "ext-sub" {
		t.Fatalf("topic list = %v, want [ext-sub] (extension form must unify with direct form)", names)
	}
}

func listGlobalByResourceGroup(t *testing.T, c *armeventgrid.EventSubscriptionsClient, rg string) []string {
	t.Helper()
	ctx := context.Background()

	var names []string
	pager := c.NewListGlobalByResourceGroupPager(rg, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListGlobalByResourceGroup: %v", err)
		}
		for _, es := range page.Value {
			names = append(names, *es.Name)
		}
	}

	return names
}
