package functions_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v3"
	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// rgNameB is a second resource group, distinct from rgName, used by the
// cross-resource-group isolation tests below.
const rgNameB = "test-rg-b"

// TestSDKSiteGetDeleteScopedByResourceGroup covers the deep-sweep BLOCKER: a
// site created in one resource group must be invisible (Get and Delete both
// 404) through a different resource group, even though the site name is
// unique in the store.
func TestSDKSiteGetDeleteScopedByResourceGroup(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Functions: cloudP.Functions}))
	t.Cleanup(ts.Close)

	client := newWebAppsClient(t, ts)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, rgName, "sdk-scoped-site",
		armappservice.Site{
			Kind:     to.Ptr("functionapp"),
			Location: to.Ptr("eastus"),
			Properties: &armappservice.SiteProperties{
				SiteConfig: &armappservice.SiteConfig{LinuxFxVersion: to.Ptr("Python|3.11")},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, &runtimePollerOptions); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	// A GET against the real owning resource group succeeds.
	if _, err := client.Get(ctx, rgName, "sdk-scoped-site", nil); err != nil {
		t.Fatalf("Get in owning rg: %v", err)
	}

	// The same site name, requested through a different resource group, must
	// 404 — not return rgName's site.
	if _, err := client.Get(ctx, rgNameB, "sdk-scoped-site", nil); err == nil {
		t.Fatal("Get from wrong resource group returned nil error, want 404")
	}

	// Deleting through the wrong resource group must 404 and must not remove
	// the site from its real resource group.
	if _, err := client.Delete(ctx, rgNameB, "sdk-scoped-site", nil); err == nil {
		t.Fatal("Delete from wrong resource group returned nil error, want 404")
	}

	if _, err := client.Get(ctx, rgName, "sdk-scoped-site", nil); err != nil {
		t.Fatalf("site removed by wrong-resource-group delete: %v", err)
	}

	// Deleting through the correct resource group succeeds and the site is
	// then gone from there too.
	if _, err := client.Delete(ctx, rgName, "sdk-scoped-site", nil); err != nil {
		t.Fatalf("Delete in owning rg: %v", err)
	}

	if _, err := client.Get(ctx, rgName, "sdk-scoped-site", nil); err == nil {
		t.Fatal("post-delete Get returned nil error, want 404")
	}
}

// TestSDKAppServicePlanDeleteAndListWebApps covers the tractable HIGH
// findings: DELETE on an App Service plan, and Plans.ListWebApps returning
// the apps hosted on that plan (previously misrouted to the plan Get and
// always empty).
func TestSDKAppServicePlanDeleteAndListWebApps(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Functions: cloudP.Functions})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	plansClient := newPlansClient(t, ts)
	webAppsClient := newWebAppsClient(t, ts)
	ctx := context.Background()

	planPoller, err := plansClient.BeginCreateOrUpdate(ctx, rgName, "sdk-plan-2",
		armappservice.Plan{
			Kind:     to.Ptr("linux"),
			Location: to.Ptr("eastus"),
			SKU:      &armappservice.SKUDescription{Name: to.Ptr("B1"), Tier: to.Ptr("Basic")},
		}, nil)
	if err != nil {
		t.Fatalf("Plans BeginCreateOrUpdate: %v", err)
	}

	if _, err = planPoller.PollUntilDone(ctx, &runtimePollerOptions); err != nil {
		t.Fatalf("Plans PollUntilDone: %v", err)
	}

	serverFarmID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/serverfarms/sdk-plan-2", subID, rgName)

	sitePoller, err := webAppsClient.BeginCreateOrUpdate(ctx, rgName, "sdk-plan-site",
		armappservice.Site{
			Kind:     to.Ptr("app"),
			Location: to.Ptr("eastus"),
			Properties: &armappservice.SiteProperties{
				ServerFarmID: to.Ptr(serverFarmID),
				SiteConfig:   &armappservice.SiteConfig{},
			},
		}, nil)
	if err != nil {
		t.Fatalf("Sites BeginCreateOrUpdate: %v", err)
	}

	if _, err = sitePoller.PollUntilDone(ctx, &runtimePollerOptions); err != nil {
		t.Fatalf("Sites PollUntilDone: %v", err)
	}

	// Plans.ListWebApps must return the app just created against this plan.
	pager := plansClient.NewListWebAppsPager(rgName, "sdk-plan-2", nil)

	var names []string

	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("ListWebApps page: %v", perr)
		}

		for _, s := range page.Value {
			if s.Name != nil {
				names = append(names, *s.Name)
			}
		}
	}

	if len(names) != 1 || names[0] != "sdk-plan-site" {
		t.Fatalf("ListWebApps = %v, want [sdk-plan-site]", names)
	}

	// A plan in the wrong resource group must not see this plan's apps.
	wrongPager := plansClient.NewListWebAppsPager(rgNameB, "sdk-plan-2", nil)
	if wrongPager.More() {
		if _, perr := wrongPager.NextPage(ctx); perr == nil {
			t.Fatal("ListWebApps from wrong resource group returned nil error, want 404")
		}
	}

	// The plan cannot be deleted while it still hosts a site (guarded, see
	// TestSDKAppServicePlanDeleteRejectedWhileSiteAssigned) — remove the site
	// first, then DELETE the plan.
	if _, err := webAppsClient.Delete(ctx, rgName, "sdk-plan-site", nil); err != nil {
		t.Fatalf("Sites Delete: %v", err)
	}

	if _, err := plansClient.Delete(ctx, rgName, "sdk-plan-2", nil); err != nil {
		t.Fatalf("Plans Delete: %v", err)
	}

	if _, err := plansClient.Get(ctx, rgName, "sdk-plan-2", nil); err == nil {
		t.Fatal("post-delete Plans Get returned nil error, want 404")
	}

	// Deleting again is a 404, not a panic or 500.
	if _, err := plansClient.Delete(ctx, rgName, "sdk-plan-2", nil); err == nil {
		t.Fatal("second Delete returned nil error, want 404")
	}
}

// TestSDKAppServicePlanDeleteRejectedWhileSiteAssigned covers the data-corruption
// finding: deleting an App Service plan that still hosts a Web App must 409
// Conflict (real Azure: "Server farm ... cannot be deleted because it has web
// app(s) assigned to it"), not silently succeed and leave the site pointing at a
// plan that no longer exists. Once the site is deleted, the plan delete succeeds.
func TestSDKAppServicePlanDeleteRejectedWhileSiteAssigned(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Functions: cloudP.Functions}))
	t.Cleanup(ts.Close)

	plansClient := newPlansClient(t, ts)
	webAppsClient := newWebAppsClient(t, ts)
	ctx := context.Background()

	planPoller, err := plansClient.BeginCreateOrUpdate(ctx, rgName, "sdk-guard-plan",
		armappservice.Plan{
			Kind:     to.Ptr("linux"),
			Location: to.Ptr("eastus"),
			SKU:      &armappservice.SKUDescription{Name: to.Ptr("B1"), Tier: to.Ptr("Basic")},
		}, nil)
	if err != nil {
		t.Fatalf("Plans BeginCreateOrUpdate: %v", err)
	}

	if _, err = planPoller.PollUntilDone(ctx, &runtimePollerOptions); err != nil {
		t.Fatalf("Plans PollUntilDone: %v", err)
	}

	serverFarmID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Web/serverfarms/sdk-guard-plan", subID, rgName)

	sitePoller, err := webAppsClient.BeginCreateOrUpdate(ctx, rgName, "sdk-guard-site",
		armappservice.Site{
			Kind:     to.Ptr("app"),
			Location: to.Ptr("eastus"),
			Properties: &armappservice.SiteProperties{
				ServerFarmID: to.Ptr(serverFarmID),
				SiteConfig:   &armappservice.SiteConfig{},
			},
		}, nil)
	if err != nil {
		t.Fatalf("Sites BeginCreateOrUpdate: %v", err)
	}

	if _, err = sitePoller.PollUntilDone(ctx, &runtimePollerOptions); err != nil {
		t.Fatalf("Sites PollUntilDone: %v", err)
	}

	// Deleting the plan while the site is still assigned must 409 Conflict.
	_, err = plansClient.Delete(ctx, rgName, "sdk-guard-plan", nil)
	if err == nil {
		t.Fatal("Plans Delete with an assigned site returned nil error, want 409 Conflict")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusConflict {
		t.Fatalf("Plans Delete error = %v, want 409 Conflict", err)
	}

	// The plan (and the site's reference to it) must survive the rejected delete.
	if _, err := plansClient.Get(ctx, rgName, "sdk-guard-plan", nil); err != nil {
		t.Fatalf("plan removed by rejected delete: %v", err)
	}

	// After the site is deleted, deleting the plan succeeds.
	if _, err := webAppsClient.Delete(ctx, rgName, "sdk-guard-site", nil); err != nil {
		t.Fatalf("Sites Delete: %v", err)
	}

	if _, err := plansClient.Delete(ctx, rgName, "sdk-guard-plan", nil); err != nil {
		t.Fatalf("Plans Delete after site removed: %v", err)
	}

	if _, err := plansClient.Get(ctx, rgName, "sdk-guard-plan", nil); err == nil {
		t.Fatal("post-delete Plans Get returned nil error, want 404")
	}
}

// TestSDKUpdateApplicationSettings covers the tractable HIGH finding: PUT
// config/appsettings must persist the settings (replacing, not merging) and
// echo them back, and a wrong-resource-group PUT must not leak through.
func TestSDKUpdateApplicationSettings(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Functions: cloudP.Functions}))
	t.Cleanup(ts.Close)

	client := newWebAppsClient(t, ts)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, rgName, "sdk-appsettings",
		armappservice.Site{
			Kind:     to.Ptr("functionapp"),
			Location: to.Ptr("eastus"),
			Properties: &armappservice.SiteProperties{
				SiteConfig: &armappservice.SiteConfig{
					AppSettings: []*armappservice.NameValuePair{
						{Name: to.Ptr("ORIGINAL"), Value: to.Ptr("keepme")},
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

	updated, err := client.UpdateApplicationSettings(ctx, rgName, "sdk-appsettings",
		armappservice.StringDictionary{
			Properties: map[string]*string{
				"NEW_SETTING": to.Ptr("value1"),
			},
		}, nil)
	if err != nil {
		t.Fatalf("UpdateApplicationSettings: %v", err)
	}

	if got := updated.Properties["NEW_SETTING"]; got == nil || *got != "value1" {
		t.Fatalf("response NEW_SETTING = %v, want value1", got)
	}

	settings, err := client.ListApplicationSettings(ctx, rgName, "sdk-appsettings", nil)
	if err != nil {
		t.Fatalf("ListApplicationSettings: %v", err)
	}

	if got := settings.Properties["NEW_SETTING"]; got == nil || *got != "value1" {
		t.Fatalf("persisted NEW_SETTING = %v, want value1", got)
	}

	// PUT replaces the settings map — ORIGINAL from create time must be gone.
	if _, ok := settings.Properties["ORIGINAL"]; ok {
		t.Fatalf("PUT config/appsettings merged instead of replacing: %+v", settings.Properties)
	}

	// A PUT against the wrong resource group must 404 and must not touch the
	// real site's settings.
	if _, err := client.UpdateApplicationSettings(ctx, rgNameB, "sdk-appsettings",
		armappservice.StringDictionary{Properties: map[string]*string{"HIJACK": to.Ptr("bad")}}, nil,
	); err == nil {
		t.Fatal("UpdateApplicationSettings from wrong resource group returned nil error, want 404")
	}

	after, err := client.ListApplicationSettings(ctx, rgName, "sdk-appsettings", nil)
	if err != nil {
		t.Fatalf("ListApplicationSettings after wrong-rg update: %v", err)
	}

	if _, leaked := after.Properties["HIJACK"]; leaked {
		t.Fatal("wrong-resource-group UpdateApplicationSettings leaked through")
	}
}
