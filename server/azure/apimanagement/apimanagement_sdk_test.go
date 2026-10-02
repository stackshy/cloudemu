package apimanagement_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/stackshy/cloudemu/v2"
	azureprov "github.com/stackshy/cloudemu/v2/providers/azure"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	subID   = "00000000-0000-0000-0000-0000000000a1"
	rgName  = "rg-apim"
	rgOther = "rg-apim-other"
	svcName = "Contoso-Apim"
)

// fakeCred is a static-token credential for tests.
type fakeCred struct{}

func (fakeCred) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type fixture struct {
	ts   *httptest.Server
	prov *azureprov.Provider
	opts *arm.ClientOptions
	cf   *armapimanagement.ClientFactory
	svc  *armapimanagement.ServiceClient
	rgs  *armresources.ResourceGroupsClient
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	prov := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.NewFromProvider(prov))
	t.Cleanup(ts.Close)

	opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: cloud.Configuration{
			ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
			Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
			},
		},
		Transport: ts.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}

	cf, err := armapimanagement.NewClientFactory(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("apim client factory: %v", err)
	}

	rgs, err := armresources.NewResourceGroupsClient(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("resource groups client: %v", err)
	}

	f := &fixture{ts: ts, prov: prov, opts: opts, cf: cf, svc: cf.NewServiceClient(), rgs: rgs}
	f.ensureRG(t, rgName)
	f.ensureRG(t, rgOther)

	return f
}

func (f *fixture) ensureRG(t *testing.T, name string) {
	t.Helper()

	_, err := f.rgs.CreateOrUpdate(context.Background(), name, armresources.ResourceGroup{Location: to.Ptr("eastus")}, nil)
	if err != nil {
		t.Fatalf("create resource group %s: %v", name, err)
	}
}

func developerService() armapimanagement.ServiceResource {
	return armapimanagement.ServiceResource{
		Location: to.Ptr("East US"),
		Tags:     map[string]*string{"env": to.Ptr("dev")},
		SKU: &armapimanagement.ServiceSKUProperties{
			Name:     to.Ptr(armapimanagement.SKUTypeDeveloper),
			Capacity: to.Ptr[int32](1),
		},
		Identity: &armapimanagement.ServiceIdentity{Type: to.Ptr(armapimanagement.ApimIdentityTypeSystemAssigned)},
		Properties: &armapimanagement.ServiceProperties{
			PublisherEmail: to.Ptr("api@contoso.test"),
			PublisherName:  to.Ptr("Contoso"),
			CustomProperties: map[string]*string{
				"Microsoft.WindowsAzure.ApiManagement.Gateway.Security.Protocols.Tls10": to.Ptr("false"),
			},
		},
	}
}

func (f *fixture) create(
	t *testing.T, rg, name string, body armapimanagement.ServiceResource,
) armapimanagement.ServiceResource {
	t.Helper()

	ctx := context.Background()

	poller, err := f.svc.BeginCreateOrUpdate(ctx, rg, name, body, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate %s: %v", name, err)
	}

	res, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Millisecond})
	if err != nil {
		t.Fatalf("PollUntilDone create %s: %v", name, err)
	}

	return res.ServiceResource
}

// TestSDKServiceLifecycle drives the real armapimanagement ServiceClient
// through create (LRO) -> get -> patch (LRO) -> list (rg + subscription) ->
// delete (LRO) -> get 404.
func TestSDKServiceLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	created := f.create(t, rgName, svcName, developerService())
	assertCreated(t, &created)

	got, err := f.svc.Get(ctx, rgName, svcName, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if *got.Etag != *created.Etag || *got.Identity.PrincipalID != *created.Identity.PrincipalID ||
		!got.Properties.CreatedAtUTC.Equal(*created.Properties.CreatedAtUTC) {
		t.Errorf("computed fields drifted between create and get")
	}

	patched := patchService(t, f)
	assertPatched(t, &patched, &created)

	assertListed(t, f)

	delPoller, err := f.svc.BeginDelete(ctx, rgName, svcName, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone delete: %v", err)
	}

	_, err = f.svc.Get(ctx, rgName, svcName, nil)
	assertStatus(t, err, http.StatusNotFound, "ResourceNotFound")
}

func assertCreated(t *testing.T, s *armapimanagement.ServiceResource) {
	t.Helper()

	p := s.Properties

	checks := map[string][2]string{
		"type":               {deref(s.Type), "Microsoft.ApiManagement/service"},
		"provisioningState":  {deref(p.ProvisioningState), "Succeeded"},
		"gatewayUrl":         {deref(p.GatewayURL), "https://contoso-apim.azure-api.net"},
		"portalUrl":          {deref(p.PortalURL), "https://contoso-apim.portal.azure-api.net"},
		"developerPortalUrl": {deref(p.DeveloperPortalURL), "https://contoso-apim.developer.azure-api.net"},
		"managementApiUrl":   {deref(p.ManagementAPIURL), "https://contoso-apim.management.azure-api.net"},
		"scmUrl":             {deref(p.ScmURL), "https://contoso-apim.scm.azure-api.net"},
		"publisherEmail":     {deref(p.PublisherEmail), "api@contoso.test"},
		"sku":                {string(deref(s.SKU.Name)), "Developer"},
	}

	for field, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", field, c[0], c[1])
		}
	}

	if !strings.HasSuffix(deref(s.ID), "/resourceGroups/"+rgName+"/providers/Microsoft.ApiManagement/service/"+svcName) {
		t.Errorf("id = %q", deref(s.ID))
	}

	if p.CreatedAtUTC == nil || p.CreatedAtUTC.IsZero() || s.Etag == nil || *s.Etag == "" {
		t.Error("createdAtUtc / etag not minted")
	}

	if s.Identity == nil || deref(s.Identity.PrincipalID) == "" || deref(s.Identity.TenantID) == "" {
		t.Errorf("system-assigned identity not minted: %+v", s.Identity)
	}

	if deref(p.CustomProperties["Microsoft.WindowsAzure.ApiManagement.Gateway.Security.Protocols.Tls10"]) != "false" {
		t.Errorf("customProperties did not round-trip: %v", p.CustomProperties)
	}
}

func patchService(t *testing.T, f *fixture) armapimanagement.ServiceResource {
	t.Helper()

	ctx := context.Background()

	poller, err := f.svc.BeginUpdate(ctx, rgName, svcName, armapimanagement.ServiceUpdateParameters{
		Tags: map[string]*string{"team": to.Ptr("api")},
		SKU: &armapimanagement.ServiceSKUProperties{
			Name:     to.Ptr(armapimanagement.SKUTypePremium),
			Capacity: to.Ptr[int32](2),
		},
		Properties: &armapimanagement.ServiceUpdateProperties{PublisherName: to.Ptr("Fabrikam")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone update: %v", err)
	}

	return res.ServiceResource
}

func assertPatched(t *testing.T, got, before *armapimanagement.ServiceResource) {
	t.Helper()

	if len(got.Tags) != 1 || deref(got.Tags["team"]) != "api" {
		t.Errorf("PATCH tags must replace the set, got %v", got.Tags)
	}

	if deref(got.SKU.Name) != armapimanagement.SKUTypePremium || deref(got.SKU.Capacity) != 2 {
		t.Errorf("sku = %v/%v, want Premium/2", deref(got.SKU.Name), deref(got.SKU.Capacity))
	}

	if deref(got.Properties.PublisherName) != "Fabrikam" ||
		deref(got.Properties.PublisherEmail) != "api@contoso.test" {
		t.Errorf("PATCH properties must merge: name=%q email=%q",
			deref(got.Properties.PublisherName), deref(got.Properties.PublisherEmail))
	}

	if len(got.Properties.CustomProperties) != 1 {
		t.Errorf("PATCH dropped unnamed properties: %v", got.Properties.CustomProperties)
	}

	if deref(got.Identity.PrincipalID) != deref(before.Identity.PrincipalID) {
		t.Error("PATCH re-minted the identity")
	}

	if deref(got.Etag) == deref(before.Etag) {
		t.Error("PATCH must rotate the etag")
	}
}

func assertListed(t *testing.T, f *fixture) {
	t.Helper()

	f.create(t, rgOther, "other-apim", developerService())

	ctx := context.Background()

	var inRG []string

	for pager := f.svc.NewListByResourceGroupPager(rgName, nil); pager.More(); {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list by rg: %v", err)
		}

		for _, s := range page.Value {
			inRG = append(inRG, *s.Name)
		}
	}

	if len(inRG) != 1 || inRG[0] != svcName {
		t.Errorf("list by rg = %v, want [%s]", inRG, svcName)
	}

	total := 0

	for pager := f.svc.NewListPager(nil); pager.More(); {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list by subscription: %v", err)
		}

		total += len(page.Value)
	}

	if total != 2 {
		t.Errorf("list by subscription = %d services, want 2", total)
	}
}

// TestSDKConsumptionTier covers the capacity-0 serverless tier, whose response
// carries only the gateway endpoint.
func TestSDKConsumptionTier(t *testing.T) {
	f := newFixture(t)

	body := developerService()
	body.SKU = &armapimanagement.ServiceSKUProperties{
		Name: to.Ptr(armapimanagement.SKUTypeConsumption), Capacity: to.Ptr[int32](0),
	}

	s := f.create(t, rgName, "serverless", body)

	if deref(s.SKU.Capacity) != 0 || deref(s.Properties.GatewayURL) != "https://serverless.azure-api.net" {
		t.Errorf("consumption: capacity=%d gateway=%q", deref(s.SKU.Capacity), deref(s.Properties.GatewayURL))
	}

	if s.Properties.PortalURL != nil || s.Properties.ScmURL != nil {
		t.Error("consumption tier must not report portal/scm endpoints")
	}
}

// TestSDKValidationErrors asserts every rejected create surfaces as APIM's 400
// ValidationError through the real client.
func TestSDKValidationErrors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	cases := map[string]struct {
		name   string
		mutate func(*armapimanagement.ServiceResource)
	}{
		"missing publisherEmail": {"svc-a", func(s *armapimanagement.ServiceResource) { s.Properties.PublisherEmail = nil }},
		"missing publisherName":  {"svc-b", func(s *armapimanagement.ServiceResource) { s.Properties.PublisherName = nil }},
		"missing location":       {"svc-c", func(s *armapimanagement.ServiceResource) { s.Location = nil }},
		"consumption capacity 1": {"svc-d", func(s *armapimanagement.ServiceResource) {
			s.SKU.Name = to.Ptr(armapimanagement.SKUTypeConsumption)
		}},
		"developer capacity 0": {"svc-e", func(s *armapimanagement.ServiceResource) { s.SKU.Capacity = to.Ptr[int32](0) }},
		"unknown sku":          {"svc-f", func(s *armapimanagement.ServiceResource) { s.SKU.Name = to.Ptr(armapimanagement.SKUType("Gold")) }},
		"name starts digit":    {"1svc", nil},
		"name too long":        {"a" + strings.Repeat("b", 50), nil},
		"name trailing hyphen": {"svc-", nil},
	}

	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			body := developerService()
			if tc.mutate != nil {
				tc.mutate(&body)
			}

			_, err := f.svc.BeginCreateOrUpdate(ctx, rgName, tc.name, body, nil)
			assertStatus(t, err, http.StatusBadRequest, "ValidationError")
		})
	}
}

// TestSDKMissingResourceGroupAndCascade covers the resource-group gate (a
// create in a group that does not exist is 404 ResourceGroupNotFound) and the
// purge cascade (deleting the group deletes its services).
func TestSDKMissingResourceGroupAndCascade(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	_, err := f.svc.BeginCreateOrUpdate(ctx, "rg-missing", svcName, developerService(), nil)
	assertStatus(t, err, http.StatusNotFound, "ResourceGroupNotFound")

	f.create(t, rgOther, svcName, developerService())

	rgPoller, err := f.rgs.BeginDelete(ctx, rgOther, nil)
	if err != nil {
		t.Fatalf("delete resource group: %v", err)
	}

	if _, err := rgPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("poll resource group delete: %v", err)
	}

	f.ensureRG(t, rgOther)

	_, err = f.svc.Get(ctx, rgOther, svcName, nil)
	assertStatus(t, err, http.StatusNotFound, "ResourceNotFound")
}

// TestResourceGraphListsService asserts the service is projected into the
// discovery inventory and Resource Graph under its ARM type.
func TestResourceGraphListsService(t *testing.T) {
	f := newFixture(t)
	f.create(t, rgName, svcName, developerService())

	body := `{"subscriptions":["` + subID + `"],"query":"Resources | where type =~ 'microsoft.apimanagement/service'"}`

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		f.ts.URL+"/providers/Microsoft.ResourceGraph/resources?api-version=2022-10-01", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("resource graph query: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), svcName) ||
		!strings.Contains(strings.ToLower(string(raw)), "microsoft.apimanagement/service") {
		t.Fatalf("resource graph = %d %s, want the service row", resp.StatusCode, raw)
	}
}

func assertStatus(t *testing.T, err error, status int, code string) {
	t.Helper()

	var re *azcore.ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want *azcore.ResponseError %d", err, status)
	}

	if re.StatusCode != status || re.ErrorCode != code {
		t.Fatalf("got %d %s, want %d %s", re.StatusCode, re.ErrorCode, status, code)
	}
}

// deref returns *p, or the zero value for a nil pointer.
func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}

	return *p
}
