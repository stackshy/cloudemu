package azure_test

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

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	fpSubID  = "00000000-0000-0000-0000-000000000000"
	fpRGName = "rg-fp"
	fpNSName = "ns-fp"
)

// fakeCred is a static-token credential for tests.
type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// TestNewFromProvider verifies that a server assembled straight from a
// fully-constructed provider serves a real azure-sdk-for-go ARM call.
func TestNewFromProvider(t *testing.T) {
	p := cloudemu.NewAzure()

	srv := azureserver.NewFromProvider(p)
	if srv == nil {
		t.Fatal("NewFromProvider returned nil")
	}

	// The azure SDK refuses authenticated requests over plain HTTP, so the
	// endpoint must be TLS (mirrors the existing server/azure roundtrip tests).
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armservicebus.NewClientFactory(fpSubID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("ClientFactory: %v", err)
	}

	client := cf.NewNamespacesClient()
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, fpRGName, fpNSName,
		armservicebus.SBNamespace{
			Location: to.Ptr("eastus"),
			SKU: &armservicebus.SBSKU{
				Name: to.Ptr(armservicebus.SKUNameStandard),
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	if created.Name == nil || *created.Name != fpNSName {
		t.Fatalf("created.Name = %v, want %s", created.Name, fpNSName)
	}

	got, err := client.Get(ctx, fpRGName, fpNSName, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name == nil || *got.Name != fpNSName {
		t.Fatalf("got.Name = %v, want %s", got.Name, fpNSName)
	}
}
