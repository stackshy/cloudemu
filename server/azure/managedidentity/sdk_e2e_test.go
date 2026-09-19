package managedidentity_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"

	cloudemu "github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const testSub = "00000000-0000-0000-0000-000000000001"

// fakeCred is a static-token credential; the emulator ignores the header but the
// SDK still requires a credential implementation.
type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func armClientOptions(ts *httptest.Server) *arm.ClientOptions {
	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}

	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}
}

func newClient(t *testing.T) (*armmsi.UserAssignedIdentitiesClient, *httptest.Server) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.DriversFrom(cloudP))

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client, err := armmsi.NewUserAssignedIdentitiesClient(testSub, fakeCred{}, armClientOptions(ts))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	return client, ts
}

// ensureRG creates a resource group so tests can PUT resources into it. Real
// Azure requires the group to exist first (the emulator enforces this via a
// pre-dispatch gate), so tests must provision it before their resource ops.
func ensureRG(t *testing.T, ts *httptest.Server, sub, rg string) {
	t.Helper()

	url := ts.URL + "/subscriptions/" + sub + "/resourcegroups/" + rg + "?api-version=2021-04-01"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, url,
		strings.NewReader(`{"location":"eastus"}`))
	if err != nil {
		t.Fatalf("ensureRG new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("ensureRG PUT %s: %v", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("ensureRG %s: unexpected status %d", url, resp.StatusCode)
	}
}

// TestSDKUserAssignedIdentityStableIdentifiers is the load-bearing regression:
// a user-assigned identity mints clientId/principalId/tenantId once, and every
// subsequent read returns exactly those values. Callers capture principalId to
// grant the identity RBAC role assignments, so regenerating it on a read would
// silently break those assignments.
func TestSDKUserAssignedIdentityStableIdentifiers(t *testing.T) {
	client, ts := newClient(t)
	ctx := context.Background()

	const rg, name = "rg-1", "id-1"

	ensureRG(t, ts, testSub, rg)

	created, err := client.CreateOrUpdate(ctx, rg, name, armmsi.Identity{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("prod")},
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	props := created.Properties
	if props == nil || props.ClientID == nil || props.PrincipalID == nil || props.TenantID == nil {
		t.Fatalf("create returned nil identity ids: %+v", props)
	}

	clientID, principalID, tenantID := *props.ClientID, *props.PrincipalID, *props.TenantID
	if clientID == "" || principalID == "" || tenantID == "" {
		t.Fatalf("create returned empty ids: client=%q principal=%q tenant=%q", clientID, principalID, tenantID)
	}

	if clientID == principalID {
		t.Errorf("clientId and principalId should be distinct, both %q", clientID)
	}

	if created.Name == nil || *created.Name != name {
		t.Errorf("create name = %v, want %q", created.Name, name)
	}

	// First Get round-trips the same ids, location and tags.
	got, err := client.Get(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	assertIDs(t, "get#1", got.Properties, clientID, principalID, tenantID)

	if got.Location == nil || *got.Location != "eastus" {
		t.Errorf("get location = %v, want eastus", got.Location)
	}

	if v := got.Tags["env"]; v == nil || *v != "prod" {
		t.Errorf("get tag env = %v, want prod", v)
	}

	// A second Get must return the SAME ids — proves they are persisted, not
	// regenerated per read.
	got2, err := client.Get(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("get#2: %v", err)
	}

	assertIDs(t, "get#2", got2.Properties, clientID, principalID, tenantID)
}

// TestSDKUserAssignedIdentityUpdatePreservesIDs verifies an update changes
// tags/location but keeps the minted ids.
func TestSDKUserAssignedIdentityUpdatePreservesIDs(t *testing.T) {
	client, ts := newClient(t)
	ctx := context.Background()

	const rg, name = "rg-1", "id-1"

	ensureRG(t, ts, testSub, rg)

	created, err := client.CreateOrUpdate(ctx, rg, name, armmsi.Identity{Location: to.Ptr("eastus")}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	principalID := *created.Properties.PrincipalID

	updated, err := client.CreateOrUpdate(ctx, rg, name, armmsi.Identity{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"team": to.Ptr("core")},
	}, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if updated.Properties.PrincipalID == nil || *updated.Properties.PrincipalID != principalID {
		t.Errorf("update changed principalId: got %v, want %q", updated.Properties.PrincipalID, principalID)
	}

	if v := updated.Tags["team"]; v == nil || *v != "core" {
		t.Errorf("update tag team = %v, want core", v)
	}
}

// TestSDKUserAssignedIdentityListing verifies ListByResourceGroup scopes to one
// group while ListBySubscription surfaces every identity.
func TestSDKUserAssignedIdentityListing(t *testing.T) {
	client, ts := newClient(t)
	ctx := context.Background()

	ensureRG(t, ts, testSub, "rg-a")
	ensureRG(t, ts, testSub, "rg-b")

	if _, err := client.CreateOrUpdate(ctx, "rg-a", "id-a", armmsi.Identity{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("create a: %v", err)
	}

	if _, err := client.CreateOrUpdate(ctx, "rg-b", "id-b", armmsi.Identity{Location: to.Ptr("westus")}, nil); err != nil {
		t.Fatalf("create b: %v", err)
	}

	rgNames := map[string]bool{}

	rgPager := client.NewListByResourceGroupPager("rg-a", nil)
	for rgPager.More() {
		page, err := rgPager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListByResourceGroup: %v", err)
		}

		for _, id := range page.Value {
			rgNames[*id.Name] = true
		}
	}

	if len(rgNames) != 1 || !rgNames["id-a"] {
		t.Fatalf("ListByResourceGroup(rg-a) = %v, want just id-a", rgNames)
	}

	subNames := map[string]bool{}

	subPager := client.NewListBySubscriptionPager(nil)
	for subPager.More() {
		page, err := subPager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListBySubscription: %v", err)
		}

		for _, id := range page.Value {
			subNames[*id.Name] = true
		}
	}

	if !subNames["id-a"] || !subNames["id-b"] {
		t.Fatalf("ListBySubscription = %v, want id-a and id-b", subNames)
	}
}

// TestSDKUserAssignedIdentityDelete verifies delete removes the identity and a
// subsequent Get is a 404.
func TestSDKUserAssignedIdentityDelete(t *testing.T) {
	client, ts := newClient(t)
	ctx := context.Background()

	const rg, name = "rg-1", "id-1"

	ensureRG(t, ts, testSub, rg)

	if _, err := client.CreateOrUpdate(ctx, rg, name, armmsi.Identity{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := client.Delete(ctx, rg, name, nil); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := client.Get(ctx, rg, name, nil)
	if err == nil {
		t.Fatal("get after delete: want error, got nil")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: want 404 ResponseError, got %v", err)
	}
}

func assertIDs(t *testing.T, label string, props *armmsi.UserAssignedIdentityProperties, client, principal, tenant string) {
	t.Helper()

	if props == nil {
		t.Fatalf("%s: nil properties", label)
	}

	if props.ClientID == nil || *props.ClientID != client {
		t.Errorf("%s clientId = %v, want %q", label, props.ClientID, client)
	}

	if props.PrincipalID == nil || *props.PrincipalID != principal {
		t.Errorf("%s principalId = %v, want %q", label, props.PrincipalID, principal)
	}

	if props.TenantID == nil || *props.TenantID != tenant {
		t.Errorf("%s tenantId = %v, want %q", label, props.TenantID, tenant)
	}
}
