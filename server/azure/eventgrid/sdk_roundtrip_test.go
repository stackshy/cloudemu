package eventgrid_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

// ensureRG creates a resource group so tests can PUT resources into it. Real
// Azure requires the group to exist first (the emulator enforces this via a
// pre-dispatch gate), so tests must provision it before their resource ops.
func ensureRG(t *testing.T, ts *httptest.Server, sub, rg string) {
	t.Helper()

	url := ts.URL + "/subscriptions/" + sub + "/resourcegroups/" + rg + "?api-version=2021-04-01"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, url,
		strings.NewReader(`{"location":"eastus"}`))
	if err != nil {
		t.Fatalf("ensureRG new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("ensureRG PUT %s: %v", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("ensureRG %s: unexpected status %d", url, resp.StatusCode)
	}
}

func newTopicsClient(t *testing.T) (*armeventgrid.TopicsClient, *httptest.Server) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{EventGrid: cloudP.EventGrid})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ensureRG(t, ts, testSub, testRG)

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

	return cf.NewTopicsClient(), ts
}

func TestSDKAzureEventGridTopicLifecycle(t *testing.T) {
	topics, _ := newTopicsClient(t)
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
	topics, _ := newTopicsClient(t)
	ctx := context.Background()

	_, err := topics.Get(ctx, testRG, "missing", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get(missing): got %v, want 404", err)
	}
}
