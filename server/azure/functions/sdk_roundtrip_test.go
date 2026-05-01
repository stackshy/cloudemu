package functions_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v3"
	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

// fakeCred is a static-token credential for tests. The handler ignores the
// Authorization header but the SDK still demands a TokenCredential.
type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newWebAppsClient(t *testing.T, ts *httptest.Server) *armappservice.WebAppsClient {
	t.Helper()

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

	clientFactory, err := armappservice.NewClientFactory(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewClientFactory: %v", err)
	}

	return clientFactory.NewWebAppsClient()
}

func TestSDKAzureFunctionsCreateGetDelete(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Functions: cloudP.Functions})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client := newWebAppsClient(t, ts)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, rgName, "sdk-fn",
		armappservice.Site{
			Kind:     to.Ptr("functionapp"),
			Location: to.Ptr("eastus"),
			Properties: &armappservice.SiteProperties{
				HTTPSOnly: to.Ptr(true),
				SiteConfig: &armappservice.SiteConfig{
					LinuxFxVersion: to.Ptr("Python|3.10"),
				},
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, &runtimePollerOptions)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	if created.Name == nil || *created.Name != "sdk-fn" {
		t.Fatalf("created Name = %v, want sdk-fn", created.Name)
	}

	if created.Kind == nil || *created.Kind != "functionapp" {
		t.Fatalf("created Kind = %v, want functionapp", created.Kind)
	}

	got, err := client.Get(ctx, rgName, "sdk-fn", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name == nil || *got.Name != "sdk-fn" {
		t.Fatalf("got Name = %v, want sdk-fn", got.Name)
	}

	if got.Properties == nil ||
		got.Properties.SiteConfig == nil ||
		got.Properties.SiteConfig.LinuxFxVersion == nil ||
		*got.Properties.SiteConfig.LinuxFxVersion != "Python|3.10" {
		t.Fatalf("LinuxFxVersion not preserved: %+v", got.Properties)
	}

	if _, err := client.Delete(ctx, rgName, "sdk-fn", nil); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := client.Get(ctx, rgName, "sdk-fn", nil); err == nil {
		t.Fatal("post-delete Get returned nil error, want NotFound")
	}
}

// runtimePollerOptions is a zero-frequency poll override so the SDK doesn't
// sleep between status polls in tests.
//
//nolint:gochecknoglobals // exported only to test files in this package.
var runtimePollerOptions = pollerOptionsZeroFrequency()

func pollerOptionsZeroFrequency() runtime.PollUntilDoneOptions {
	return runtime.PollUntilDoneOptions{Frequency: time.Millisecond}
}
