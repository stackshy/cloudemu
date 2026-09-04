package eventhub_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventhub/armeventhub"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	subID  = "00000000-0000-0000-0000-000000000000"
	rgName = "rg-eh"
	nsName = "test-eh-ns"
	ehName = "orders"
)

// fakeCred is a static-token credential for tests.
type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func clientOpts(ts *httptest.Server) *arm.ClientOptions {
	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	return &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: myCloud, Transport: ts.Client(),
		Retry: policy.RetryOptions{MaxRetries: -1},
	}}
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{SubscriptionID: subID, IAM: cloudP.IAM})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

// TestSDKEventHubLifecycle drives the full Event Hubs control plane with the
// real armeventhub SDK: namespace create (poller must complete, not hang),
// event hub + consumer group create/list, authorization-rule listKeys round
// trip, and namespace delete.
func TestSDKEventHubLifecycle(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	nsClient, err := armeventhub.NewNamespacesClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewNamespacesClient: %v", err)
	}

	createNamespace(t, ctx, nsClient)
	assertNamespaceListed(t, ctx, nsClient)

	ehClient, err := armeventhub.NewEventHubsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewEventHubsClient: %v", err)
	}

	createEventHub(t, ctx, ehClient)
	assertEventHubListed(t, ctx, ehClient)

	cgClient, err := armeventhub.NewConsumerGroupsClient(subID, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewConsumerGroupsClient: %v", err)
	}

	createAndListConsumerGroups(t, ctx, cgClient)

	assertNamespaceKeys(t, ctx, nsClient)
	assertEventHubAuthRule(t, ctx, ehClient)

	deleteNamespace(t, ctx, nsClient)
}

func createNamespace(t *testing.T, ctx context.Context, c *armeventhub.NamespacesClient) {
	t.Helper()

	poller, err := c.BeginCreateOrUpdate(ctx, rgName, nsName, armeventhub.EHNamespace{
		Location: to.Ptr("eastus"),
		SKU:      &armeventhub.SKU{Name: to.Ptr(armeventhub.SKUNameStandard), Tier: to.Ptr(armeventhub.SKUTierStandard)},
		Tags:     map[string]*string{"env": to.Ptr("test")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate namespace: %v", err)
	}

	// PollUntilDone must terminate: the sync 201 body carries
	// provisioningState=Succeeded, so the poller does not hang.
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("poll namespace create: %v", err)
	}

	if res.Properties == nil || res.Properties.ProvisioningState == nil ||
		*res.Properties.ProvisioningState != "Succeeded" {
		t.Fatalf("namespace provisioningState = %v, want Succeeded", res.Properties)
	}

	got, err := c.Get(ctx, rgName, nsName, nil)
	if err != nil {
		t.Fatalf("Get namespace: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("namespace tags = %v, want env=test", got.Tags)
	}

	if got.Properties == nil || got.Properties.ServiceBusEndpoint == nil ||
		!strings.Contains(*got.Properties.ServiceBusEndpoint, nsName) {
		t.Fatalf("serviceBusEndpoint = %v, want it to contain %q", got.Properties, nsName)
	}
}

func assertNamespaceListed(t *testing.T, ctx context.Context, c *armeventhub.NamespacesClient) {
	t.Helper()

	pager := c.NewListByResourceGroupPager(rgName, nil)

	var names []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list namespaces: %v", err)
		}

		for _, ns := range page.Value {
			names = append(names, *ns.Name)
		}
	}

	if len(names) != 1 || names[0] != nsName {
		t.Fatalf("ListByResourceGroup = %v, want [%s]", names, nsName)
	}
}

func createEventHub(t *testing.T, ctx context.Context, c *armeventhub.EventHubsClient) {
	t.Helper()

	res, err := c.CreateOrUpdate(ctx, rgName, nsName, ehName, armeventhub.Eventhub{
		Properties: &armeventhub.Properties{
			PartitionCount:         to.Ptr[int64](4),
			MessageRetentionInDays: to.Ptr[int64](3),
		},
	}, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate event hub: %v", err)
	}

	got, err := c.Get(ctx, rgName, nsName, ehName, nil)
	if err != nil {
		t.Fatalf("Get event hub: %v", err)
	}

	if got.Properties == nil || got.Properties.PartitionCount == nil || *got.Properties.PartitionCount != 4 {
		t.Fatalf("partitionCount = %v, want 4", got.Properties)
	}

	if len(got.Properties.PartitionIDs) != 4 {
		t.Fatalf("partitionIds = %v, want 4 entries", got.Properties.PartitionIDs)
	}

	if res.Properties == nil || res.Properties.MessageRetentionInDays == nil ||
		*res.Properties.MessageRetentionInDays != 3 {
		t.Fatalf("messageRetentionInDays = %v, want 3", res.Properties)
	}
}

func assertEventHubListed(t *testing.T, ctx context.Context, c *armeventhub.EventHubsClient) {
	t.Helper()

	pager := c.NewListByNamespacePager(rgName, nsName, nil)

	var names []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list event hubs: %v", err)
		}

		for _, eh := range page.Value {
			names = append(names, *eh.Name)
		}
	}

	if len(names) != 1 || names[0] != ehName {
		t.Fatalf("ListByNamespace = %v, want [%s]", names, ehName)
	}
}

func createAndListConsumerGroups(t *testing.T, ctx context.Context, c *armeventhub.ConsumerGroupsClient) {
	t.Helper()

	if _, err := c.CreateOrUpdate(ctx, rgName, nsName, ehName, "workers", armeventhub.ConsumerGroup{
		Properties: &armeventhub.ConsumerGroupProperties{UserMetadata: to.Ptr("team-a")},
	}, nil); err != nil {
		t.Fatalf("CreateOrUpdate consumer group: %v", err)
	}

	got, err := c.Get(ctx, rgName, nsName, ehName, "workers", nil)
	if err != nil {
		t.Fatalf("Get consumer group: %v", err)
	}

	if got.Properties == nil || got.Properties.UserMetadata == nil || *got.Properties.UserMetadata != "team-a" {
		t.Fatalf("userMetadata = %v, want team-a", got.Properties)
	}

	pager := c.NewListByEventHubPager(rgName, nsName, ehName, nil)

	var names []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list consumer groups: %v", err)
		}

		for _, cg := range page.Value {
			names = append(names, *cg.Name)
		}
	}

	// The built-in $Default consumer group plus the one just created.
	if !contains(names, "$Default") || !contains(names, "workers") {
		t.Fatalf("consumer groups = %v, want $Default and workers", names)
	}
}

func assertNamespaceKeys(t *testing.T, ctx context.Context, c *armeventhub.NamespacesClient) {
	t.Helper()

	const rootRule = "RootManageSharedAccessKey"

	keys, err := c.ListKeys(ctx, rgName, nsName, rootRule, nil)
	if err != nil {
		t.Fatalf("ListKeys namespace: %v", err)
	}

	if keys.PrimaryKey == nil || *keys.PrimaryKey == "" {
		t.Fatalf("primaryKey empty, want a generated SAS key")
	}

	if keys.PrimaryConnectionString == nil || !strings.HasPrefix(*keys.PrimaryConnectionString, "Endpoint=sb://") {
		t.Fatalf("primaryConnectionString = %v, want Endpoint=sb:// prefix", keys.PrimaryConnectionString)
	}

	before := *keys.PrimaryKey

	regen, err := c.RegenerateKeys(ctx, rgName, nsName, rootRule, armeventhub.RegenerateAccessKeyParameters{
		KeyType: to.Ptr(armeventhub.KeyTypePrimaryKey),
	}, nil)
	if err != nil {
		t.Fatalf("RegenerateKeys: %v", err)
	}

	if regen.PrimaryKey == nil || *regen.PrimaryKey == before {
		t.Fatalf("regenerated primaryKey unchanged, want a fresh key")
	}
}

func assertEventHubAuthRule(t *testing.T, ctx context.Context, c *armeventhub.EventHubsClient) {
	t.Helper()

	const ruleName = "send-rule"

	if _, err := c.CreateOrUpdateAuthorizationRule(ctx, rgName, nsName, ehName, ruleName, armeventhub.AuthorizationRule{
		Properties: &armeventhub.AuthorizationRuleProperties{
			Rights: []*armeventhub.AccessRights{to.Ptr(armeventhub.AccessRightsSend)},
		},
	}, nil); err != nil {
		t.Fatalf("CreateOrUpdateAuthorizationRule event hub: %v", err)
	}

	keys, err := c.ListKeys(ctx, rgName, nsName, ehName, ruleName, nil)
	if err != nil {
		t.Fatalf("ListKeys event hub: %v", err)
	}

	// An event-hub-scoped rule's connection string carries the EntityPath.
	if keys.PrimaryConnectionString == nil || !strings.Contains(*keys.PrimaryConnectionString, "EntityPath="+ehName) {
		t.Fatalf("connection string = %v, want EntityPath=%s", keys.PrimaryConnectionString, ehName)
	}
}

func deleteNamespace(t *testing.T, ctx context.Context, c *armeventhub.NamespacesClient) {
	t.Helper()

	poller, err := c.BeginDelete(ctx, rgName, nsName, nil)
	if err != nil {
		t.Fatalf("BeginDelete namespace: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll namespace delete: %v", err)
	}

	if _, err := c.Get(ctx, rgName, nsName, nil); err == nil {
		t.Fatal("post-delete Get returned nil error, want NotFound")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}

	return false
}
