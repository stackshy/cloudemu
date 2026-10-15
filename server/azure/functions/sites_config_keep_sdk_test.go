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

// TestSDKSiteConfigWebHidesAppSettingsAndSurvivesBarePut: GET config/web
// returns appSettings null (values come only from appsettings/list), and a
// site PUT that omits siteConfig keeps the config/web values set earlier.
func TestSDKSiteConfigWebHidesAppSettingsAndSurvivesBarePut(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Functions: cloudP.Functions}))
	t.Cleanup(ts.Close)

	ensureRG(t, ts.Client(), ts.URL, subID, rgName)

	client := newWebAppsClient(t, ts)
	ctx := context.Background()

	put := func(props *armappservice.SiteProperties) {
		t.Helper()

		poller, err := client.BeginCreateOrUpdate(ctx, rgName, "sdk-cfg-keep",
			armappservice.Site{Kind: to.Ptr("app,linux"), Location: to.Ptr("eastus"), Properties: props}, nil)
		if err != nil {
			t.Fatalf("BeginCreateOrUpdate: %v", err)
		}

		if _, err = poller.PollUntilDone(ctx, &runtimePollerOptions); err != nil {
			t.Fatalf("PollUntilDone: %v", err)
		}
	}

	put(&armappservice.SiteProperties{
		Reserved: to.Ptr(true),
		SiteConfig: &armappservice.SiteConfig{
			AlwaysOn:    to.Ptr(true),
			AppSettings: []*armappservice.NameValuePair{{Name: to.Ptr("SECRET"), Value: to.Ptr("s3cr3t")}},
		},
	})

	if _, err := client.UpdateConfiguration(ctx, rgName, "sdk-cfg-keep", armappservice.SiteConfigResource{
		Properties: &armappservice.SiteConfig{Http20Enabled: to.Ptr(true)},
	}, nil); err != nil {
		t.Fatalf("UpdateConfiguration: %v", err)
	}

	put(&armappservice.SiteProperties{Reserved: to.Ptr(true)})

	cfg, err := client.GetConfiguration(ctx, rgName, "sdk-cfg-keep", nil)
	if err != nil {
		t.Fatalf("GetConfiguration: %v", err)
	}

	if cfg.Properties.AppSettings != nil {
		t.Errorf("config/web appSettings = %v, want null", cfg.Properties.AppSettings)
	}

	if cfg.Properties.Http20Enabled == nil || !*cfg.Properties.Http20Enabled {
		t.Errorf("http20Enabled = %v after a site PUT without siteConfig, want true", cfg.Properties.Http20Enabled)
	}

	if cfg.Properties.AlwaysOn == nil || !*cfg.Properties.AlwaysOn {
		t.Errorf("alwaysOn = %v after a site PUT without siteConfig, want true", cfg.Properties.AlwaysOn)
	}
}
