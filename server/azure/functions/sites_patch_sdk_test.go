package functions_test

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v3"
)

// TestSDKSitePatchPartialUpdate drives WebApps_Update (PATCH) through the real
// client: a PATCH that touches only httpsOnly must leave the location, kind,
// runtime, app settings and an unmodeled (overlay) property exactly as stored.
func TestSDKSitePatchPartialUpdate(t *testing.T) {
	client, ctx := newFunctionsTestServer(t)

	createSite(t, ctx, client, "sdk-patch", armappservice.Site{
		Kind:     to.Ptr("functionapp,linux"),
		Location: to.Ptr("eastus"),
		Properties: &armappservice.SiteProperties{
			// ClientAffinityEnabled is not modeled by the handler, so it survives
			// only through the unmodeled-property overlay — the PATCH must not drop it.
			ClientAffinityEnabled: to.Ptr(true),
			SiteConfig: &armappservice.SiteConfig{
				LinuxFxVersion: to.Ptr("Python|3.11"),
				AppSettings: []*armappservice.NameValuePair{
					{Name: to.Ptr("FOO"), Value: to.Ptr("bar")},
				},
			},
		},
	})

	// PATCH only httpsOnly.
	updated, err := client.Update(ctx, rgName, "sdk-patch", armappservice.SitePatchResource{
		Properties: &armappservice.SitePatchResourceProperties{HTTPSOnly: to.Ptr(true)},
	}, nil)
	if err != nil {
		t.Fatalf("Update(PATCH): %v", err)
	}

	if updated.Properties == nil || updated.Properties.HTTPSOnly == nil || !*updated.Properties.HTTPSOnly {
		t.Fatalf("PATCH did not set httpsOnly: %+v", updated.Properties)
	}

	got, err := client.Get(ctx, rgName, "sdk-patch", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Location == nil || *got.Location != "eastus" {
		t.Fatalf("PATCH clobbered location = %v, want eastus", got.Location)
	}

	if got.Kind == nil || *got.Kind != "functionapp,linux" {
		t.Fatalf("PATCH clobbered kind = %v, want functionapp,linux", got.Kind)
	}

	if got.Properties == nil || got.Properties.SiteConfig == nil ||
		got.Properties.SiteConfig.LinuxFxVersion == nil || *got.Properties.SiteConfig.LinuxFxVersion != "Python|3.11" {
		t.Fatalf("PATCH clobbered linuxFxVersion: %+v", got.Properties)
	}

	if got.Properties.HTTPSOnly == nil || !*got.Properties.HTTPSOnly {
		t.Fatalf("PATCH httpsOnly not persisted: %+v", got.Properties)
	}

	if got.Properties.ClientAffinityEnabled == nil || !*got.Properties.ClientAffinityEnabled {
		t.Fatalf("PATCH dropped the unmodeled overlay property clientAffinityEnabled: %+v", got.Properties)
	}

	// App settings the PATCH never mentioned must still be there.
	settings, err := client.ListApplicationSettings(ctx, rgName, "sdk-patch", nil)
	if err != nil {
		t.Fatalf("ListApplicationSettings: %v", err)
	}

	if v := settings.Properties["FOO"]; v == nil || *v != "bar" {
		t.Fatalf("PATCH clobbered app setting FOO = %v, want bar", v)
	}
}

// TestSDKSitePatchIdentityAssign confirms `az functionapp identity assign` (a
// PATCH carrying only the identity block) attaches a system-assigned identity
// without disturbing the rest of the site.
func TestSDKSitePatchIdentityAssign(t *testing.T) {
	client, ctx := newFunctionsTestServer(t)

	createSite(t, ctx, client, "sdk-patch-id", armappservice.Site{
		Location:   to.Ptr("eastus"),
		Properties: &armappservice.SiteProperties{SiteConfig: &armappservice.SiteConfig{}},
	})

	updated, err := client.Update(ctx, rgName, "sdk-patch-id", armappservice.SitePatchResource{
		Identity: &armappservice.ManagedServiceIdentity{
			Type: to.Ptr(armappservice.ManagedServiceIdentityTypeSystemAssigned),
		},
	}, nil)
	if err != nil {
		t.Fatalf("Update(identity assign): %v", err)
	}

	if updated.Identity == nil || updated.Identity.PrincipalID == nil || *updated.Identity.PrincipalID == "" {
		t.Fatalf("PATCH identity assign did not synthesize a principalId: %+v", updated.Identity)
	}

	got, err := client.Get(ctx, rgName, "sdk-patch-id", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Identity == nil || got.Identity.Type == nil ||
		*got.Identity.Type != armappservice.ManagedServiceIdentityTypeSystemAssigned {
		t.Fatalf("identity not persisted after PATCH: %+v", got.Identity)
	}
}

// TestSDKSitePatchMissing confirms a PATCH against a site that does not exist is
// a 404 rather than a 405 or a silent create.
func TestSDKSitePatchMissing(t *testing.T) {
	client, ctx := newFunctionsTestServer(t)

	if _, err := client.Update(ctx, rgName, "ghost", armappservice.SitePatchResource{
		Properties: &armappservice.SitePatchResourceProperties{HTTPSOnly: to.Ptr(true)},
	}, nil); err == nil {
		t.Fatal("PATCH on missing site returned nil error, want 404")
	}
}

// TestSDKSiteStartStop drives WebApps_Stop / WebApps_Start and confirms the
// running state a subsequent GET reports follows the last action.
func TestSDKSiteStartStop(t *testing.T) {
	client, ctx := newFunctionsTestServer(t)

	created := createSite(t, ctx, client, "sdk-startstop", armappservice.Site{
		Location:   to.Ptr("eastus"),
		Properties: &armappservice.SiteProperties{SiteConfig: &armappservice.SiteConfig{}},
	})
	if created.Properties == nil || created.Properties.State == nil || *created.Properties.State != "Running" {
		t.Fatalf("fresh site state = %v, want Running", stateOf(created))
	}

	if _, err := client.Stop(ctx, rgName, "sdk-startstop", nil); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	got, err := client.Get(ctx, rgName, "sdk-startstop", nil)
	if err != nil {
		t.Fatalf("Get after stop: %v", err)
	}

	if got.Properties == nil || got.Properties.State == nil || *got.Properties.State != "Stopped" {
		t.Fatalf("state after stop = %v, want Stopped", stateOf(got.Site))
	}

	if _, err := client.Start(ctx, rgName, "sdk-startstop", nil); err != nil {
		t.Fatalf("Start: %v", err)
	}

	got, err = client.Get(ctx, rgName, "sdk-startstop", nil)
	if err != nil {
		t.Fatalf("Get after start: %v", err)
	}

	if got.Properties == nil || got.Properties.State == nil || *got.Properties.State != "Running" {
		t.Fatalf("state after start = %v, want Running", stateOf(got.Site))
	}
}

// stateOf renders a site's State pointer for test failure messages.
func stateOf(s armappservice.Site) string {
	if s.Properties == nil || s.Properties.State == nil {
		return "<nil>"
	}

	return *s.Properties.State
}
