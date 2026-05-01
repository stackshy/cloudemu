package servicebus_test

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus/v2"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

// fakeCred is a static-token credential for tests.
type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newQueuesClient(t *testing.T, ts *httptest.Server) *armservicebus.QueuesClient {
	t.Helper()

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: myCloud, Transport: ts.Client(),
			Retry: policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armservicebus.NewClientFactory(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("ClientFactory: %v", err)
	}

	return cf.NewQueuesClient()
}

func TestSDKServiceBusQueueLifecycle(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{ServiceBus: cloudP.ServiceBus})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newQueuesClient(t, ts)
	ctx := context.Background()

	created, err := client.CreateOrUpdate(ctx, rgName, nsName, "sdk-q",
		armservicebus.SBQueue{
			Properties: &armservicebus.SBQueueProperties{
				MaxSizeInMegabytes: to.Ptr[int32](1024),
			},
		}, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}

	if created.Name == nil || *created.Name != "sdk-q" {
		t.Fatalf("created.Name = %v, want sdk-q", created.Name)
	}

	got, err := client.Get(ctx, rgName, nsName, "sdk-q", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name == nil || *got.Name != "sdk-q" {
		t.Fatalf("got.Name = %v, want sdk-q", got.Name)
	}

	if _, err := client.Delete(ctx, rgName, nsName, "sdk-q", nil); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := client.Get(ctx, rgName, nsName, "sdk-q", nil); err == nil {
		t.Fatal("post-delete Get returned nil error, want NotFound")
	}
}
