package sqlvirtualmachine_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sqlvirtualmachine/armsqlvirtualmachine"

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

// newServer builds an Azure wire server over a fresh in-memory estate and
// returns it along with the shared client options.
func newServer(t *testing.T) (*httptest.Server, *arm.ClientOptions) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.DriversFrom(cloudP))

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return ts, armClientOptions(ts)
}

func newClient(t *testing.T, ts *httptest.Server, opts *arm.ClientOptions) *armsqlvirtualmachine.SQLVirtualMachinesClient {
	t.Helper()

	client, err := armsqlvirtualmachine.NewSQLVirtualMachinesClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	return client
}

// vmResourceID is a plausible Microsoft.Compute VM id the SQL virtual machine
// links to; the emulator stores it verbatim without resolving the VM.
func vmResourceID(rg, vm string) string {
	return "/subscriptions/" + testSub + "/resourceGroups/" + rg +
		"/providers/Microsoft.Compute/virtualMachines/" + vm
}

// createSQLVM runs BeginCreateOrUpdate and polls to completion, asserting the
// LRO settles rather than hanging.
func createSQLVM(
	t *testing.T, ctx context.Context, client *armsqlvirtualmachine.SQLVirtualMachinesClient,
	rg, name string, params armsqlvirtualmachine.SQLVirtualMachine,
) armsqlvirtualmachine.SQLVirtualMachine {
	t.Helper()

	poller, err := client.BeginCreateOrUpdate(ctx, rg, name, params, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate(%s): %v", name, err)
	}

	// The emulator settles the LRO on the first response (terminal
	// provisioningState), so PollUntilDone returns without sleeping.
	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("create poll(%s): %v", name, err)
	}

	return resp.SQLVirtualMachine
}

// TestSDKSQLVirtualMachineLifecycle exercises the full armsqlvirtualmachine
// round-trip: create (PUT LRO settling to Succeeded), get, list by RG and by
// subscription, PATCH tag replace including a tags:{} wipe, delete, and a 404
// afterwards. It also proves a CRUD-created SQL virtual machine surfaces in
// Resource Graph.
func TestSDKSQLVirtualMachineLifecycle(t *testing.T) {
	ts, opts := newServer(t)
	client := newClient(t, ts, opts)
	ctx := context.Background()

	const rg, name = "sql-rg", "sqlvm1"

	created := createSQLVM(t, ctx, client, rg, name, armsqlvirtualmachine.SQLVirtualMachine{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("prod"), "team": to.Ptr("data")},
		Properties: &armsqlvirtualmachine.Properties{
			VirtualMachineResourceID: to.Ptr(vmResourceID(rg, name)),
			SQLServerLicenseType:     to.Ptr(armsqlvirtualmachine.SQLServerLicenseTypePAYG),
			SQLImageSKU:              to.Ptr(armsqlvirtualmachine.SQLImageSKUEnterprise),
			SQLImageOffer:            to.Ptr("SQL2022-WS2022"),
			EnableAutomaticUpgrade:   to.Ptr(true),
			AutoPatchingSettings: &armsqlvirtualmachine.AutoPatchingSettings{
				Enable:                        to.Ptr(true),
				DayOfWeek:                     to.Ptr(armsqlvirtualmachine.DayOfWeekSunday),
				MaintenanceWindowStartingHour: to.Ptr[int32](2),
				MaintenanceWindowDuration:     to.Ptr[int32](60),
			},
		},
	})

	// Create settles to a terminal Succeeded state (no poller hang).
	if created.Properties == nil || created.Properties.ProvisioningState == nil {
		t.Fatalf("create returned nil provisioningState: %+v", created.Properties)
	}

	if got := *created.Properties.ProvisioningState; got != "Succeeded" {
		t.Fatalf("create provisioningState = %q, want Succeeded", got)
	}

	if created.ID == nil || *created.ID == "" {
		t.Fatal("create returned empty id")
	}

	if created.Type == nil || *created.Type != "Microsoft.SqlVirtualMachine/sqlVirtualMachines" {
		t.Errorf("create type = %v, want Microsoft.SqlVirtualMachine/sqlVirtualMachines", created.Type)
	}

	// Get round-trips the linked VM id, the SKU, and the nested patching block.
	got, err := client.Get(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	gp := got.Properties
	if gp == nil || gp.VirtualMachineResourceID == nil || *gp.VirtualMachineResourceID != vmResourceID(rg, name) {
		t.Fatalf("get virtualMachineResourceId = %v, want %q", gp.VirtualMachineResourceID, vmResourceID(rg, name))
	}

	if gp.SQLImageSKU == nil || *gp.SQLImageSKU != armsqlvirtualmachine.SQLImageSKUEnterprise {
		t.Errorf("get sqlImageSku = %v, want Enterprise", gp.SQLImageSKU)
	}

	if gp.AutoPatchingSettings == nil || gp.AutoPatchingSettings.DayOfWeek == nil ||
		*gp.AutoPatchingSettings.DayOfWeek != armsqlvirtualmachine.DayOfWeekSunday {
		t.Errorf("get autoPatchingSettings.dayOfWeek = %+v, want Sunday", gp.AutoPatchingSettings)
	}

	if v := got.Tags["env"]; v == nil || *v != "prod" {
		t.Errorf("get tag env = %v, want prod", v)
	}

	// A second SQL VM in another resource group, to distinguish the two listings.
	createSQLVM(t, ctx, client, "other-rg", "sqlvm2", armsqlvirtualmachine.SQLVirtualMachine{
		Location: to.Ptr("westus"),
		Properties: &armsqlvirtualmachine.Properties{
			VirtualMachineResourceID: to.Ptr(vmResourceID("other-rg", "sqlvm2")),
		},
	})

	assertListByRG(t, ctx, client, rg, name)
	assertListBySub(t, ctx, client, name, "sqlvm2")
	assertResourceGraphLists(t, ts, opts, name)

	assertTagReplace(t, ctx, client, rg, name)
	assertDelete(t, ctx, client, rg, name)
}

// assertTagReplace verifies PATCH UpdateTags replaces the tag set wholesale and
// that an empty map wipes every tag.
func assertTagReplace(
	t *testing.T, ctx context.Context, client *armsqlvirtualmachine.SQLVirtualMachinesClient, rg, name string,
) {
	t.Helper()

	repPoller, err := client.BeginUpdate(ctx, rg, name, armsqlvirtualmachine.Update{
		Tags: map[string]*string{"env": to.Ptr("staging")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}

	replaced, err := repPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("update poll: %v", err)
	}

	// REPLACE, not merge: "team" (set at create) is gone, only "env" remains.
	if _, ok := replaced.Tags["team"]; ok {
		t.Errorf("tag replace kept stale tag team: %v", replaced.Tags)
	}

	if v := replaced.Tags["env"]; v == nil || *v != "staging" {
		t.Errorf("tag replace env = %v, want staging", v)
	}

	// tags:{} wipes every tag.
	wipePoller, err := client.BeginUpdate(ctx, rg, name, armsqlvirtualmachine.Update{
		Tags: map[string]*string{},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate wipe: %v", err)
	}

	wiped, err := wipePoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("wipe poll: %v", err)
	}

	if len(wiped.Tags) != 0 {
		t.Errorf("tags:{} did not wipe tags: %v", wiped.Tags)
	}
}

// assertDelete verifies delete settles and a subsequent Get is a 404.
func assertDelete(
	t *testing.T, ctx context.Context, client *armsqlvirtualmachine.SQLVirtualMachinesClient, rg, name string,
) {
	t.Helper()

	delPoller, err := client.BeginDelete(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err = delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete poll: %v", err)
	}

	_, err = client.Get(ctx, rg, name, nil)
	if err == nil {
		t.Fatal("get after delete: want error, got nil")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: want 404 ResponseError, got %v", err)
	}
}

func assertListByRG(
	t *testing.T, ctx context.Context, client *armsqlvirtualmachine.SQLVirtualMachinesClient, rg, want string,
) {
	t.Helper()

	names := map[string]bool{}

	pager := client.NewListByResourceGroupPager(rg, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListByResourceGroup: %v", err)
		}

		for _, v := range page.Value {
			names[*v.Name] = true
		}
	}

	if len(names) != 1 || !names[want] {
		t.Fatalf("ListByResourceGroup(%s) = %v, want just %q", rg, names, want)
	}
}

func assertListBySub(
	t *testing.T, ctx context.Context, client *armsqlvirtualmachine.SQLVirtualMachinesClient, want ...string,
) {
	t.Helper()

	names := map[string]bool{}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}

		for _, v := range page.Value {
			names[*v.Name] = true
		}
	}

	for _, w := range want {
		if !names[w] {
			t.Fatalf("List = %v, want to contain %q", names, w)
		}
	}
}

// assertResourceGraphLists proves the CRUD-created SQL virtual machine surfaces
// in Resource Graph as a microsoft.sqlvirtualmachine/sqlvirtualmachines row.
func assertResourceGraphLists(t *testing.T, ts *httptest.Server, opts *arm.ClientOptions, name string) {
	t.Helper()

	rgClient, err := armresourcegraph.NewClient(fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new resourcegraph client: %v", err)
	}

	out, err := rgClient.Resources(context.Background(), armresourcegraph.QueryRequest{
		Query: to.Ptr("Resources | where type =~ 'microsoft.sqlvirtualmachine/sqlvirtualmachines'"),
	}, nil)
	if err != nil {
		t.Fatalf("resourcegraph query: %v", err)
	}

	rows, ok := out.Data.([]any)
	if !ok {
		t.Fatalf("resourcegraph data type = %T, want []any", out.Data)
	}

	found := false

	for _, r := range rows {
		row, ok := r.(map[string]any)
		if !ok {
			continue
		}

		if row["name"] == name && row["type"] == "microsoft.sqlvirtualmachine/sqlvirtualmachines" {
			found = true
		}
	}

	if !found {
		t.Fatalf("resource graph did not list CRUD-created SQL VM %q: %v", name, rows)
	}
}

// TestSDKSQLVirtualMachineRequiresVMResourceID verifies a create with no
// virtualMachineResourceId is rejected with a 400, matching real Azure's
// required-property validation.
func TestSDKSQLVirtualMachineRequiresVMResourceID(t *testing.T) {
	ts, opts := newServer(t)
	client := newClient(t, ts, opts)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, "rg", "novm", armsqlvirtualmachine.SQLVirtualMachine{
		Location:   to.Ptr("eastus"),
		Properties: &armsqlvirtualmachine.Properties{},
	}, nil)
	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}

	if err == nil {
		t.Fatal("create without virtualMachineResourceId: want error, got nil")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 ResponseError, got %v", err)
	}
}
