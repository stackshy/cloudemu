package apimanagement_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

const apiVersion = "?api-version=2022-08-01"

// serviceURL is the ARM URL of a service in the fixture subscription.
func (f *fixture) serviceURL(rg, name string) string {
	return f.ts.URL + "/subscriptions/" + subID + "/resourceGroups/" + rg +
		"/providers/Microsoft.ApiManagement/service/" + name
}

// do sends a raw ARM request and returns the status, body and response headers.
func (f *fixture) do(t *testing.T, method, url, body string, hdr map[string]string) (int, string, http.Header) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	for k, v := range hdr {
		req.Header.Set(k, v)
	}

	resp, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(raw), resp.Header
}

// TestSDKEtagRotatesOnEveryWrite: PUT, PATCH and a replacing PUT each return a
// new etag, a read does not change it, and a stale If-Match is 412 on PUT,
// PATCH and DELETE while the current etag succeeds. On b0ebd3d3 every write
// returned the same etag and If-Match was ignored.
func TestSDKEtagRotatesOnEveryWrite(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	created := f.create(t, rgName, svcName, developerService())
	patched := patchService(t, f)
	replaced := f.create2(t, rgName, svcName, developerService())

	etags := map[string]bool{deref(created.Etag): true, deref(patched.Etag): true, deref(replaced.Etag): true}
	if len(etags) != 3 {
		t.Fatalf("PUT/PATCH/PUT must each return a new etag, got %q %q %q",
			deref(created.Etag), deref(patched.Etag), deref(replaced.Etag))
	}

	got, err := f.svc.Get(ctx, rgName, svcName, nil)
	if err != nil || deref(got.Etag) != deref(replaced.Etag) {
		t.Fatalf("a read must not change the etag: %v %q", err, deref(got.Etag))
	}

	stale := map[string]string{"If-Match": `"` + deref(created.Etag) + `"`}
	url := f.serviceURL(rgName, svcName) + apiVersion

	if code, body, _ := f.do(t, http.MethodPatch, url, `{"tags":{"a":"b"}}`, stale); code != http.StatusPreconditionFailed {
		t.Errorf("PATCH with a stale If-Match = %d %s, want 412", code, body)
	}

	putBody, _ := json.Marshal(developerService())
	if code, body, _ := f.do(t, http.MethodPut, url, string(putBody), stale); code != http.StatusPreconditionFailed {
		t.Errorf("PUT with a stale If-Match = %d %s, want 412", code, body)
	}

	if code, body, _ := f.do(t, http.MethodDelete, url, "", stale); code != http.StatusPreconditionFailed {
		t.Errorf("DELETE with a stale If-Match = %d %s, want 412", code, body)
	}

	current := map[string]string{"If-Match": deref(replaced.Etag)}
	if code, body, _ := f.do(t, http.MethodPatch, url, `{"tags":{"a":"b"}}`, current); code != http.StatusOK {
		t.Errorf("PATCH with the current If-Match = %d %s, want 200", code, body)
	}
}

// create2 is create for a service that already exists (a replace).
func (f *fixture) create2(
	t *testing.T, rg, name string, body armapimanagement.ServiceResource,
) armapimanagement.ServiceResource {
	t.Helper()

	return f.create(t, rg, name, body)
}

// TestSDKCapacityCeilingsAndZones: each tier's unit ceiling is enforced (on
// create and on PATCH) and availability zones are Premium-only. On b0ebd3d3
// Developer with capacity 5 and zones on Developer were both accepted.
func TestSDKCapacityCeilingsAndZones(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	ceilings := map[armapimanagement.SKUType]int32{
		armapimanagement.SKUTypeDeveloper:      1,
		armapimanagement.SKUTypeBasic:          2,
		armapimanagement.SKUTypeStandard:       4,
		armapimanagement.SKUTypePremium:        12,
		armapimanagement.SKUTypeBasicV2:        10,
		armapimanagement.SKUType("StandardV2"): 10,
	}

	for sku, maxUnits := range ceilings {
		name := "cap-" + strings.ToLower(string(sku))

		body := developerService()
		body.SKU = &armapimanagement.ServiceSKUProperties{Name: to.Ptr(sku), Capacity: to.Ptr(maxUnits + 1)}

		_, err := f.svc.BeginCreateOrUpdate(ctx, rgName, name, body, nil)
		assertStatus(t, err, http.StatusBadRequest, "ValidationError")

		body.SKU.Capacity = to.Ptr(maxUnits)
		f.create(t, rgName, name, body)
	}

	_, err := f.svc.BeginUpdate(ctx, rgName, "cap-developer", armapimanagement.ServiceUpdateParameters{
		SKU: &armapimanagement.ServiceSKUProperties{Name: to.Ptr(armapimanagement.SKUTypeDeveloper), Capacity: to.Ptr[int32](5)},
	}, nil)
	assertStatus(t, err, http.StatusBadRequest, "ValidationError")

	zonal := developerService()
	zonal.Zones = []*string{to.Ptr("1"), to.Ptr("2")}

	_, err = f.svc.BeginCreateOrUpdate(ctx, rgName, "zones-dev", zonal, nil)
	assertStatus(t, err, http.StatusBadRequest, "ValidationError")

	zonal.SKU = &armapimanagement.ServiceSKUProperties{
		Name: to.Ptr(armapimanagement.SKUTypePremium), Capacity: to.Ptr[int32](2),
	}
	if s := f.create(t, rgName, "zones-premium", zonal); len(s.Zones) != 2 {
		t.Errorf("Premium zones = %v, want 2", s.Zones)
	}
}

// TestSDKServiceNameIsGlobal: the service name is a global *.azure-api.net
// label, so a second service of that name in another group or another
// subscription is 409, and checkNameAvailability reports it. On b0ebd3d3 both
// creates succeeded with the same gatewayUrl and checkNameAvailability was 501.
func TestSDKServiceNameIsGlobal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.create(t, rgName, "svc1", developerService())

	_, err := f.svc.BeginCreateOrUpdate(ctx, rgOther, "svc1", developerService(), nil)
	assertStatus(t, err, http.StatusConflict, "ServiceAlreadyExists")

	_, err = f.svc.BeginCreateOrUpdate(ctx, rgOther, "SVC1", developerService(), nil)
	assertStatus(t, err, http.StatusConflict, "ServiceAlreadyExists")

	otherSub := "00000000-0000-0000-0000-0000000000b2"

	cf, err := armapimanagement.NewClientFactory(otherSub, fakeCred{}, f.opts)
	if err != nil {
		t.Fatalf("client factory: %v", err)
	}

	rgs, err := armresources.NewResourceGroupsClient(otherSub, fakeCred{}, f.opts)
	if err != nil {
		t.Fatalf("rg client: %v", err)
	}

	if _, err := rgs.CreateOrUpdate(ctx, "rg-b", armresources.ResourceGroup{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("create rg in other subscription: %v", err)
	}

	_, err = cf.NewServiceClient().BeginCreateOrUpdate(ctx, "rg-b", "svc1", developerService(), nil)
	assertStatus(t, err, http.StatusConflict, "ServiceAlreadyExists")

	check := func(name string) armapimanagement.ServiceNameAvailabilityResult {
		res, err := f.svc.CheckNameAvailability(ctx,
			armapimanagement.ServiceCheckNameAvailabilityParameters{Name: to.Ptr(name)}, nil)
		if err != nil {
			t.Fatalf("checkNameAvailability %s: %v", name, err)
		}

		return res.ServiceNameAvailabilityResult
	}

	if r := check("svc1"); deref(r.NameAvailable) || deref(r.Reason) != armapimanagement.NameAvailabilityReasonAlreadyExists {
		t.Errorf("svc1 availability = %v/%v, want false/AlreadyExists", deref(r.NameAvailable), deref(r.Reason))
	}

	if r := check("free-name"); !deref(r.NameAvailable) || deref(r.Reason) != armapimanagement.NameAvailabilityReasonValid {
		t.Errorf("free-name availability = %v/%v, want true/Valid", deref(r.NameAvailable), deref(r.Reason))
	}

	if r := check("1bad"); deref(r.NameAvailable) || deref(r.Reason) != armapimanagement.NameAvailabilityReasonInvalid {
		t.Errorf("1bad availability = %v/%v, want false/Invalid", deref(r.NameAvailable), deref(r.Reason))
	}

	// A soft-deleted service still holds its name until it is purged.
	if _, err := f.svc.BeginDelete(ctx, rgName, "svc1", nil); err != nil {
		t.Fatalf("delete svc1: %v", err)
	}

	if r := check("svc1"); deref(r.NameAvailable) {
		t.Error("a soft-deleted service must still hold its name")
	}

	_, err = f.svc.BeginCreateOrUpdate(ctx, rgOther, "svc1", developerService(), nil)
	assertStatus(t, err, http.StatusConflict, "ServiceAlreadyExistsInSoftDeletedState")
}

// TestSDKLocationIsImmutable: a PUT naming another location on an existing
// service is 409 InvalidResourceLocation and leaves it unchanged; the same
// location in another spelling is accepted. On b0ebd3d3 the PUT answered 200 and
// silently kept eastus.
func TestSDKLocationIsImmutable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.create(t, rgName, svcName, developerService())

	moved := developerService()
	moved.Location = to.Ptr("West Europe")

	_, err := f.svc.BeginCreateOrUpdate(ctx, rgName, svcName, moved, nil)
	assertStatus(t, err, http.StatusConflict, "InvalidResourceLocation")

	same := developerService()
	same.Location = to.Ptr("eastus")
	f.create(t, rgName, svcName, same)

	got, err := f.svc.Get(ctx, rgName, svcName, nil)
	if err != nil || deref(got.Location) != "East US" {
		t.Fatalf("location = %q (%v), want East US", deref(got.Location), err)
	}
}

// TestLibraryAndServerReturnSameResource: the Go library's service carries the
// same properties block as the HTTP GET (the defaults and computed fields are
// the provider's). On b0ebd3d3 the library returned only the caller's
// properties and the server added virtualNetworkType, platformVersion, the
// regional gateway URL and the rest.
func TestLibraryAndServerReturnSameResource(t *testing.T) {
	f := newFixture(t)
	f.create(t, rgName, svcName, developerService())

	lib, err := f.prov.APIManagement.GetService(context.Background(), subID, rgName, svcName)
	if err != nil {
		t.Fatalf("library get: %v", err)
	}

	code, body, _ := f.do(t, http.MethodGet, f.serviceURL(rgName, svcName)+apiVersion, "", nil)
	if code != http.StatusOK {
		t.Fatalf("GET = %d %s", code, body)
	}

	var wire struct {
		Etag       string         `json:"etag"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal([]byte(body), &wire); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var libProps map[string]any
	if err := json.Unmarshal(lib.Properties, &libProps); err != nil {
		t.Fatalf("decode library properties: %v", err)
	}

	for _, k := range []string{
		"virtualNetworkType", "publicNetworkAccess", "notificationSenderEmail", "platformVersion",
		"gatewayRegionalUrl", "gatewayUrl", "developerPortalUrl", "provisioningState", "createdAtUtc",
	} {
		if libProps[k] == nil || libProps[k] != wire.Properties[k] {
			t.Errorf("property %s: library %v, server %v", k, libProps[k], wire.Properties[k])
		}
	}

	if lib.Etag != wire.Etag {
		t.Errorf("etag: library %q, server %q", lib.Etag, wire.Etag)
	}

	if got := libProps["gatewayRegionalUrl"]; got != "https://contoso-apim-eastus-01.regional.azure-api.net" {
		t.Errorf("gatewayRegionalUrl = %v", got)
	}
}

// TestResourceGraphTopLevelSKU: the Resource Graph row carries a top-level
// sku{name,capacity} and zones, not properties.sku. On b0ebd3d3 sku and
// skuCapacity sat in properties as strings/numbers and there was no sku block.
func TestResourceGraphTopLevelSKU(t *testing.T) {
	f := newFixture(t)

	body := developerService()
	body.SKU = &armapimanagement.ServiceSKUProperties{Name: to.Ptr(armapimanagement.SKUTypePremium), Capacity: to.Ptr[int32](3)}
	body.Zones = []*string{to.Ptr("1")}
	f.create(t, rgName, svcName, body)

	q := `{"subscriptions":["` + subID + `"],"query":"Resources | where type =~ 'microsoft.apimanagement/service'"}`

	code, raw, _ := f.do(t, http.MethodPost,
		f.ts.URL+"/providers/Microsoft.ResourceGraph/resources?api-version=2022-10-01", q, nil)
	if code != http.StatusOK {
		t.Fatalf("resource graph = %d %s", code, raw)
	}

	var res struct {
		Data []struct {
			Sku        map[string]any `json:"sku"`
			Zones      []string       `json:"zones"`
			Properties map[string]any `json:"properties"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &res); err != nil || len(res.Data) != 1 {
		t.Fatalf("decode %s: %v", raw, err)
	}

	row := res.Data[0]
	if row.Sku["name"] != "Premium" || row.Sku["capacity"] != float64(3) {
		t.Errorf("sku = %v, want {Premium 3}", row.Sku)
	}

	if len(row.Zones) != 1 || row.Zones[0] != "1" {
		t.Errorf("zones = %v", row.Zones)
	}

	if _, ok := row.Properties["sku"]; ok {
		t.Errorf("sku must not be a property: %v", row.Properties)
	}
}

// TestSDKListsPageWithNextLink: a list honours $top and emits a nextLink that
// the official pager follows to the end. On b0ebd3d3 there was no nextLink.
func TestSDKListsPageWithNextLink(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for _, n := range []string{"page-a", "page-b", "page-c"} {
		f.create(t, rgName, n, developerService())
	}

	code, body, _ := f.do(t, http.MethodGet, f.ts.URL+"/subscriptions/"+subID+"/resourceGroups/"+rgName+
		"/providers/Microsoft.ApiManagement/service"+apiVersion+"&$top=2", "", nil)

	var page struct {
		Value    []json.RawMessage `json:"value"`
		NextLink string            `json:"nextLink"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil || code != http.StatusOK {
		t.Fatalf("list = %d %s", code, body)
	}

	if len(page.Value) != 2 || !strings.Contains(page.NextLink, "%24skip=2") {
		t.Fatalf("first page = %d items, nextLink %q", len(page.Value), page.NextLink)
	}

	var names []string

	pager := f.cf.NewProductClient().NewListByServicePager(rgName, "page-a",
		&armapimanagement.ProductClientListByServiceOptions{Top: to.Ptr[int32](1)})

	pages := 0
	for pager.More() {
		p, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("products page: %v", err)
		}

		pages++

		for _, v := range p.Value {
			names = append(names, deref(v.Name))
		}
	}

	if pages != 2 || strings.Join(names, ",") != "starter,unlimited" {
		t.Errorf("paged products = %v over %d pages, want starter,unlimited over 2", names, pages)
	}
}

// TestSDKPolicyLifecycle covers the service-level policy: PUT (201 then 200),
// GET, a stale If-Match (412), malformed XML (400 ValidationError) and DELETE.
func TestSDKPolicyLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.create(t, rgName, svcName, developerService())

	pc := f.cf.NewPolicyClient()
	doc := `<policies><inbound><base /></inbound><backend><forward-request /></backend><outbound /><on-error /></policies>`

	put := func(value string, opts *armapimanagement.PolicyClientCreateOrUpdateOptions) (armapimanagement.PolicyClientCreateOrUpdateResponse, error) {
		return pc.CreateOrUpdate(ctx, rgName, svcName, armapimanagement.PolicyIDNamePolicy, armapimanagement.PolicyContract{
			Properties: &armapimanagement.PolicyContractProperties{
				Value: to.Ptr(value), Format: to.Ptr(armapimanagement.PolicyContentFormatXML),
			},
		}, opts)
	}

	first, err := put(doc, nil)
	if err != nil || first.ETag == nil {
		t.Fatalf("PUT policy: %v", err)
	}

	second, err := put(doc, &armapimanagement.PolicyClientCreateOrUpdateOptions{IfMatch: first.ETag})
	if err != nil || deref(second.ETag) == deref(first.ETag) {
		t.Fatalf("PUT policy with current If-Match: %v (etag %q -> %q)", err, deref(first.ETag), deref(second.ETag))
	}

	_, err = put(doc, &armapimanagement.PolicyClientCreateOrUpdateOptions{IfMatch: first.ETag})
	assertStatus(t, err, http.StatusPreconditionFailed, "PreconditionFailed")

	_, err = put("<policies><inbound>", nil)
	assertStatus(t, err, http.StatusBadRequest, "ValidationError")

	got, err := pc.Get(ctx, rgName, svcName, armapimanagement.PolicyIDNamePolicy, nil)
	if err != nil || deref(got.Properties.Value) != doc {
		t.Fatalf("GET policy = %v, %v", got.Properties, err)
	}

	_, err = pc.Delete(ctx, rgName, svcName, armapimanagement.PolicyIDNamePolicy, "\"stale\"", nil)
	assertStatus(t, err, http.StatusPreconditionFailed, "PreconditionFailed")

	if _, err := pc.Delete(ctx, rgName, svcName, armapimanagement.PolicyIDNamePolicy, "*", nil); err != nil {
		t.Fatalf("DELETE policy: %v", err)
	}

	_, err = pc.Get(ctx, rgName, svcName, armapimanagement.PolicyIDNamePolicy, nil)
	assertStatus(t, err, http.StatusNotFound, "ResourceNotFound")
}

// TestSDKTenantAccessAndPortal covers tenant access GET/PATCH (keys only from
// listSecrets), the delegation key staying out of GET, and the tiers without a
// developer portal refusing both.
func TestSDKTenantAccessAndPortal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.create(t, rgName, svcName, developerService())

	ta := f.cf.NewTenantAccessClient()

	got, err := ta.Get(ctx, rgName, svcName, armapimanagement.AccessIDNameAccess, nil)
	if err != nil || deref(got.Properties.Enabled) {
		t.Fatalf("GET tenant access = %+v, %v", got.Properties, err)
	}

	upd, err := ta.Update(ctx, rgName, svcName, armapimanagement.AccessIDNameAccess, "*",
		armapimanagement.AccessInformationUpdateParameters{
			Properties: &armapimanagement.AccessInformationUpdateParameterProperties{Enabled: to.Ptr(true)},
		}, nil)
	if err != nil || !deref(upd.Properties.Enabled) {
		t.Fatalf("PATCH tenant access = %+v, %v", upd.Properties, err)
	}

	secrets, err := ta.ListSecrets(ctx, rgName, svcName, armapimanagement.AccessIDNameAccess, nil)
	if err != nil || !deref(secrets.Enabled) {
		t.Fatalf("listSecrets = %+v, %v", secrets, err)
	}

	dc := f.cf.NewDelegationSettingsClient()
	if _, err := dc.CreateOrUpdate(ctx, rgName, svcName, armapimanagement.PortalDelegationSettings{
		Properties: &armapimanagement.PortalDelegationSettingsProperties{
			URL: to.Ptr("https://delegate.test"), ValidationKey: to.Ptr("c2VjcmV0"),
		},
	}, nil); err != nil {
		t.Fatalf("PUT delegation: %v", err)
	}

	code, body, _ := f.do(t, http.MethodGet, f.serviceURL(rgName, svcName)+"/portalsettings/delegation"+apiVersion, "", nil)
	if code != http.StatusOK || strings.Contains(body, "c2VjcmV0") {
		t.Errorf("GET delegation must not reveal the validation key: %d %s", code, body)
	}

	key, err := dc.ListSecrets(ctx, rgName, svcName, nil)
	if err != nil || deref(key.ValidationKey) != "c2VjcmV0" {
		t.Errorf("delegation listSecrets = %q, %v", deref(key.ValidationKey), err)
	}

	consumption := developerService()
	consumption.SKU = &armapimanagement.ServiceSKUProperties{
		Name: to.Ptr(armapimanagement.SKUTypeConsumption), Capacity: to.Ptr[int32](0),
	}
	f.create(t, rgName, "serverless", consumption)

	_, err = f.cf.NewSignInSettingsClient().Get(ctx, rgName, "serverless", nil)
	assertStatus(t, err, http.StatusBadRequest, "MethodNotAllowedInPricingTier")

	_, err = ta.Get(ctx, rgName, "serverless", armapimanagement.AccessIDNameAccess, nil)
	assertStatus(t, err, http.StatusBadRequest, "MethodNotAllowedInPricingTier")

	apis := f.cf.NewAPIClient().NewListByServicePager(rgName, "serverless", nil)

	p, err := apis.NextPage(ctx)
	if err != nil || len(p.Value) != 0 {
		t.Errorf("a Consumption service has no sample API: %d, %v", len(p.Value), err)
	}
}

// TestSDKRestoreSoftDeleted: a PUT with properties.restore recovers a
// soft-deleted service (ignoring the rest of the body), and a resource-group
// delete soft-deletes its services too.
func TestSDKRestoreSoftDeleted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	orig := f.create(t, rgName, svcName, developerService())

	if _, err := f.svc.BeginDelete(ctx, rgName, svcName, nil); err != nil {
		t.Fatalf("delete: %v", err)
	}

	list := f.cf.NewDeletedServicesClient().NewListBySubscriptionPager(nil)

	page, err := list.NextPage(ctx)
	if err != nil || len(page.Value) != 1 || deref(page.Value[0].Name) != svcName {
		t.Fatalf("deleted services list = %v, %v", page.Value, err)
	}

	restore := armapimanagement.ServiceResource{
		Location:   to.Ptr("eastus"),
		SKU:        &armapimanagement.ServiceSKUProperties{Name: to.Ptr(armapimanagement.SKUTypeDeveloper), Capacity: to.Ptr[int32](1)},
		Properties: &armapimanagement.ServiceProperties{Restore: to.Ptr(true), PublisherEmail: to.Ptr(""), PublisherName: to.Ptr("")},
	}

	got := f.create(t, rgName, svcName, restore)
	if deref(got.Properties.PublisherName) != "Contoso" || deref(got.Tags["env"]) != "dev" ||
		!got.Properties.CreatedAtUTC.Equal(*orig.Properties.CreatedAtUTC) {
		t.Errorf("restore must bring the service back as it was: %+v", got.Properties)
	}

	if _, err := f.cf.NewDeletedServicesClient().GetByName(ctx, svcName, "eastus", nil); err == nil {
		t.Error("a recovered service must leave the deleted list")
	}

	poller, err := f.rgs.BeginDelete(ctx, rgName, nil)
	if err != nil {
		t.Fatalf("delete rg: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, pollFast); err != nil {
		t.Fatalf("delete rg poll: %v", err)
	}

	if _, err := f.cf.NewDeletedServicesClient().GetByName(ctx, svcName, "eastus", nil); err != nil {
		t.Errorf("a resource-group delete must soft-delete its services: %v", err)
	}
}
