// Regression tests for two real-user e2e divergences found in a black-box
// audit against the real armeventgrid SDK:
//
//   - Domains.Update / SystemTopics.Update (PATCH) merged the supplied tags
//     onto the existing set instead of replacing it wholesale, diverging from
//     real Azure's resource-level tag PATCH semantics (Topics.Update's own
//     regression is covered in topic_network_regen_sdk_test.go).
//   - Topics.Delete / TopicEventSubscriptions.Delete were not idempotent on
//     an already-absent resource, diverging from real ARM and from every
//     other delete path in this package (domains, system topics, domain
//     topics, scoped/system-topic subscriptions).
package eventgrid_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

// TestSDKDomainUpdatePatchReplacesTags verifies Domains.BeginUpdate REPLACES
// the domain's tag set wholesale (a pre-existing tag absent from the body is
// dropped), matching real Azure and this codebase's tags-PATCH convention.
func TestSDKDomainUpdatePatchReplacesTags(t *testing.T) {
	cf, _ := newEGFactory(t)
	dc := cf.NewDomainsClient()
	ctx := context.Background()

	createPoller, err := dc.BeginCreateOrUpdate(ctx, testRG, "patch-domain", armeventgrid.Domain{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("test"), "owner": to.Ptr("alice")},
	}, nil)
	if err != nil {
		t.Fatalf("Domains.BeginCreateOrUpdate: %v", err)
	}

	if _, err := createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create PollUntilDone: %v", err)
	}

	updPoller, err := dc.BeginUpdate(ctx, testRG, "patch-domain", armeventgrid.DomainUpdateParameters{
		Tags: map[string]*string{"team": to.Ptr("data")},
	}, nil)
	if err != nil {
		t.Fatalf("Domains.BeginUpdate: %v", err)
	}

	updated, err := updPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("Update PollUntilDone: %v", err)
	}

	if _, ok := updated.Tags["env"]; ok {
		t.Fatalf("replace PATCH should have dropped omitted tags: %+v", updated.Tags)
	}

	if _, ok := updated.Tags["owner"]; ok {
		t.Fatalf("replace PATCH should have dropped omitted tags: %+v", updated.Tags)
	}

	if updated.Tags["team"] == nil || *updated.Tags["team"] != "data" {
		t.Fatalf("update missing new tag team: %+v", updated.Tags)
	}

	got, err := dc.Get(ctx, testRG, "patch-domain", nil)
	if err != nil {
		t.Fatalf("Domains.Get: %v", err)
	}

	if len(got.Tags) != 1 || got.Tags["team"] == nil || *got.Tags["team"] != "data" {
		t.Fatalf("Get after patch tags = %+v, want only {team: data}", got.Tags)
	}
}

// TestSDKSystemTopicUpdatePatchReplacesTags verifies SystemTopics.BeginUpdate
// REPLACES the system topic's tag set wholesale.
func TestSDKSystemTopicUpdatePatchReplacesTags(t *testing.T) {
	cf, _ := newEGFactory(t)
	st := cf.NewSystemTopicsClient()
	ctx := context.Background()

	const source = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Storage/storageAccounts/acct"

	createPoller, err := st.BeginCreateOrUpdate(ctx, testRG, "patch-systopic", armeventgrid.SystemTopic{
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

	if _, err := createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create PollUntilDone: %v", err)
	}

	updPoller, err := st.BeginUpdate(ctx, testRG, "patch-systopic", armeventgrid.SystemTopicUpdateParameters{
		Tags: map[string]*string{"team": to.Ptr("data")},
	}, nil)
	if err != nil {
		t.Fatalf("SystemTopics.BeginUpdate: %v", err)
	}

	updated, err := updPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("Update PollUntilDone: %v", err)
	}

	if _, ok := updated.Tags["env"]; ok {
		t.Fatalf("replace PATCH should have dropped omitted env tag: %+v", updated.Tags)
	}

	if updated.Tags["team"] == nil || *updated.Tags["team"] != "data" {
		t.Fatalf("update missing new tag team: %+v", updated.Tags)
	}

	got, err := st.Get(ctx, testRG, "patch-systopic", nil)
	if err != nil {
		t.Fatalf("SystemTopics.Get: %v", err)
	}

	if len(got.Tags) != 1 || got.Tags["team"] == nil || *got.Tags["team"] != "data" {
		t.Fatalf("Get after patch tags = %+v, want only {team: data}", got.Tags)
	}
}

// TestSDKTopicDeleteIdempotent verifies Topics.BeginDelete succeeds (not a
// 404) when the topic never existed, matching real ARM's idempotent delete
// semantics and every sibling delete path in this package.
func TestSDKTopicDeleteIdempotent(t *testing.T) {
	cf, _ := newEGFactory(t)
	topics := cf.NewTopicsClient()
	ctx := context.Background()

	poller, err := topics.BeginDelete(ctx, testRG, "never-existed-topic", nil)
	if err != nil {
		t.Fatalf("Topics.BeginDelete on a missing topic should not error: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete-missing PollUntilDone: %v", err)
	}

	// Deleting a topic that existed, then deleting it again, must also succeed.
	mkTopicLoc(t, topics, "delete-twice", "eastus")

	firstPoller, err := topics.BeginDelete(ctx, testRG, "delete-twice", nil)
	if err != nil {
		t.Fatalf("first Topics.BeginDelete: %v", err)
	}

	if _, err := firstPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("first delete PollUntilDone: %v", err)
	}

	secondPoller, err := topics.BeginDelete(ctx, testRG, "delete-twice", nil)
	if err != nil {
		t.Fatalf("second Topics.BeginDelete should not error: %v", err)
	}

	if _, err := secondPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("second delete PollUntilDone: %v", err)
	}
}

// TestSDKTopicEventSubscriptionDeleteIdempotent verifies
// TopicEventSubscriptions.BeginDelete succeeds when the subscription (or its
// topic) never existed, matching real ARM's idempotent delete semantics.
func TestSDKTopicEventSubscriptionDeleteIdempotent(t *testing.T) {
	cf, _ := newEGFactory(t)
	ctx := context.Background()

	topics := cf.NewTopicsClient()
	mkTopicLoc(t, topics, "sub-delete-topic", "eastus")

	subs := cf.NewTopicEventSubscriptionsClient()

	// Missing subscription on an existing topic.
	poller, err := subs.BeginDelete(ctx, testRG, "sub-delete-topic", "never-existed-sub", nil)
	if err != nil {
		t.Fatalf("BeginDelete on a missing subscription should not error: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete-missing-sub PollUntilDone: %v", err)
	}

	// Missing subscription on a topic that never existed either.
	poller2, err := subs.BeginDelete(ctx, testRG, "never-existed-topic-either", "whatever", nil)
	if err != nil {
		t.Fatalf("BeginDelete on a missing topic+subscription should not error: %v", err)
	}

	if _, err := poller2.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete-missing-topic-and-sub PollUntilDone: %v", err)
	}
}
