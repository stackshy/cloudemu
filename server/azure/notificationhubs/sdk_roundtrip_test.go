package notificationhubs_test

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/notificationhubs/armnotificationhubs"

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

func newClients(t *testing.T) (*armnotificationhubs.NamespacesClient, *armnotificationhubs.Client) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{NotificationHubs: cloudP.NotificationHubs})

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

	cf, err := armnotificationhubs.NewClientFactory(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("armnotificationhubs.NewClientFactory: %v", err)
	}

	return cf.NewNamespacesClient(), cf.NewClient()
}

func TestSDKNotificationHubsNamespaceLifecycle(t *testing.T) {
	namespaces, _ := newClients(t)
	ctx := context.Background()

	created, err := namespaces.CreateOrUpdate(ctx, testRG, "my-ns", armnotificationhubs.NamespaceCreateOrUpdateParameters{
		Location: to.Ptr("global"),
		Tags:     map[string]*string{"env": to.Ptr("test")},
	}, nil)
	if err != nil {
		t.Fatalf("Namespaces.CreateOrUpdate: %v", err)
	}

	if created.Name == nil || *created.Name != "my-ns" {
		t.Fatalf("CreateOrUpdate name = %v, want my-ns", created.Name)
	}

	got, err := namespaces.Get(ctx, testRG, "my-ns", nil)
	if err != nil {
		t.Fatalf("Namespaces.Get: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("tags = %v, want env=test", got.Tags)
	}

	var names []string

	pager := namespaces.NewListPager(testRG, nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("Namespaces.List: %v", perr)
		}

		for _, ns := range page.Value {
			names = append(names, *ns.Name)
		}
	}

	if len(names) != 1 || names[0] != "my-ns" {
		t.Fatalf("list = %v, want [my-ns]", names)
	}

	poller, err := namespaces.BeginDelete(ctx, testRG, "my-ns", nil)
	if err != nil {
		t.Fatalf("Namespaces.BeginDelete: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}

	_, err = namespaces.Get(ctx, testRG, "my-ns", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}

func TestSDKNotificationHubsHubLifecycle(t *testing.T) {
	namespaces, hubs := newClients(t)
	ctx := context.Background()

	if _, err := namespaces.CreateOrUpdate(ctx, testRG, "hub-ns", armnotificationhubs.NamespaceCreateOrUpdateParameters{
		Location: to.Ptr("global"),
	}, nil); err != nil {
		t.Fatalf("Namespaces.CreateOrUpdate: %v", err)
	}

	created, err := hubs.CreateOrUpdate(ctx, testRG, "hub-ns", "my-hub", armnotificationhubs.NotificationHubCreateOrUpdateParameters{
		Location: to.Ptr("global"),
		Properties: &armnotificationhubs.NotificationHubProperties{
			Name:            to.Ptr("my-hub"),
			RegistrationTTL: to.Ptr("PT10M"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("Client.CreateOrUpdate (hub): %v", err)
	}

	if created.Name == nil || *created.Name != "my-hub" {
		t.Fatalf("hub CreateOrUpdate name = %v, want my-hub", created.Name)
	}

	got, err := hubs.Get(ctx, testRG, "hub-ns", "my-hub", nil)
	if err != nil {
		t.Fatalf("Client.Get (hub): %v", err)
	}

	if got.Name == nil || *got.Name != "my-hub" {
		t.Fatalf("hub Get name = %v, want my-hub", got.Name)
	}

	var listed []string

	pager := hubs.NewListPager(testRG, "hub-ns", nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("Client.List (hubs): %v", perr)
		}

		for _, h := range page.Value {
			listed = append(listed, *h.Name)
		}
	}

	if len(listed) != 1 || listed[0] != "my-hub" {
		t.Fatalf("hub list = %v, want [my-hub]", listed)
	}

	if _, err := hubs.Delete(ctx, testRG, "hub-ns", "my-hub", nil); err != nil {
		t.Fatalf("Client.Delete (hub): %v", err)
	}

	_, err = hubs.Get(ctx, testRG, "hub-ns", "my-hub", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("hub Get after delete: got %v, want 404", err)
	}
}

func TestSDKNotificationHubsErrors(t *testing.T) {
	namespaces, _ := newClients(t)
	ctx := context.Background()

	_, err := namespaces.Get(ctx, testRG, "missing", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get(missing): got %v, want 404", err)
	}
}
