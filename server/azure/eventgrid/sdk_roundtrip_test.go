package eventgrid_test

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"

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

func newTopicsClient(t *testing.T) *armeventgrid.TopicsClient {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{EventGrid: cloudP.EventGrid})

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

	cf, err := armeventgrid.NewClientFactory(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("armeventgrid.NewClientFactory: %v", err)
	}

	return cf.NewTopicsClient()
}

func TestSDKAzureEventGridTopicLifecycle(t *testing.T) {
	topics := newTopicsClient(t)
	ctx := context.Background()

	createPoller, err := topics.BeginCreateOrUpdate(ctx, testRG, "orders-topic", armeventgrid.Topic{
		Location: to.Ptr("global"),
		Tags:     map[string]*string{"env": to.Ptr("test")},
	}, nil)
	if err != nil {
		t.Fatalf("Topics.BeginCreateOrUpdate: %v", err)
	}

	created, err := createPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate PollUntilDone: %v", err)
	}

	if created.Name == nil || *created.Name != "orders-topic" {
		t.Fatalf("CreateOrUpdate name = %v, want orders-topic", created.Name)
	}

	got, err := topics.Get(ctx, testRG, "orders-topic", nil)
	if err != nil {
		t.Fatalf("Topics.Get: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("tags = %v, want env=test", got.Tags)
	}

	var names []string

	pager := topics.NewListByResourceGroupPager(testRG, nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("ListByResourceGroup: %v", perr)
		}

		for _, tp := range page.Value {
			names = append(names, *tp.Name)
		}
	}

	if len(names) != 1 || names[0] != "orders-topic" {
		t.Fatalf("list = %v, want [orders-topic]", names)
	}

	delPoller, err := topics.BeginDelete(ctx, testRG, "orders-topic", nil)
	if err != nil {
		t.Fatalf("Topics.BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}

	_, err = topics.Get(ctx, testRG, "orders-topic", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}

func TestSDKAzureEventGridErrors(t *testing.T) {
	topics := newTopicsClient(t)
	ctx := context.Background()

	_, err := topics.Get(ctx, testRG, "missing", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get(missing): got %v, want 404", err)
	}
}
