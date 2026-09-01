package tags_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	cloudemu "github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	testSub   = "00000000-0000-0000-0000-000000000001"
	testScope = "subscriptions/" + testSub
)

// fakeCred is a static-token credential; the emulator ignores the header but the
// SDK still requires a credential implementation.
type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func armClientOptions(ts *httptest.Server) *arm.ClientOptions {
	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}
}

func newClient(t *testing.T) *armresources.TagsClient {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.DriversFrom(cloudP))

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client, err := armresources.NewTagsClient(testSub, fakeCred{}, armClientOptions(ts))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	return client
}

func tagsOf(props *armresources.Tags) map[string]string {
	out := map[string]string{}
	if props == nil {
		return out
	}

	for k, v := range props.Tags {
		if v != nil {
			out[k] = *v
		}
	}

	return out
}

func ptrs(m map[string]string) map[string]*string {
	out := make(map[string]*string, len(m))
	for k, v := range m {
		out[k] = to.Ptr(v)
	}

	return out
}

func assertTags(t *testing.T, label string, got *armresources.Tags, want map[string]string) {
	t.Helper()

	have := tagsOf(got)
	if len(have) != len(want) {
		t.Fatalf("%s: tags = %v, want %v", label, have, want)
	}

	for k, v := range want {
		if have[k] != v {
			t.Fatalf("%s: tag %q = %q, want %q (all: %v)", label, k, have[k], v, have)
		}
	}
}

// TestSDKTagsAtScopeLifecycle exercises the full armresources TagsClient surface
// against the emulator: PUT sets, GET returns, PATCH Merge/Replace/Delete apply
// their documented semantics, and DELETE clears.
func TestSDKTagsAtScopeLifecycle(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	// PUT sets the tag set.
	put, err := client.CreateOrUpdateAtScope(ctx, testScope, armresources.TagsResource{
		Properties: &armresources.Tags{Tags: ptrs(map[string]string{"env": "prod", "team": "core"})},
	}, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdateAtScope: %v", err)
	}

	assertTags(t, "put", put.Properties, map[string]string{"env": "prod", "team": "core"})

	if put.Name == nil || *put.Name != "default" {
		t.Errorf("put name = %v, want default", put.Name)
	}

	if put.Type == nil || *put.Type != "Microsoft.Resources/tags" {
		t.Errorf("put type = %v, want Microsoft.Resources/tags", put.Type)
	}

	// GET returns what PUT stored.
	got, err := client.GetAtScope(ctx, testScope, nil)
	if err != nil {
		t.Fatalf("GetAtScope: %v", err)
	}

	assertTags(t, "get", got.Properties, map[string]string{"env": "prod", "team": "core"})

	// PATCH Merge adds a key and overwrites an existing one, keeping the rest.
	merged, err := client.UpdateAtScope(ctx, testScope, armresources.TagsPatchResource{
		Operation:  to.Ptr(armresources.TagsPatchOperationMerge),
		Properties: &armresources.Tags{Tags: ptrs(map[string]string{"team": "platform", "cost": "eng"})},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateAtScope Merge: %v", err)
	}

	assertTags(t, "merge", merged.Properties, map[string]string{"env": "prod", "team": "platform", "cost": "eng"})

	// PATCH Delete removes the named tags regardless of the supplied value; keys
	// not named remain. Here "env" is deleted even though the value is wrong,
	// while "team" (unnamed) survives.
	deleted, err := client.UpdateAtScope(ctx, testScope, armresources.TagsPatchResource{
		Operation:  to.Ptr(armresources.TagsPatchOperationDelete),
		Properties: &armresources.Tags{Tags: ptrs(map[string]string{"cost": "eng", "env": "wrong-value"})},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateAtScope Delete: %v", err)
	}

	assertTags(t, "delete-op", deleted.Properties, map[string]string{"team": "platform"})

	// PATCH Replace swaps the whole set.
	replaced, err := client.UpdateAtScope(ctx, testScope, armresources.TagsPatchResource{
		Operation:  to.Ptr(armresources.TagsPatchOperationReplace),
		Properties: &armresources.Tags{Tags: ptrs(map[string]string{"only": "one"})},
	}, nil)
	if err != nil {
		t.Fatalf("UpdateAtScope Replace: %v", err)
	}

	assertTags(t, "replace", replaced.Properties, map[string]string{"only": "one"})

	// DELETE clears the set; a subsequent GET returns an empty set.
	if _, err := client.DeleteAtScope(ctx, testScope, nil); err != nil {
		t.Fatalf("DeleteAtScope: %v", err)
	}

	cleared, err := client.GetAtScope(ctx, testScope, nil)
	if err != nil {
		t.Fatalf("GetAtScope after delete: %v", err)
	}

	assertTags(t, "cleared", cleared.Properties, map[string]string{})
}

// TestSDKTagsAtScopeIsolatedByScope verifies two scopes keep independent tag
// sets — a resource-id scope and a subscription scope do not bleed into each
// other.
func TestSDKTagsAtScopeIsolatedByScope(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	const resScope = "subscriptions/" + testSub +
		"/resourceGroups/rg-1/providers/Microsoft.Storage/storageAccounts/acct1"

	if _, err := client.CreateOrUpdateAtScope(ctx, testScope, armresources.TagsResource{
		Properties: &armresources.Tags{Tags: ptrs(map[string]string{"level": "sub"})},
	}, nil); err != nil {
		t.Fatalf("put sub scope: %v", err)
	}

	if _, err := client.CreateOrUpdateAtScope(ctx, resScope, armresources.TagsResource{
		Properties: &armresources.Tags{Tags: ptrs(map[string]string{"level": "resource"})},
	}, nil); err != nil {
		t.Fatalf("put resource scope: %v", err)
	}

	subGot, err := client.GetAtScope(ctx, testScope, nil)
	if err != nil {
		t.Fatalf("get sub scope: %v", err)
	}

	assertTags(t, "sub-scope", subGot.Properties, map[string]string{"level": "sub"})

	resGot, err := client.GetAtScope(ctx, resScope, nil)
	if err != nil {
		t.Fatalf("get resource scope: %v", err)
	}

	assertTags(t, "resource-scope", resGot.Properties, map[string]string{"level": "resource"})
}
