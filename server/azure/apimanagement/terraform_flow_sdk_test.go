package apimanagement_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement/v3"
)

// pollFast is the poll frequency for the synchronous emulator LROs.
//
//nolint:gochecknoglobals // shared poll option
var pollFast = &runtime.PollUntilDoneOptions{Frequency: time.Millisecond}

// TestSDKTerraformCreateReadDestroy replays, through the official clients, the
// request sequence terraform-provider-azurerm v4 makes for azurerm_api_management
// (internal/services/apimanagement/api_management_resource.go at 3cfd078, with
// the provider's default features: recover_soft_deleted and
// purge_soft_delete_on_destroy both true):
//
//	create:  GET service (404) -> GET deletedservices (404) -> PUT service ->
//	         list+delete apis -> list+delete products -> PUT signin -> PUT signup
//	read:    GET service -> GET policies/policy?format=xml (404 tolerated) ->
//	         GET signin, signup, delegation -> POST delegation/listSecrets ->
//	         POST tenant/access/listSecrets
//	destroy: GET service -> DELETE service -> GET deletedservices (must be 200)
//	         -> DELETE deletedservices (purge)
//
// On b0ebd3d3 the first deletedservices GET answered 501, and every child call
// answered 404 InvalidResourceType.
func TestSDKTerraformCreateReadDestroy(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	name := "tf-apim"

	// create
	_, err := f.svc.Get(ctx, rgName, name, nil)
	assertStatus(t, err, http.StatusNotFound, "ResourceNotFound")

	deleted := f.cf.NewDeletedServicesClient()
	_, err = deleted.GetByName(ctx, name, "eastus", nil)
	assertStatus(t, err, http.StatusNotFound, "ResourceNotFound")

	f.create(t, rgName, name, developerService())
	f.terraformPruneSamples(t, name)
	f.terraformPortalWrites(t, name)

	// read (twice: apply's read and the no-op plan's refresh must agree)
	first := f.terraformRead(t, name)
	if again := f.terraformRead(t, name); again != first {
		t.Errorf("refresh drifted:\n first %+v\n again %+v", first, again)
	}

	// destroy
	f.terraformDestroy(t, name)

	// the purged name is free again
	f.create(t, rgName, name, developerService())
}

// terraformPruneSamples lists and deletes the sample Echo API and the Starter
// and Unlimited products Azure provisions with a new service.
func (f *fixture) terraformPruneSamples(t *testing.T, name string) {
	t.Helper()

	ctx := context.Background()
	apis := f.cf.NewAPIClient()

	var apiIDs []string

	for pager := apis.NewListByServicePager(rgName, name, nil); pager.More(); {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list apis: %v", err)
		}

		for _, a := range page.Value {
			apiIDs = append(apiIDs, deref(a.Name))
		}
	}

	if len(apiIDs) != 1 || apiIDs[0] != "echo-api" {
		t.Fatalf("a new Developer service must carry the sample echo-api, got %v", apiIDs)
	}

	for _, id := range apiIDs {
		poller, err := apis.BeginDelete(ctx, rgName, name, id, "*",
			&armapimanagement.APIClientBeginDeleteOptions{DeleteRevisions: to.Ptr(true)})
		if err != nil {
			t.Fatalf("delete api %s: %v", id, err)
		}

		if _, err := poller.PollUntilDone(ctx, pollFast); err != nil {
			t.Fatalf("delete api %s poll: %v", id, err)
		}
	}

	products := f.cf.NewProductClient()

	var productIDs []string

	for pager := products.NewListByServicePager(rgName, name, nil); pager.More(); {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("list products: %v", err)
		}

		for _, p := range page.Value {
			productIDs = append(productIDs, deref(p.Name))
		}
	}

	if len(productIDs) != 2 || productIDs[0] != "starter" || productIDs[1] != "unlimited" {
		t.Fatalf("a new Developer service must carry starter+unlimited, got %v", productIDs)
	}

	for _, id := range productIDs {
		if _, err := products.Delete(ctx, rgName, name, id, "*",
			&armapimanagement.ProductClientDeleteOptions{DeleteSubscriptions: to.Ptr(true)}); err != nil {
			t.Fatalf("delete product %s: %v", id, err)
		}
	}

	if _, err := apis.Get(ctx, rgName, name, "echo-api", nil); err == nil {
		t.Error("echo-api still readable after delete")
	}
}

// terraformPortalWrites is the sign_in / sign_up PUT azurerm always sends for a
// tier with a developer portal (the expanded defaults: both disabled).
func (f *fixture) terraformPortalWrites(t *testing.T, name string) {
	t.Helper()

	ctx := context.Background()

	_, err := f.cf.NewSignInSettingsClient().CreateOrUpdate(ctx, rgName, name, armapimanagement.PortalSigninSettings{
		Properties: &armapimanagement.PortalSigninSettingProperties{Enabled: to.Ptr(false)},
	}, nil)
	if err != nil {
		t.Fatalf("PUT signin: %v", err)
	}

	_, err = f.cf.NewSignUpSettingsClient().CreateOrUpdate(ctx, rgName, name, armapimanagement.PortalSignupSettings{
		Properties: &armapimanagement.PortalSignupSettingsProperties{
			Enabled: to.Ptr(false),
			TermsOfService: &armapimanagement.TermsOfServiceProperties{
				ConsentRequired: to.Ptr(false), Enabled: to.Ptr(false), Text: to.Ptr(""),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("PUT signup: %v", err)
	}
}

// tfState is the part of azurerm's read that depends on the child calls.
type tfState struct {
	etag, gateway, regional, portal         string
	signIn, signUp, tosEnabled, delegation  bool
	validationKey, tenantID, primary, secnd string
}

// terraformRead performs azurerm's read calls and flattens what it stores.
func (f *fixture) terraformRead(t *testing.T, name string) tfState {
	t.Helper()

	ctx := context.Background()

	svc, err := f.svc.Get(ctx, rgName, name, nil)
	if err != nil {
		t.Fatalf("GET service: %v", err)
	}

	if svc.Properties.CustomProperties == nil {
		t.Fatal("customProperties must be present: azurerm dereferences it on read")
	}

	_, err = f.cf.NewPolicyClient().Get(ctx, rgName, name, armapimanagement.PolicyIDNamePolicy,
		&armapimanagement.PolicyClientGetOptions{Format: to.Ptr(armapimanagement.PolicyExportFormatXML)})
	assertStatus(t, err, http.StatusNotFound, "ResourceNotFound")

	signIn, err := f.cf.NewSignInSettingsClient().Get(ctx, rgName, name, nil)
	if err != nil {
		t.Fatalf("GET signin: %v", err)
	}

	signUp, err := f.cf.NewSignUpSettingsClient().Get(ctx, rgName, name, nil)
	if err != nil {
		t.Fatalf("GET signup: %v", err)
	}

	delegation := f.cf.NewDelegationSettingsClient()

	del, err := delegation.Get(ctx, rgName, name, nil)
	if err != nil {
		t.Fatalf("GET delegation: %v", err)
	}

	key, err := delegation.ListSecrets(ctx, rgName, name, nil)
	if err != nil {
		t.Fatalf("delegation listSecrets: %v", err)
	}

	secrets, err := f.cf.NewTenantAccessClient().ListSecrets(ctx, rgName, name, armapimanagement.AccessIDNameAccess, nil)
	if err != nil {
		t.Fatalf("tenant access listSecrets: %v", err)
	}

	if deref(secrets.PrimaryKey) == "" || deref(secrets.SecondaryKey) == "" || deref(secrets.ID) != "access" {
		t.Errorf("tenant access secrets incomplete: %+v", secrets.AccessInformationSecretsContract)
	}

	return tfState{
		etag:          deref(svc.Etag),
		gateway:       deref(svc.Properties.GatewayURL),
		regional:      deref(svc.Properties.GatewayRegionalURL),
		portal:        deref(svc.Properties.DeveloperPortalURL),
		signIn:        deref(signIn.Properties.Enabled),
		signUp:        deref(signUp.Properties.Enabled),
		tosEnabled:    deref(signUp.Properties.TermsOfService.Enabled),
		delegation:    deref(del.Properties.Subscriptions.Enabled),
		validationKey: deref(key.ValidationKey),
		tenantID:      deref(secrets.ID),
		primary:       deref(secrets.PrimaryKey),
		secnd:         deref(secrets.SecondaryKey),
	}
}

// terraformDestroy deletes the service and purges the soft-deleted record.
func (f *fixture) terraformDestroy(t *testing.T, name string) {
	t.Helper()

	ctx := context.Background()

	existing, err := f.svc.Get(ctx, rgName, name, nil)
	if err != nil {
		t.Fatalf("GET before delete: %v", err)
	}

	poller, err := f.svc.BeginDelete(ctx, rgName, name, nil)
	if err != nil {
		t.Fatalf("delete service: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, pollFast); err != nil {
		t.Fatalf("delete service poll: %v", err)
	}

	deleted := f.cf.NewDeletedServicesClient()

	got, err := deleted.GetByName(ctx, name, "eastus", nil)
	if err != nil {
		t.Fatalf("the deleted service must be soft-deleted and readable before purge: %v", err)
	}

	if deref(got.Properties.ServiceID) != deref(existing.ID) || got.Properties.ScheduledPurgeDate == nil ||
		!got.Properties.ScheduledPurgeDate.After(*got.Properties.DeletionDate) {
		t.Errorf("deleted service record = %+v", got.Properties)
	}

	purge, err := deleted.BeginPurge(ctx, name, "eastus", nil)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}

	if _, err := purge.PollUntilDone(ctx, pollFast); err != nil {
		t.Fatalf("purge poll: %v", err)
	}

	_, err = deleted.GetByName(ctx, name, "eastus", nil)
	assertStatus(t, err, http.StatusNotFound, "ResourceNotFound")
}
