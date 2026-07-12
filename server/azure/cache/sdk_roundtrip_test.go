package cache_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	testRG  = "rg-1"
	testSub = "sub-1"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newRedisClient(t *testing.T) *armredis.Client {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Cache: cloudP.Cache})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: ts.URL,
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armredis.NewClientFactory(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("armredis.NewClientFactory: %v", err)
	}

	return cf.NewClient()
}

func TestSDKAzureCacheLifecycle(t *testing.T) {
	client := newRedisClient(t)
	ctx := context.Background()

	poller, err := client.BeginCreate(ctx, testRG, "my-cache", armredis.CreateParameters{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("test")},
		Properties: &armredis.CreateProperties{
			SKU: &armredis.SKU{
				Name:     to.Ptr(armredis.SKUNameStandard),
				Family:   to.Ptr(armredis.SKUFamilyC),
				Capacity: to.Ptr(int32(1)),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("Create PollUntilDone: %v", err)
	}

	if created.Name == nil || *created.Name != "my-cache" {
		t.Fatalf("Create name = %v, want my-cache", created.Name)
	}

	got, err := client.Get(ctx, testRG, "my-cache", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("tags = %v, want env=test", got.Tags)
	}

	if got.Properties == nil || got.Properties.HostName == nil || *got.Properties.HostName == "" {
		t.Fatalf("expected hostName to be set, got %+v", got.Properties)
	}

	if got.Properties.ProvisioningState == nil || *got.Properties.ProvisioningState != armredis.ProvisioningStateSucceeded {
		t.Fatalf("provisioningState = %v, want Succeeded", got.Properties.ProvisioningState)
	}

	// List by subscription — should include the one cache.
	var names []string

	pager := client.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("ListBySubscription: %v", perr)
		}

		for _, c := range page.Value {
			names = append(names, *c.Name)
		}
	}

	if len(names) != 1 || names[0] != "my-cache" {
		t.Fatalf("list = %v, want [my-cache]", names)
	}

	delPoller, err := client.BeginDelete(ctx, testRG, "my-cache", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}

	_, err = client.Get(ctx, testRG, "my-cache", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}

func TestSDKAzureCacheNotFound(t *testing.T) {
	client := newRedisClient(t)

	_, err := client.Get(context.Background(), testRG, "missing", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get(missing): got %v, want 404", err)
	}
}
