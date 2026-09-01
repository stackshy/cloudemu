package locks_test

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armlocks"

	cloudemu "github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const testSub = "00000000-0000-0000-0000-000000000001"

// fakeCred is a static-token credential; the emulator ignores the header but
// the SDK still requires a credential implementation.
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

func newClient(t *testing.T) *armlocks.ManagementLocksClient {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.DriversFrom(cloudP))

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	client, err := armlocks.NewManagementLocksClient(testSub, fakeCred{}, armClientOptions(ts))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	return client
}

// TestSDKResourceGroupLockRoundTrip drives the full create→get→list→delete
// cycle at resource-group scope through the real armlocks client and asserts
// level and notes survive each hop.
func TestSDKResourceGroupLockRoundTrip(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	const rg, lockName, notes = "rg-1", "no-delete", "protect prod"

	created, err := client.CreateOrUpdateAtResourceGroupLevel(ctx, rg, lockName, armlocks.ManagementLockObject{
		Properties: &armlocks.ManagementLockProperties{
			Level: to.Ptr(armlocks.LockLevelCanNotDelete),
			Notes: to.Ptr(notes),
		},
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	assertLock(t, "create", created.ManagementLockObject, lockName, armlocks.LockLevelCanNotDelete, notes)

	if created.ID == nil || *created.ID == "" {
		t.Fatal("create returned empty id")
	}

	got, err := client.GetAtResourceGroupLevel(ctx, rg, lockName, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	assertLock(t, "get", got.ManagementLockObject, lockName, armlocks.LockLevelCanNotDelete, notes)

	names := listNamesAtRG(t, client, rg)
	if len(names) != 1 || !names[lockName] {
		t.Fatalf("list = %v, want just %q", names, lockName)
	}

	if _, err := client.DeleteAtResourceGroupLevel(ctx, rg, lockName, nil); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err = client.GetAtResourceGroupLevel(ctx, rg, lockName, nil)
	if err == nil {
		t.Fatal("get after delete: want error, got nil")
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: want 404 ResponseError, got %v", err)
	}
}

// TestSDKSubscriptionLockRoundTrip proves a second scope (subscription) works,
// preserving the ReadOnly level, and that CreateOrUpdate on an existing lock
// updates it in place rather than duplicating it.
func TestSDKSubscriptionLockRoundTrip(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	const lockName = "sub-readonly"

	if _, err := client.CreateOrUpdateAtSubscriptionLevel(ctx, lockName, armlocks.ManagementLockObject{
		Properties: &armlocks.ManagementLockProperties{Level: to.Ptr(armlocks.LockLevelReadOnly)},
	}, nil); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Update in place: change the notes, keep the level.
	updated, err := client.CreateOrUpdateAtSubscriptionLevel(ctx, lockName, armlocks.ManagementLockObject{
		Properties: &armlocks.ManagementLockProperties{
			Level: to.Ptr(armlocks.LockLevelReadOnly),
			Notes: to.Ptr("locked for audit"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	assertLock(t, "update", updated.ManagementLockObject, lockName, armlocks.LockLevelReadOnly, "locked for audit")

	got, err := client.GetAtSubscriptionLevel(ctx, lockName, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	assertLock(t, "get", got.ManagementLockObject, lockName, armlocks.LockLevelReadOnly, "locked for audit")

	count := 0

	pager := client.NewListAtSubscriptionLevelPager(nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("list: %v", perr)
		}

		for _, l := range page.Value {
			if l.Name != nil && *l.Name == lockName {
				count++
			}
		}
	}

	if count != 1 {
		t.Fatalf("subscription lock appears %d times after update, want exactly 1", count)
	}
}

// TestSDKResourceLevelLockOnExistingHandlerPath is the regression for the
// dispatch-order bug: a lock on an individual resource is addressed at
// .../providers/Microsoft.Compute/virtualMachines/vm1/providers/Microsoft.
// Authorization/locks/lk — a path whose leading providers pair belongs to the
// (registered) VM handler. If the locks handler is not dispatched first this
// returns 501 instead of round-tripping the lock.
func TestSDKResourceLevelLockOnExistingHandlerPath(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	const rg, vm, lockName = "rg-1", "vm-1", "vm-lock"

	created, err := client.CreateOrUpdateAtResourceLevel(ctx, rg,
		"Microsoft.Compute", "", "virtualMachines", vm, lockName,
		armlocks.ManagementLockObject{
			Properties: &armlocks.ManagementLockProperties{Level: to.Ptr(armlocks.LockLevelReadOnly)},
		}, nil)
	if err != nil {
		t.Fatalf("create at resource level: %v", err)
	}

	assertLock(t, "create", created.ManagementLockObject, lockName, armlocks.LockLevelReadOnly, "")

	got, err := client.GetAtResourceLevel(ctx, rg,
		"Microsoft.Compute", "", "virtualMachines", vm, lockName, nil)
	if err != nil {
		t.Fatalf("get at resource level: %v", err)
	}

	assertLock(t, "get", got.ManagementLockObject, lockName, armlocks.LockLevelReadOnly, "")

	if _, err := client.DeleteAtResourceLevel(ctx, rg,
		"Microsoft.Compute", "", "virtualMachines", vm, lockName, nil); err != nil {
		t.Fatalf("delete at resource level: %v", err)
	}
}

// TestSDKResourceLevelAndByScopeAddressSameLock is the regression for the
// scope-normalization bug: a lock created via *AtResourceLevel must be visible
// and deletable via *ByScope for the same logical scope (and vice versa). The
// two SDK variants build subtly different raw paths (a doubled leading slash on
// ByScope, an empty parentResourcePath segment on AtResourceLevel, differing
// resourceGroups casing) that must normalize to one store key.
func TestSDKResourceLevelAndByScopeAddressSameLock(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	const rg, vm = "rg-1", "vm-1"

	// Scope string as a ByScope caller would supply it: single slashes,
	// canonical resourceGroups casing.
	scope := "/subscriptions/" + testSub +
		"/resourceGroups/" + rg +
		"/providers/Microsoft.Compute/virtualMachines/" + vm

	// Create via AtResourceLevel...
	if _, err := client.CreateOrUpdateAtResourceLevel(ctx, rg,
		"Microsoft.Compute", "", "virtualMachines", vm, "lk-a",
		armlocks.ManagementLockObject{
			Properties: &armlocks.ManagementLockProperties{Level: to.Ptr(armlocks.LockLevelCanNotDelete)},
		}, nil); err != nil {
		t.Fatalf("create at resource level: %v", err)
	}

	// ...and read it back via GetByScope for the same logical scope.
	byScope, err := client.GetByScope(ctx, scope, "lk-a", nil)
	if err != nil {
		t.Fatalf("get by scope after create at resource level: %v", err)
	}

	assertLock(t, "byScope", byScope.ManagementLockObject, "lk-a", armlocks.LockLevelCanNotDelete, "")

	// Reverse direction: create via ByScope, delete via AtResourceLevel.
	if _, err := client.CreateOrUpdateByScope(ctx, scope, "lk-b",
		armlocks.ManagementLockObject{
			Properties: &armlocks.ManagementLockProperties{Level: to.Ptr(armlocks.LockLevelReadOnly)},
		}, nil); err != nil {
		t.Fatalf("create by scope: %v", err)
	}

	if _, err := client.GetAtResourceLevel(ctx, rg,
		"Microsoft.Compute", "", "virtualMachines", vm, "lk-b", nil); err != nil {
		t.Fatalf("get at resource level after create by scope: %v", err)
	}

	if _, err := client.DeleteByScope(ctx, scope, "lk-b", nil); err != nil {
		t.Fatalf("delete by scope: %v", err)
	}

	_, err = client.GetAtResourceLevel(ctx, rg,
		"Microsoft.Compute", "", "virtualMachines", vm, "lk-b", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != http.StatusNotFound {
		t.Fatalf("get after by-scope delete: want 404, got %v", err)
	}
}

func listNamesAtRG(t *testing.T, client *armlocks.ManagementLocksClient, rg string) map[string]bool {
	t.Helper()

	names := map[string]bool{}

	pager := client.NewListAtResourceGroupLevelPager(rg, nil)
	for pager.More() {
		page, err := pager.NextPage(context.Background())
		if err != nil {
			t.Fatalf("ListAtResourceGroupLevel: %v", err)
		}

		for _, l := range page.Value {
			if l.Name != nil {
				names[*l.Name] = true
			}
		}
	}

	return names
}

func assertLock(t *testing.T, label string, o armlocks.ManagementLockObject, name string, level armlocks.LockLevel, notes string) {
	t.Helper()

	if o.Name == nil || *o.Name != name {
		t.Errorf("%s name = %v, want %q", label, o.Name, name)
	}

	if o.Type == nil || *o.Type != "Microsoft.Authorization/locks" {
		t.Errorf("%s type = %v, want Microsoft.Authorization/locks", label, o.Type)
	}

	if o.Properties == nil {
		t.Fatalf("%s: nil properties", label)
	}

	if o.Properties.Level == nil || *o.Properties.Level != level {
		t.Errorf("%s level = %v, want %q", label, o.Properties.Level, level)
	}

	if notes == "" {
		return
	}

	if o.Properties.Notes == nil || *o.Properties.Notes != notes {
		t.Errorf("%s notes = %v, want %q", label, o.Properties.Notes, notes)
	}
}
