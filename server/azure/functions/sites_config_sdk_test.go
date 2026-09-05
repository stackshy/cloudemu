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

// TestSDKSiteConfigTrioRoundTrip pins the site_config knobs Terraform's
// azurerm_linux_web_app compares on every plan — always_on, ftps_state and
// minimum_tls_version — round-tripping through GetConfiguration (config/web)
// and the site resource, not just the generic property echo. An explicit
// always_on=false (required on Basic/Free tiers) must survive distinctly from
// "unset", or the provider sees perpetual drift.
func TestSDKSiteConfigTrioRoundTrip(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Functions: cloudP.Functions}))
	t.Cleanup(ts.Close)

	client := newWebAppsClient(t, ts)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, rgName, "sdk-cfg",
		armappservice.Site{
			Kind:     to.Ptr("app,linux"),
			Location: to.Ptr("eastus"),
			Properties: &armappservice.SiteProperties{
				Reserved: to.Ptr(true),
				SiteConfig: &armappservice.SiteConfig{
					LinuxFxVersion: to.Ptr("NODE|18-lts"),
					AlwaysOn:       to.Ptr(false),
					FtpsState:      to.Ptr(armappservice.FtpsStateDisabled),
					MinTLSVersion:  to.Ptr(armappservice.SupportedTLSVersionsOne2),
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, &runtimePollerOptions); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	// GetConfiguration is the call azurerm reads site_config back from.
	cfg, err := client.GetConfiguration(ctx, rgName, "sdk-cfg", nil)
	if err != nil {
		t.Fatalf("GetConfiguration: %v", err)
	}

	assertTrio(t, "GetConfiguration", cfg.Properties, false, armappservice.FtpsStateDisabled, armappservice.SupportedTLSVersionsOne2)

	// The site resource itself must carry the same values.
	got, err := client.Get(ctx, rgName, "sdk-cfg", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertTrio(t, "Get", got.Properties.SiteConfig, false, armappservice.FtpsStateDisabled, armappservice.SupportedTLSVersionsOne2)

	// Update (PATCH) only always_on -> true; ftps/minTls must be preserved.
	if _, err = client.Update(ctx, rgName, "sdk-cfg", armappservice.SitePatchResource{
		Properties: &armappservice.SitePatchResourceProperties{
			SiteConfig: &armappservice.SiteConfig{AlwaysOn: to.Ptr(true)},
		},
	}, nil); err != nil {
		t.Fatalf("Update: %v", err)
	}

	cfg, err = client.GetConfiguration(ctx, rgName, "sdk-cfg", nil)
	if err != nil {
		t.Fatalf("GetConfiguration after patch: %v", err)
	}

	assertTrio(t, "afterPatch", cfg.Properties, true, armappservice.FtpsStateDisabled, armappservice.SupportedTLSVersionsOne2)
}

// assertTrio asserts the site_config always_on/ftps_state/min_tls_version trio.
func assertTrio(
	t *testing.T, where string, cfg *armappservice.SiteConfig,
	wantAlwaysOn bool, wantFtps armappservice.FtpsState, wantTLS armappservice.SupportedTLSVersions,
) {
	t.Helper()

	if cfg == nil {
		t.Fatalf("%s: siteConfig is nil", where)
	}

	if cfg.AlwaysOn == nil || *cfg.AlwaysOn != wantAlwaysOn {
		t.Fatalf("%s: alwaysOn = %v, want %v", where, cfg.AlwaysOn, wantAlwaysOn)
	}

	if cfg.FtpsState == nil || *cfg.FtpsState != wantFtps {
		t.Fatalf("%s: ftpsState = %v, want %v", where, cfg.FtpsState, wantFtps)
	}

	if cfg.MinTLSVersion == nil || *cfg.MinTLSVersion != wantTLS {
		t.Fatalf("%s: minTlsVersion = %v, want %v", where, cfg.MinTLSVersion, wantTLS)
	}
}

// TestSDKSiteConfigUnsetOmitsTrio confirms a site created without the trio does
// not synthesize phantom values: the fields stay absent (nil) so a caller that
// never set them reads them as unset, matching "explicit-only" round-trip.
func TestSDKSiteConfigUnsetOmitsTrio(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Functions: cloudP.Functions}))
	t.Cleanup(ts.Close)

	client := newWebAppsClient(t, ts)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, rgName, "sdk-cfg-bare",
		armappservice.Site{
			Kind:     to.Ptr("app,linux"),
			Location: to.Ptr("eastus"),
			Properties: &armappservice.SiteProperties{
				SiteConfig: &armappservice.SiteConfig{LinuxFxVersion: to.Ptr("PYTHON|3.11")},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, &runtimePollerOptions); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	cfg, err := client.GetConfiguration(ctx, rgName, "sdk-cfg-bare", nil)
	if err != nil {
		t.Fatalf("GetConfiguration: %v", err)
	}

	if cfg.Properties == nil {
		t.Fatal("siteConfig properties nil")
	}

	if cfg.Properties.AlwaysOn != nil {
		t.Fatalf("alwaysOn = %v, want nil (unset)", *cfg.Properties.AlwaysOn)
	}

	if cfg.Properties.FtpsState != nil {
		t.Fatalf("ftpsState = %v, want nil (unset)", *cfg.Properties.FtpsState)
	}

	if cfg.Properties.MinTLSVersion != nil {
		t.Fatalf("minTlsVersion = %v, want nil (unset)", *cfg.Properties.MinTLSVersion)
	}
}
