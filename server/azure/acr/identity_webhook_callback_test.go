package acr_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

const uaResourceID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/" +
	"Microsoft.ManagedIdentity/userAssignedIdentities/uai1"

// TestSDKACRRegistryUserAssignedIdentity is B1: a UserAssigned identity map must
// survive the create→GET round trip with a synthesized, deterministic
// principal/client pair per identity (the TF-drift bug was the map being
// dropped on GET).
func TestSDKACRRegistryUserAssignedIdentity(t *testing.T) {
	cf := newACRARMFactory(t)
	client := cf.NewRegistriesClient()
	ctx := context.Background()

	poller, err := client.BeginCreate(ctx, "rg-1", "uareg", armcontainerregistry.Registry{
		Location: to.Ptr("eastus"),
		SKU:      &armcontainerregistry.SKU{Name: to.Ptr(armcontainerregistry.SKUNameStandard)},
		Identity: &armcontainerregistry.IdentityProperties{
			Type: to.Ptr(armcontainerregistry.ResourceIdentityTypeUserAssigned),
			UserAssignedIdentities: map[string]*armcontainerregistry.UserIdentityProperties{
				uaResourceID: {},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Create PollUntilDone: %v", err)
	}

	got, err := client.Get(ctx, "rg-1", "uareg", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertUserAssigned(t, got.Identity)
}

func assertUserAssigned(t *testing.T, id *armcontainerregistry.IdentityProperties) {
	t.Helper()

	if id == nil || id.Type == nil || *id.Type != armcontainerregistry.ResourceIdentityTypeUserAssigned {
		t.Fatalf("got identity %+v, want type UserAssigned", id)
	}

	if len(id.UserAssignedIdentities) != 1 {
		t.Fatalf("got %d user-assigned identities, want 1 (map dropped)", len(id.UserAssignedIdentities))
	}

	uai := id.UserAssignedIdentities[uaResourceID]
	if uai == nil || uai.PrincipalID == nil || uai.ClientID == nil {
		t.Fatalf("user-assigned identity %q missing principal/client: %+v", uaResourceID, uai)
	}

	if want := idgen.SyntheticGUID("uai-principal/" + uaResourceID); *uai.PrincipalID != want {
		t.Fatalf("got principalId %q, want deterministic %q", *uai.PrincipalID, want)
	}

	if want := idgen.SyntheticGUID("uai-client/" + uaResourceID); *uai.ClientID != want {
		t.Fatalf("got clientId %q, want deterministic %q", *uai.ClientID, want)
	}
}

// TestSDKACRRegistrySystemAssignedIdentity is B1b: system-assigned still yields a
// principal/tenant pair and carries no user-assigned map. It also covers the
// combined SystemAssigned,UserAssigned type.
func TestSDKACRRegistrySystemAssignedIdentity(t *testing.T) {
	cf := newACRARMFactory(t)
	client := cf.NewRegistriesClient()
	ctx := context.Background()

	reg := createRegistry(t, client, "rg-1", "sysreg") // system-assigned
	if reg.Identity == nil || reg.Identity.PrincipalID == nil || *reg.Identity.PrincipalID == "" {
		t.Fatal("expected system-assigned identity with principalId")
	}

	if len(reg.Identity.UserAssignedIdentities) != 0 {
		t.Fatalf("system-assigned must carry no user-assigned map, got %d", len(reg.Identity.UserAssignedIdentities))
	}

	got, err := client.Get(ctx, "rg-1", "sysreg", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Identity.TenantID == nil || *got.Identity.TenantID == "" {
		t.Fatal("expected system-assigned identity tenantId on GET")
	}

	combined := combinedIdentityRegistry(t, client)
	if combined.PrincipalID == nil || *combined.PrincipalID == "" {
		t.Fatal("combined identity: expected system-assigned principalId")
	}

	if len(combined.UserAssignedIdentities) != 1 {
		t.Fatalf("combined identity: got %d user-assigned identities, want 1", len(combined.UserAssignedIdentities))
	}
}

func combinedIdentityRegistry(t *testing.T, client *armcontainerregistry.RegistriesClient) *armcontainerregistry.IdentityProperties {
	t.Helper()

	ctx := context.Background()

	poller, err := client.BeginCreate(ctx, "rg-1", "bothreg", armcontainerregistry.Registry{
		Location: to.Ptr("eastus"),
		SKU:      &armcontainerregistry.SKU{Name: to.Ptr(armcontainerregistry.SKUNameStandard)},
		Identity: &armcontainerregistry.IdentityProperties{
			Type: to.Ptr(armcontainerregistry.ResourceIdentityTypeSystemAssignedUserAssigned),
			UserAssignedIdentities: map[string]*armcontainerregistry.UserIdentityProperties{
				uaResourceID: {},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("combined BeginCreate: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("combined PollUntilDone: %v", err)
	}

	got, err := client.Get(ctx, "rg-1", "bothreg", nil)
	if err != nil {
		t.Fatalf("combined Get: %v", err)
	}

	return got.Identity
}

// TestSDKACRWebhookCallbackConfig is B2+B3: a plain Webhooks_Get must NOT expose
// serviceUri or customHeaders (an Authorization bearer header among them), while
// getCallbackConfig — the only supported read path — returns both.
func TestSDKACRWebhookCallbackConfig(t *testing.T) {
	ts := newACRARMServer(t)
	cf := armFactoryFor(t, ts)
	ctx := context.Background()

	createRegistry(t, cf.NewRegistriesClient(), "rg-1", "cbreg")

	const authHeader = "Bearer sekret-token"

	whClient := cf.NewWebhooksClient()

	poller, err := whClient.BeginCreate(ctx, "rg-1", "cbreg", "wh1", armcontainerregistry.WebhookCreateParameters{
		Location: to.Ptr("eastus"),
		Properties: &armcontainerregistry.WebhookPropertiesCreateParameters{
			ServiceURI:    to.Ptr("https://example.com/hook"),
			Actions:       []*armcontainerregistry.WebhookAction{to.Ptr(armcontainerregistry.WebhookActionPush)},
			Status:        to.Ptr(armcontainerregistry.WebhookStatusEnabled),
			CustomHeaders: map[string]*string{"Authorization": to.Ptr(authHeader)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("Webhook BeginCreate: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Webhook PollUntilDone: %v", err)
	}

	assertPlainGetNoLeak(t, ts)
	assertCallbackConfig(t, whClient, authHeader)
}

func assertPlainGetNoLeak(t *testing.T, ts *httptest.Server) {
	t.Helper()

	url := ts.URL + "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.ContainerRegistry/" +
		"registries/cbreg/webhooks/wh1?api-version=2023-07-01"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build raw GET: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("raw webhook GET: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read raw body: %v", err)
	}

	body := string(raw)
	for _, leak := range []string{"serviceUri", "customHeaders", "Authorization", "example.com", "sekret"} {
		if strings.Contains(body, leak) {
			t.Fatalf("plain Webhooks_Get leaked %q: %s", leak, body)
		}
	}
}

func assertCallbackConfig(t *testing.T, whClient *armcontainerregistry.WebhooksClient, authHeader string) {
	t.Helper()

	cb, err := whClient.GetCallbackConfig(context.Background(), "rg-1", "cbreg", "wh1", nil)
	if err != nil {
		t.Fatalf("GetCallbackConfig: %v", err)
	}

	if cb.ServiceURI == nil || *cb.ServiceURI != "https://example.com/hook" {
		t.Fatalf("getCallbackConfig serviceUri = %v, want https://example.com/hook", cb.ServiceURI)
	}

	if got := cb.CustomHeaders["Authorization"]; got == nil || *got != authHeader {
		t.Fatalf("getCallbackConfig customHeaders[Authorization] = %v, want %q", got, authHeader)
	}
}

// TestSDKACRListCredentialsNotStripped guards #715: listCredentials admin
// passwords are legitimately returned and must not be stripped as secrets.
func TestSDKACRListCredentialsNotStripped(t *testing.T) {
	cf := newACRARMFactory(t)
	client := cf.NewRegistriesClient()
	ctx := context.Background()

	createRegistry(t, client, "rg-1", "credregx") // admin user enabled

	creds, err := client.ListCredentials(ctx, "rg-1", "credregx", nil)
	if err != nil {
		t.Fatalf("ListCredentials: %v", err)
	}

	if len(creds.Passwords) != 2 {
		t.Fatalf("got %d passwords, want 2", len(creds.Passwords))
	}

	for i, p := range creds.Passwords {
		if p.Value == nil || *p.Value == "" {
			t.Fatalf("password %d empty — #715 regression (admin password stripped)", i)
		}
	}
}
