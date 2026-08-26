package eventgrid_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

// TestSDKTopicPublicNetworkAccessRoundTrips locks B1: a topic created with
// PublicNetworkAccess=Disabled must echo Disabled on both the create response
// and a subsequent Get, not the hardcoded Enabled default.
func TestSDKTopicPublicNetworkAccessRoundTrips(t *testing.T) {
	cf, _ := newEGFactory(t)
	topics := cf.NewTopicsClient()
	ctx := context.Background()

	poller, err := topics.BeginCreateOrUpdate(ctx, testRG, "private-topic", armeventgrid.Topic{
		Location: to.Ptr("eastus"),
		Properties: &armeventgrid.TopicProperties{
			PublicNetworkAccess: to.Ptr(armeventgrid.PublicNetworkAccessDisabled),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	if created.Properties == nil || created.Properties.PublicNetworkAccess == nil ||
		*created.Properties.PublicNetworkAccess != armeventgrid.PublicNetworkAccessDisabled {
		t.Fatalf("create response publicNetworkAccess = %v, want Disabled", created.Properties)
	}

	got, err := topics.Get(ctx, testRG, "private-topic", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.PublicNetworkAccess == nil ||
		*got.Properties.PublicNetworkAccess != armeventgrid.PublicNetworkAccessDisabled {
		t.Fatalf("Get publicNetworkAccess = %v, want Disabled", got.Properties)
	}
}

// TestSDKTopicPublicNetworkAccessDefaultsToEnabled covers the unset case: a
// topic created without PublicNetworkAccess still gets the real default.
func TestSDKTopicPublicNetworkAccessDefaultsToEnabled(t *testing.T) {
	cf, _ := newEGFactory(t)
	topics := cf.NewTopicsClient()
	ctx := context.Background()

	poller, err := topics.BeginCreateOrUpdate(ctx, testRG, "public-topic", armeventgrid.Topic{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	if created.Properties == nil || created.Properties.PublicNetworkAccess == nil ||
		*created.Properties.PublicNetworkAccess != armeventgrid.PublicNetworkAccessEnabled {
		t.Fatalf("publicNetworkAccess = %v, want Enabled default", created.Properties)
	}
}

// TestSDKTopicUpdatePatchMergesTags locks B2: Topics.BeginUpdate (PATCH)
// succeeds, merges the supplied tags onto the existing ones, and applies the
// mutable publicNetworkAccess.
func TestSDKTopicUpdatePatchMergesTags(t *testing.T) {
	cf, _ := newEGFactory(t)
	topics := cf.NewTopicsClient()
	ctx := context.Background()

	createPoller, err := topics.BeginCreateOrUpdate(ctx, testRG, "patch-topic", armeventgrid.Topic{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("test")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create PollUntilDone: %v", err)
	}

	updPoller, err := topics.BeginUpdate(ctx, testRG, "patch-topic", armeventgrid.TopicUpdateParameters{
		Tags: map[string]*string{"team": to.Ptr("data")},
		Properties: &armeventgrid.TopicUpdateParameterProperties{
			PublicNetworkAccess: to.Ptr(armeventgrid.PublicNetworkAccessDisabled),
		},
	}, nil)
	if err != nil {
		t.Fatalf("Topics.BeginUpdate: %v", err)
	}

	updated, err := updPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("Update PollUntilDone: %v", err)
	}

	if updated.Tags["env"] == nil || *updated.Tags["env"] != "test" {
		t.Fatalf("update dropped existing tag env: %+v", updated.Tags)
	}

	if updated.Tags["team"] == nil || *updated.Tags["team"] != "data" {
		t.Fatalf("update missing new tag team: %+v", updated.Tags)
	}

	got, err := topics.Get(ctx, testRG, "patch-topic", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Tags["env"] == nil || got.Tags["team"] == nil {
		t.Fatalf("Get after patch missing merged tags: %+v", got.Tags)
	}

	if got.Properties == nil || got.Properties.PublicNetworkAccess == nil ||
		*got.Properties.PublicNetworkAccess != armeventgrid.PublicNetworkAccessDisabled {
		t.Fatalf("patch did not apply publicNetworkAccess: %v", got.Properties)
	}
}

// TestSDKTopicRegenerateKey locks B3: BeginRegenerateKey("key1") changes key1
// but not key2, and ListSharedAccessKeys reflects the rotated value.
func TestSDKTopicRegenerateKey(t *testing.T) {
	cf, _ := newEGFactory(t)
	topics := cf.NewTopicsClient()
	ctx := context.Background()

	mkTopicLoc(t, topics, "regen-topic", "eastus")

	before, err := topics.ListSharedAccessKeys(ctx, testRG, "regen-topic", nil)
	if err != nil {
		t.Fatalf("ListSharedAccessKeys: %v", err)
	}

	if before.Key1 == nil || before.Key2 == nil || *before.Key1 == "" || *before.Key2 == "" {
		t.Fatalf("initial keys empty: %+v", before)
	}

	regenPoller, err := topics.BeginRegenerateKey(ctx, testRG, "regen-topic", armeventgrid.TopicRegenerateKeyRequest{
		KeyName: to.Ptr("key1"),
	}, nil)
	if err != nil {
		t.Fatalf("BeginRegenerateKey: %v", err)
	}

	regen, err := regenPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("RegenerateKey PollUntilDone: %v", err)
	}

	if *regen.Key1 == *before.Key1 {
		t.Fatalf("key1 did not change after regenerate: %q", *regen.Key1)
	}

	if *regen.Key2 != *before.Key2 {
		t.Fatalf("key2 changed on key1 regenerate: %q vs %q", *regen.Key2, *before.Key2)
	}

	after, err := topics.ListSharedAccessKeys(ctx, testRG, "regen-topic", nil)
	if err != nil {
		t.Fatalf("ListSharedAccessKeys after regen: %v", err)
	}

	if *after.Key1 != *regen.Key1 {
		t.Fatalf("listKeys key1 = %q, want rotated %q", *after.Key1, *regen.Key1)
	}

	if *after.Key2 != *before.Key2 {
		t.Fatalf("listKeys key2 = %q, want unchanged %q", *after.Key2, *before.Key2)
	}
}
