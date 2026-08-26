package functions_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v3"
	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// guidLen is the character length of a canonical GUID (8-4-4-4-12 + 4 dashes).
const guidLen = 36

func newFunctionsTestServer(t *testing.T) (*armappservice.WebAppsClient, context.Context) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Functions: cloudP.Functions}))
	t.Cleanup(ts.Close)

	return newWebAppsClient(t, ts), context.Background()
}

//nolint:gocritic // site mirrors the SDK request payload; copying once per create is fine.
func createSite(
	t *testing.T, ctx context.Context, client *armappservice.WebAppsClient, name string, site armappservice.Site,
) armappservice.Site {
	t.Helper()

	poller, err := client.BeginCreateOrUpdate(ctx, rgName, name, site, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate(%s): %v", name, err)
	}

	created, err := poller.PollUntilDone(ctx, &runtimePollerOptions)
	if err != nil {
		t.Fatalf("PollUntilDone(%s): %v", name, err)
	}

	return created.Site
}

// TestSDKSiteKindRoundTrips confirms a non-default kind ("app,linux") survives
// create and GET rather than being overwritten to "functionapp".
func TestSDKSiteKindRoundTrips(t *testing.T) {
	client, ctx := newFunctionsTestServer(t)

	created := createSite(t, ctx, client, "sdk-kind", armappservice.Site{
		Kind:       to.Ptr("app,linux"),
		Location:   to.Ptr("eastus"),
		Properties: &armappservice.SiteProperties{SiteConfig: &armappservice.SiteConfig{}},
	})
	if created.Kind == nil || *created.Kind != "app,linux" {
		t.Fatalf("created Kind = %v, want app,linux", created.Kind)
	}

	got, err := client.Get(ctx, rgName, "sdk-kind", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Kind == nil || *got.Kind != "app,linux" {
		t.Fatalf("got Kind = %v, want app,linux", got.Kind)
	}
}

// TestSDKSiteKindDefaultsWhenOmitted confirms an omitted kind still reports the
// "functionapp" default.
func TestSDKSiteKindDefaultsWhenOmitted(t *testing.T) {
	client, ctx := newFunctionsTestServer(t)

	created := createSite(t, ctx, client, "sdk-nokind", armappservice.Site{
		Location:   to.Ptr("eastus"),
		Properties: &armappservice.SiteProperties{SiteConfig: &armappservice.SiteConfig{}},
	})
	if created.Kind == nil || *created.Kind != "functionapp" {
		t.Fatalf("created Kind = %v, want functionapp", created.Kind)
	}
}

// TestSDKSiteSystemAssignedIdentity confirms a system-assigned identity is
// modeled: the create and GET responses carry a non-empty, deterministic
// principalId + tenantId, so azurerm's identity[0].principal_id is present.
func TestSDKSiteSystemAssignedIdentity(t *testing.T) {
	client, ctx := newFunctionsTestServer(t)

	created := createSite(t, ctx, client, "sdk-sa", armappservice.Site{
		Location:   to.Ptr("eastus"),
		Identity:   &armappservice.ManagedServiceIdentity{Type: to.Ptr(armappservice.ManagedServiceIdentityTypeSystemAssigned)},
		Properties: &armappservice.SiteProperties{SiteConfig: &armappservice.SiteConfig{}},
	})

	assertSystemAssigned(t, created.Identity)
	created1 := *created.Identity.PrincipalID

	got, err := client.Get(ctx, rgName, "sdk-sa", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertSystemAssigned(t, got.Identity)

	if *got.Identity.PrincipalID != created1 {
		t.Fatalf("principalId not deterministic: create=%s get=%s", created1, *got.Identity.PrincipalID)
	}

	got2, err := client.Get(ctx, rgName, "sdk-sa", nil)
	if err != nil {
		t.Fatalf("Get #2: %v", err)
	}

	if *got2.Identity.PrincipalID != created1 {
		t.Fatalf("principalId drifted across GETs: %s vs %s", created1, *got2.Identity.PrincipalID)
	}
}

func assertSystemAssigned(t *testing.T, id *armappservice.ManagedServiceIdentity) {
	t.Helper()

	if id == nil {
		t.Fatal("identity nil, want SystemAssigned block")
	}

	if id.PrincipalID == nil || len(*id.PrincipalID) != guidLen {
		t.Fatalf("principalId = %v, want %d-char GUID", id.PrincipalID, guidLen)
	}

	if id.TenantID == nil || len(*id.TenantID) != guidLen {
		t.Fatalf("tenantId = %v, want %d-char GUID", id.TenantID, guidLen)
	}
}

// TestSDKSiteUserAssignedIdentity confirms a user-assigned identity echoes the
// submitted identity keys back, each with a synthesized principal/client id.
func TestSDKSiteUserAssignedIdentity(t *testing.T) {
	client, ctx := newFunctionsTestServer(t)

	uaID := "/subscriptions/" + subID +
		"/resourceGroups/" + rgName +
		"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/my-uai"

	created := createSite(t, ctx, client, "sdk-ua", armappservice.Site{
		Location: to.Ptr("eastus"),
		Identity: &armappservice.ManagedServiceIdentity{
			Type:                   to.Ptr(armappservice.ManagedServiceIdentityTypeUserAssigned),
			UserAssignedIdentities: map[string]*armappservice.UserAssignedIdentity{uaID: {}},
		},
		Properties: &armappservice.SiteProperties{SiteConfig: &armappservice.SiteConfig{}},
	})

	got, err := client.Get(ctx, rgName, "sdk-ua", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	for _, id := range []*armappservice.ManagedServiceIdentity{created.Identity, got.Identity} {
		if id == nil || id.UserAssignedIdentities == nil {
			t.Fatalf("user-assigned identity missing: %+v", id)
		}

		entry, ok := id.UserAssignedIdentities[uaID]
		if !ok || entry == nil {
			t.Fatalf("submitted identity key %q not echoed: %+v", uaID, id.UserAssignedIdentities)
		}

		if entry.PrincipalID == nil || *entry.PrincipalID == "" ||
			entry.ClientID == nil || *entry.ClientID == "" {
			t.Fatalf("user-assigned identity ids not synthesized: %+v", entry)
		}
	}
}

// TestSDKSiteNoIdentity confirms a site created without an identity block
// reports no identity on GET (Identity nil).
func TestSDKSiteNoIdentity(t *testing.T) {
	client, ctx := newFunctionsTestServer(t)

	createSite(t, ctx, client, "sdk-noid", armappservice.Site{
		Location:   to.Ptr("eastus"),
		Properties: &armappservice.SiteProperties{SiteConfig: &armappservice.SiteConfig{}},
	})

	got, err := client.Get(ctx, rgName, "sdk-noid", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Identity != nil {
		t.Fatalf("Identity = %+v, want nil for a site with no identity", got.Identity)
	}
}

// TestSDKSiteSecretNotLeakedOnGet is a #715 regression: an app-setting secret
// submitted at create time must NOT appear on a plain site GET (it is exposed
// only via ListApplicationSettings).
func TestSDKSiteSecretNotLeakedOnGet(t *testing.T) {
	client, ctx := newFunctionsTestServer(t)

	const secretVal = "super-secret-value"

	createSite(t, ctx, client, "sdk-secret", armappservice.Site{
		Location: to.Ptr("eastus"),
		Properties: &armappservice.SiteProperties{
			SiteConfig: &armappservice.SiteConfig{
				AppSettings: []*armappservice.NameValuePair{
					{Name: to.Ptr("API_KEY"), Value: to.Ptr(secretVal)},
				},
			},
		},
	})

	got, err := client.Get(ctx, rgName, "sdk-secret", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.SiteConfig == nil {
		t.Fatal("site properties missing")
	}

	for _, s := range got.Properties.SiteConfig.AppSettings {
		if s != nil && s.Value != nil && strings.Contains(*s.Value, secretVal) {
			t.Fatalf("secret leaked on plain GET: %+v", s)
		}
	}
}
