package servicebus_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus/v2"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func newClientFactory(t *testing.T, ts *httptest.Server) *armservicebus.ClientFactory {
	t.Helper()

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}
	opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: myCloud, Transport: ts.Client(),
		Retry: policy.RetryOptions{MaxRetries: -1},
	}}

	cf, err := armservicebus.NewClientFactory(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("ClientFactory: %v", err)
	}

	return cf
}

func pubsubServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{ServiceBus: cloudP.ServiceBus}))
	t.Cleanup(ts.Close)

	return ts
}

// TestSDKTopicSubscriptionRule drives the full pub/sub tree with the real
// armservicebus SDK: topic -> subscription (with its default rule) -> custom
// SQL rule.
func TestSDKTopicSubscriptionRule(t *testing.T) {
	ts := pubsubServer(t)
	cf := newClientFactory(t, ts)
	ctx := context.Background()

	createNS(t, cf.NewNamespacesClient(), rgName, nsName, nil)

	topics := cf.NewTopicsClient()

	topic, err := topics.CreateOrUpdate(ctx, rgName, nsName, "orders", armservicebus.SBTopic{
		Properties: &armservicebus.SBTopicProperties{EnablePartitioning: to.Ptr(true)},
	}, nil)
	if err != nil {
		t.Fatalf("topic CreateOrUpdate: %v", err)
	}

	if topic.Name == nil || *topic.Name != "orders" {
		t.Fatalf("topic.Name = %v, want orders", topic.Name)
	}

	if topic.Properties == nil || topic.Properties.Status == nil ||
		*topic.Properties.Status != armservicebus.EntityStatusActive {
		t.Fatalf("topic status = %v, want Active", topic.Properties)
	}

	subs := cf.NewSubscriptionsClient()

	sub, err := subs.CreateOrUpdate(ctx, rgName, nsName, "orders", "all", armservicebus.SBSubscription{
		Properties: &armservicebus.SBSubscriptionProperties{MaxDeliveryCount: to.Ptr[int32](7)},
	}, nil)
	if err != nil {
		t.Fatalf("subscription CreateOrUpdate: %v", err)
	}

	if sub.Properties == nil || sub.Properties.MaxDeliveryCount == nil ||
		*sub.Properties.MaxDeliveryCount != 7 {
		t.Fatalf("sub MaxDeliveryCount = %v, want 7", sub.Properties)
	}

	if sub.Properties.LockDuration == nil || *sub.Properties.LockDuration != "PT1M" {
		t.Fatalf("sub LockDuration = %v, want PT1M default", sub.Properties.LockDuration)
	}

	// A freshly created subscription carries the $Default rule.
	rules := cf.NewRulesClient()

	if _, err := rules.Get(ctx, rgName, nsName, "orders", "all", "$Default", nil); err != nil {
		t.Fatalf("$Default rule Get: %v", err)
	}

	made, err := rules.CreateOrUpdate(ctx, rgName, nsName, "orders", "all", "high", armservicebus.Rule{
		Properties: &armservicebus.Ruleproperties{
			FilterType: to.Ptr(armservicebus.FilterTypeSQLFilter),
			SQLFilter:  &armservicebus.SQLFilter{SQLExpression: to.Ptr("priority > 5")},
		},
	}, nil)
	if err != nil {
		t.Fatalf("rule CreateOrUpdate: %v", err)
	}

	if made.Properties == nil || made.Properties.SQLFilter == nil ||
		made.Properties.SQLFilter.SQLExpression == nil ||
		*made.Properties.SQLFilter.SQLExpression != "priority > 5" {
		t.Fatalf("rule sqlExpression = %v", made.Properties)
	}

	// CompatibilityLevel is server-populated to 20.
	if made.Properties.SQLFilter.CompatibilityLevel == nil ||
		*made.Properties.SQLFilter.CompatibilityLevel != 20 {
		t.Fatalf("rule compatibilityLevel = %v, want 20", made.Properties.SQLFilter.CompatibilityLevel)
	}

	if _, err := subs.Get(ctx, rgName, nsName, "orders", "all", nil); err != nil {
		t.Fatalf("subscription Get: %v", err)
	}

	if _, err := topics.Delete(ctx, rgName, nsName, "orders", nil); err != nil {
		t.Fatalf("topic Delete: %v", err)
	}

	if _, err := topics.Get(ctx, rgName, nsName, "orders", nil); err == nil {
		t.Fatal("topic Get after delete = nil error, want NotFound")
	}
}

// TestSDKAuthorizationRulesAndKeys covers the default RootManageSharedAccessKey,
// listKeys, a custom rule, and regenerateKeys.
func TestSDKAuthorizationRulesAndKeys(t *testing.T) {
	ts := pubsubServer(t)
	cf := newClientFactory(t, ts)
	ctx := context.Background()

	nsc := cf.NewNamespacesClient()
	createNS(t, nsc, rgName, nsName, nil)

	// The default rule exists right after namespace creation.
	keys, err := nsc.ListKeys(ctx, rgName, nsName, "RootManageSharedAccessKey", nil)
	if err != nil {
		t.Fatalf("ListKeys default rule: %v", err)
	}

	if keys.PrimaryConnectionString == nil ||
		!strings.Contains(*keys.PrimaryConnectionString, "Endpoint=sb://"+nsName) {
		t.Fatalf("primary connection string = %v", keys.PrimaryConnectionString)
	}

	if keys.PrimaryKey == nil || *keys.PrimaryKey == "" {
		t.Fatal("primary key empty")
	}

	// Create a Send-only rule.
	if _, err := nsc.CreateOrUpdateAuthorizationRule(ctx, rgName, nsName, "sender",
		armservicebus.SBAuthorizationRule{
			Properties: &armservicebus.SBAuthorizationRuleProperties{
				Rights: []*armservicebus.AccessRights{to.Ptr(armservicebus.AccessRightsSend)},
			},
		}, nil); err != nil {
		t.Fatalf("create auth rule: %v", err)
	}

	got, err := nsc.GetAuthorizationRule(ctx, rgName, nsName, "sender", nil)
	if err != nil {
		t.Fatalf("get auth rule: %v", err)
	}

	if got.Properties == nil || len(got.Properties.Rights) != 1 ||
		*got.Properties.Rights[0] != armservicebus.AccessRightsSend {
		t.Fatalf("rights = %v, want [Send]", got.Properties)
	}

	before, err := nsc.ListKeys(ctx, rgName, nsName, "sender", nil)
	if err != nil {
		t.Fatalf("listkeys sender: %v", err)
	}

	regen, err := nsc.RegenerateKeys(ctx, rgName, nsName, "sender",
		armservicebus.RegenerateAccessKeyParameters{KeyType: to.Ptr(armservicebus.KeyTypePrimaryKey)}, nil)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}

	if regen.PrimaryKey == nil || *regen.PrimaryKey == *before.PrimaryKey {
		t.Fatal("primary key did not change after regenerate")
	}

	if regen.SecondaryKey == nil || *regen.SecondaryKey != *before.SecondaryKey {
		t.Fatal("secondary key must be unchanged when regenerating the primary")
	}
}

// TestSDKNamespaceUpdateAndCheckName covers PATCH update and the provider-level
// CheckNameAvailability action.
func TestSDKNamespaceUpdateAndCheckName(t *testing.T) {
	ts := pubsubServer(t)
	cf := newClientFactory(t, ts)
	ctx := context.Background()

	nsc := cf.NewNamespacesClient()

	avail, err := nsc.CheckNameAvailability(ctx,
		armservicebus.CheckNameAvailability{Name: to.Ptr(nsName)}, nil)
	if err != nil {
		t.Fatalf("CheckNameAvailability: %v", err)
	}

	if avail.NameAvailable == nil || !*avail.NameAvailable {
		t.Fatalf("expected %s available before create", nsName)
	}

	createNS(t, nsc, rgName, nsName, nil)

	taken, err := nsc.CheckNameAvailability(ctx,
		armservicebus.CheckNameAvailability{Name: to.Ptr(nsName)}, nil)
	if err != nil {
		t.Fatalf("CheckNameAvailability post-create: %v", err)
	}

	if taken.NameAvailable == nil || *taken.NameAvailable {
		t.Fatalf("expected %s unavailable after create", nsName)
	}

	// Default SKU is Standard even though create sent none.
	got, err := nsc.Get(ctx, rgName, nsName, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.SKU == nil || got.SKU.Name == nil || *got.SKU.Name != armservicebus.SKUNameStandard {
		t.Fatalf("default SKU = %v, want Standard", got.SKU)
	}

	if got.SystemData == nil || got.SystemData.CreatedAt == nil {
		t.Fatal("systemData/createdAt not populated")
	}

	updated, err := nsc.Update(ctx, rgName, nsName, armservicebus.SBNamespaceUpdateParameters{
		Tags: map[string]*string{"team": to.Ptr("payments")},
	}, nil)
	if err != nil {
		t.Fatalf("Update (PATCH): %v", err)
	}

	if updated.Tags["team"] == nil || *updated.Tags["team"] != "payments" {
		t.Fatalf("PATCH tags = %v, want team=payments", updated.Tags)
	}
}

// TestSDKQueueNamespaceIsolationAndCascade verifies a queue in one namespace is
// invisible from another (#4) and that deleting a namespace cascades to its
// child queues (#7).
func TestSDKQueueNamespaceIsolationAndCascade(t *testing.T) {
	ts := pubsubServer(t)
	cf := newClientFactory(t, ts)
	ctx := context.Background()

	nsc := cf.NewNamespacesClient()
	createNS(t, nsc, rgName, "ns-one", nil)
	createNS(t, nsc, rgName, "ns-two", nil)

	queues := cf.NewQueuesClient()

	if _, err := queues.CreateOrUpdate(ctx, rgName, "ns-one", "shared",
		armservicebus.SBQueue{}, nil); err != nil {
		t.Fatalf("create queue in ns-one: %v", err)
	}

	// The same queue name in ns-two must not resolve.
	if _, err := queues.Get(ctx, rgName, "ns-two", "shared", nil); err == nil {
		t.Fatal("queue 'shared' leaked across namespaces (ns-two Get succeeded)")
	}

	if _, err := queues.Get(ctx, rgName, "ns-one", "shared", nil); err != nil {
		t.Fatalf("queue Get in ns-one: %v", err)
	}

	// Deleting ns-one cascades: the child queue disappears.
	delPoller, err := nsc.BeginDelete(ctx, rgName, "ns-one", nil)
	if err != nil {
		t.Fatalf("BeginDelete ns-one: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete ns-one: %v", err)
	}

	// Re-create the namespace; the queue must be gone (state cascaded away).
	createNS(t, nsc, rgName, "ns-one", nil)

	if _, err := queues.Get(ctx, rgName, "ns-one", "shared", nil); err == nil {
		t.Fatal("child queue survived namespace delete (no cascade)")
	}
}

// TestSDKQueueUnderMissingNamespace covers #6: a child create under a
// never-created namespace is a 404.
func TestSDKQueueUnderMissingNamespace(t *testing.T) {
	ts := pubsubServer(t)
	cf := newClientFactory(t, ts)
	ctx := context.Background()

	queues := cf.NewQueuesClient()

	if _, err := queues.CreateOrUpdate(ctx, rgName, "ghost-ns", "q",
		armservicebus.SBQueue{}, nil); err == nil {
		t.Fatal("queue create under missing namespace succeeded, want NotFound")
	}
}
