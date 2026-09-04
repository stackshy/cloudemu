package cloudbilling_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	billingbudgets "google.golang.org/api/billingbudgets/v1"
	cloudbilling "google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const seedAccount = "billingAccounts/012345-567890-ABCDEF"

func newServer(t *testing.T) string {
	t.Helper()

	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{}))
	t.Cleanup(ts.Close)

	return ts.URL
}

func newBillingService(t *testing.T, url string) *cloudbilling.APIService {
	t.Helper()

	svc, err := cloudbilling.NewService(context.Background(),
		option.WithEndpoint(url), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("cloudbilling.NewService: %v", err)
	}

	return svc
}

func TestSDKBillingAccountsListGet(t *testing.T) {
	svc := newBillingService(t, newServer(t))
	ctx := context.Background()

	list, err := svc.BillingAccounts.List().Context(ctx).Do()
	if err != nil {
		t.Fatalf("BillingAccounts.List: %v", err)
	}

	if len(list.BillingAccounts) != 1 || list.BillingAccounts[0].Name != seedAccount {
		t.Fatalf("List = %+v, want the seeded account %q", list.BillingAccounts, seedAccount)
	}

	got, err := svc.BillingAccounts.Get(seedAccount).Context(ctx).Do()
	if err != nil {
		t.Fatalf("BillingAccounts.Get: %v", err)
	}

	if !got.Open || got.DisplayName == "" || got.CurrencyCode != "USD" {
		t.Fatalf("Get = %+v, want open USD account with a display name", got)
	}

	_, err = svc.BillingAccounts.Get("billingAccounts/does-not-exist").Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 404 {
		t.Fatalf("Get(missing): got %v, want 404", err)
	}
}

func TestSDKBillingAccountCreatePatch(t *testing.T) {
	svc := newBillingService(t, newServer(t))
	ctx := context.Background()

	created, err := svc.BillingAccounts.Create(&cloudbilling.BillingAccount{
		DisplayName: "New Account",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("BillingAccounts.Create: %v", err)
	}

	if !strings.HasPrefix(created.Name, "billingAccounts/") || created.DisplayName != "New Account" {
		t.Fatalf("Create = %+v, want a billingAccounts/ name and the display name", created)
	}

	patched, err := svc.BillingAccounts.Patch(created.Name, &cloudbilling.BillingAccount{
		DisplayName: "Renamed Account",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("BillingAccounts.Patch: %v", err)
	}

	if patched.DisplayName != "Renamed Account" {
		t.Fatalf("Patch displayName = %q, want Renamed Account", patched.DisplayName)
	}
}

func TestSDKProjectBillingInfoRoundTrip(t *testing.T) {
	svc := newBillingService(t, newServer(t))
	ctx := context.Background()

	// A project with no linkage yet reports billing disabled.
	info, err := svc.Projects.GetBillingInfo("projects/demo").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Projects.GetBillingInfo: %v", err)
	}

	if info.BillingEnabled || info.Name != "projects/demo/billingInfo" {
		t.Fatalf("initial GetBillingInfo = %+v, want disabled projects/demo/billingInfo", info)
	}

	// Link the project to the seeded (open) account -> billing enabled.
	updated, err := svc.Projects.UpdateBillingInfo("projects/demo", &cloudbilling.ProjectBillingInfo{
		BillingAccountName: seedAccount,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Projects.UpdateBillingInfo: %v", err)
	}

	if !updated.BillingEnabled || updated.BillingAccountName != seedAccount {
		t.Fatalf("UpdateBillingInfo = %+v, want enabled and linked to %q", updated, seedAccount)
	}

	// The linkage is readable back, and the account lists the linked project.
	reread, err := svc.Projects.GetBillingInfo("projects/demo").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Projects.GetBillingInfo (reread): %v", err)
	}

	if !reread.BillingEnabled || reread.BillingAccountName != seedAccount {
		t.Fatalf("reread = %+v, want the persisted linkage", reread)
	}

	projs, err := svc.BillingAccounts.Projects.List(seedAccount).Context(ctx).Do()
	if err != nil {
		t.Fatalf("BillingAccounts.Projects.List: %v", err)
	}

	if len(projs.ProjectBillingInfo) != 1 || projs.ProjectBillingInfo[0].ProjectId != "demo" {
		t.Fatalf("Projects.List = %+v, want the linked project demo", projs.ProjectBillingInfo)
	}

	// Unlinking disables billing.
	off, err := svc.Projects.UpdateBillingInfo("projects/demo", &cloudbilling.ProjectBillingInfo{
		BillingAccountName: "",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Projects.UpdateBillingInfo (unlink): %v", err)
	}

	if off.BillingEnabled {
		t.Fatalf("after unlink billingEnabled = true, want false")
	}
}

func TestSDKServiceCatalog(t *testing.T) {
	svc := newBillingService(t, newServer(t))
	ctx := context.Background()

	services, err := svc.Services.List().Context(ctx).Do()
	if err != nil {
		t.Fatalf("Services.List: %v", err)
	}

	if len(services.Services) == 0 {
		t.Fatalf("Services.List returned no services")
	}

	svcName := services.Services[0].Name // "services/{id}"

	skus, err := svc.Services.Skus.List(svcName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Services.Skus.List: %v", err)
	}

	if len(skus.Skus) == 0 {
		t.Fatalf("Skus.List(%s) returned no SKUs", svcName)
	}

	got := skus.Skus[0]
	if got.SkuId == "" || len(got.PricingInfo) == 0 {
		t.Fatalf("SKU = %+v, want a skuId and pricing info", got)
	}
}

func TestSDKBudgetLifecycle(t *testing.T) {
	url := newServer(t)
	ctx := context.Background()

	svc, err := billingbudgets.NewService(ctx,
		option.WithEndpoint(url), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("billingbudgets.NewService: %v", err)
	}

	want := &billingbudgets.GoogleCloudBillingBudgetsV1Budget{
		DisplayName: "Monthly cap",
		Amount: &billingbudgets.GoogleCloudBillingBudgetsV1BudgetAmount{
			SpecifiedAmount: &billingbudgets.GoogleTypeMoney{CurrencyCode: "USD", Units: 500},
		},
		ThresholdRules: []*billingbudgets.GoogleCloudBillingBudgetsV1ThresholdRule{
			{ThresholdPercent: 0.9},
		},
	}

	created, err := svc.BillingAccounts.Budgets.Create(seedAccount, want).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Budgets.Create: %v", err)
	}

	if !strings.HasPrefix(created.Name, seedAccount+"/budgets/") {
		t.Fatalf("created budget name = %q, want %s/budgets/{id}", created.Name, seedAccount)
	}

	if created.Amount == nil || created.Amount.SpecifiedAmount == nil ||
		created.Amount.SpecifiedAmount.Units != 500 {
		t.Fatalf("created amount = %+v, want 500 USD units round-tripped", created.Amount)
	}

	got, err := svc.BillingAccounts.Budgets.Get(created.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Budgets.Get: %v", err)
	}

	if got.DisplayName != "Monthly cap" || len(got.ThresholdRules) != 1 {
		t.Fatalf("Get = %+v, want the created budget", got)
	}

	patched, err := svc.BillingAccounts.Budgets.Patch(created.Name,
		&billingbudgets.GoogleCloudBillingBudgetsV1Budget{DisplayName: "Renamed cap"}).
		UpdateMask("displayName").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Budgets.Patch: %v", err)
	}

	if patched.DisplayName != "Renamed cap" {
		t.Fatalf("Patch displayName = %q, want Renamed cap", patched.DisplayName)
	}

	list, err := svc.BillingAccounts.Budgets.List(seedAccount).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Budgets.List: %v", err)
	}

	if len(list.Budgets) != 1 {
		t.Fatalf("List = %d budgets, want 1", len(list.Budgets))
	}

	if _, err := svc.BillingAccounts.Budgets.Delete(created.Name).Context(ctx).Do(); err != nil {
		t.Fatalf("Budgets.Delete: %v", err)
	}

	_, err = svc.BillingAccounts.Budgets.Get(created.Name).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}
