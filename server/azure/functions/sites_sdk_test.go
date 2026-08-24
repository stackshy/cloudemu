package functions_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v3"
	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestSDKAppSettingsHostKeysRestart drives the new site sub-routes through the
// real armappservice WebAppsClient, the way an app operator would.
func TestSDKAppSettingsHostKeysRestart(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Functions: cloudP.Functions}))
	t.Cleanup(ts.Close)

	client := newWebAppsClient(t, ts)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, rgName, "sdk-keys",
		armappservice.Site{
			Kind:     to.Ptr("functionapp"),
			Location: to.Ptr("westus2"),
			Properties: &armappservice.SiteProperties{
				SiteConfig: &armappservice.SiteConfig{
					LinuxFxVersion: to.Ptr("Python|3.11"),
					AppSettings: []*armappservice.NameValuePair{
						{Name: to.Ptr("FOO"), Value: to.Ptr("bar")},
					},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, &runtimePollerOptions); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	settings, err := client.ListApplicationSettings(ctx, rgName, "sdk-keys", nil)
	if err != nil {
		t.Fatalf("ListApplicationSettings: %v", err)
	}

	if got := settings.Properties["FOO"]; got == nil || *got != "bar" {
		t.Fatalf("app setting FOO = %v, want bar", got)
	}

	keys, err := client.ListHostKeys(ctx, rgName, "sdk-keys", nil)
	if err != nil {
		t.Fatalf("ListHostKeys: %v", err)
	}

	if keys.MasterKey == nil || *keys.MasterKey == "" {
		t.Fatalf("master key missing: %+v", keys)
	}

	if _, err = client.Restart(ctx, rgName, "sdk-keys", nil); err != nil {
		t.Fatalf("Restart: %v", err)
	}
}

// TestSDKListFunctions drives ListFunctions/GetFunction through the real client:
// a fresh app lists no functions and a missing function is a 404.
func TestSDKListFunctions(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Functions: cloudP.Functions}))
	t.Cleanup(ts.Close)

	client := newWebAppsClient(t, ts)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, rgName, "sdk-fns",
		armappservice.Site{
			Kind:       to.Ptr("functionapp"),
			Location:   to.Ptr("eastus"),
			Properties: &armappservice.SiteProperties{SiteConfig: &armappservice.SiteConfig{}},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, &runtimePollerOptions); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	pager := client.NewListFunctionsPager(rgName, "sdk-fns", nil)

	total := 0
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("ListFunctions page: %v", perr)
		}

		total += len(page.Value)
	}

	if total != 0 {
		t.Fatalf("fresh app functions = %d, want 0", total)
	}

	if _, err = client.GetFunction(ctx, rgName, "sdk-fns", "ghost", nil); err == nil {
		t.Fatal("GetFunction(ghost) returned nil error, want 404")
	}
}
