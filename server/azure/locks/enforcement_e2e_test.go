package locks_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armlocks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	cloudemu "github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// newEnforceServer starts one Azure wire server both the armlocks and
// armresources clients talk to, so a lock created via one client enforces
// against control-plane operations issued via the other.
func newEnforceServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.DriversFrom(cloudP))

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func newLocksClient(t *testing.T, ts *httptest.Server) *armlocks.ManagementLocksClient {
	t.Helper()

	c, err := armlocks.NewManagementLocksClient(testSub, fakeCred{}, armClientOptions(ts))
	if err != nil {
		t.Fatalf("new locks client: %v", err)
	}

	return c
}

func newRGClient(t *testing.T, ts *httptest.Server) *armresources.ResourceGroupsClient {
	t.Helper()

	c, err := armresources.NewResourceGroupsClient(testSub, fakeCred{}, armClientOptions(ts))
	if err != nil {
		t.Fatalf("new rg client: %v", err)
	}

	return c
}

func assertScopeLocked(t *testing.T, op string, err error) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s: want ScopeLocked error, got nil", op)
	}

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("%s: want *azcore.ResponseError, got %T: %v", op, err, err)
	}

	if respErr.StatusCode != http.StatusConflict || respErr.ErrorCode != "ScopeLocked" {
		t.Fatalf("%s: want 409 ScopeLocked, got %d %q", op, respErr.StatusCode, respErr.ErrorCode)
	}
}

func createRG(t *testing.T, rgClient *armresources.ResourceGroupsClient, name string) {
	t.Helper()

	if _, err := rgClient.CreateOrUpdate(context.Background(), name,
		armresources.ResourceGroup{Location: to.Ptr("eastus")}, nil); err != nil {
		t.Fatalf("create rg %q: %v", name, err)
	}
}

func deleteRG(t *testing.T, rgClient *armresources.ResourceGroupsClient, name string) error {
	t.Helper()

	poller, err := rgClient.BeginDelete(context.Background(), name, nil)
	if err != nil {
		return err
	}

	_, err = poller.PollUntilDone(context.Background(), nil)

	return err
}

// TestEnforceCanNotDeleteE2E: a CanNotDelete lock on an RG blocks its delete
// with 409 ScopeLocked, still allows writes, and unlocking (delete the lock via
// the locks API) restores the delete.
func TestEnforceCanNotDeleteE2E(t *testing.T) {
	ts := newEnforceServer(t)
	locksClient := newLocksClient(t, ts)
	rgClient := newRGClient(t, ts)
	ctx := context.Background()

	const rg, lockName = "rg-cnd", "no-delete"

	createRG(t, rgClient, rg)

	if _, err := locksClient.CreateOrUpdateAtResourceGroupLevel(ctx, rg, lockName,
		armlocks.ManagementLockObject{Properties: &armlocks.ManagementLockProperties{
			Level: to.Ptr(armlocks.LockLevelCanNotDelete),
		}}, nil); err != nil {
		t.Fatalf("create lock: %v", err)
	}

	assertScopeLocked(t, "delete under CanNotDelete", deleteRG(t, rgClient, rg))

	// CanNotDelete allows modify: a PUT update of the RG succeeds.
	if _, err := rgClient.CreateOrUpdate(ctx, rg,
		armresources.ResourceGroup{Location: to.Ptr("eastus"), Tags: map[string]*string{"k": to.Ptr("v")}},
		nil); err != nil {
		t.Fatalf("write under CanNotDelete should succeed: %v", err)
	}

	// Unlock via the locks API (must succeed despite covering itself)...
	if _, err := locksClient.DeleteAtResourceGroupLevel(ctx, rg, lockName, nil); err != nil {
		t.Fatalf("delete lock: %v", err)
	}

	// ...and the delete now succeeds.
	if err := deleteRG(t, rgClient, rg); err != nil {
		t.Fatalf("delete after unlock should succeed: %v", err)
	}
}

// TestEnforceReadOnlyE2E: a ReadOnly lock on an RG blocks write (PUT), tag
// update (PATCH) and delete with 409 ScopeLocked, while reads still succeed.
func TestEnforceReadOnlyE2E(t *testing.T) {
	ts := newEnforceServer(t)
	locksClient := newLocksClient(t, ts)
	rgClient := newRGClient(t, ts)
	ctx := context.Background()

	const rg, lockName = "rg-ro", "read-only"

	createRG(t, rgClient, rg)

	if _, err := locksClient.CreateOrUpdateAtResourceGroupLevel(ctx, rg, lockName,
		armlocks.ManagementLockObject{Properties: &armlocks.ManagementLockProperties{
			Level: to.Ptr(armlocks.LockLevelReadOnly),
		}}, nil); err != nil {
		t.Fatalf("create lock: %v", err)
	}

	_, putErr := rgClient.CreateOrUpdate(ctx, rg,
		armresources.ResourceGroup{Location: to.Ptr("eastus"), Tags: map[string]*string{"k": to.Ptr("v")}}, nil)
	assertScopeLocked(t, "PUT under ReadOnly", putErr)

	_, patchErr := rgClient.Update(ctx, rg,
		armresources.ResourceGroupPatchable{Tags: map[string]*string{"k": to.Ptr("v")}}, nil)
	assertScopeLocked(t, "PATCH under ReadOnly", patchErr)

	assertScopeLocked(t, "DELETE under ReadOnly", deleteRG(t, rgClient, rg))

	// Reads are always allowed.
	if _, err := rgClient.Get(ctx, rg, nil); err != nil {
		t.Fatalf("GET under ReadOnly should succeed: %v", err)
	}
}

// TestEnforceInheritanceE2E: a lock at subscription scope blocks a resource
// (resource-group) delete beneath it — inheritance flows downward.
func TestEnforceInheritanceE2E(t *testing.T) {
	ts := newEnforceServer(t)
	locksClient := newLocksClient(t, ts)
	rgClient := newRGClient(t, ts)
	ctx := context.Background()

	const rg, lockName = "rg-inh", "sub-lock"

	createRG(t, rgClient, rg)

	if _, err := locksClient.CreateOrUpdateAtSubscriptionLevel(ctx, lockName,
		armlocks.ManagementLockObject{Properties: &armlocks.ManagementLockProperties{
			Level: to.Ptr(armlocks.LockLevelCanNotDelete),
		}}, nil); err != nil {
		t.Fatalf("create subscription lock: %v", err)
	}

	assertScopeLocked(t, "RG delete under subscription lock", deleteRG(t, rgClient, rg))

	// Removing the subscription lock restores the delete.
	if _, err := locksClient.DeleteAtSubscriptionLevel(ctx, lockName, nil); err != nil {
		t.Fatalf("delete subscription lock: %v", err)
	}

	if err := deleteRG(t, rgClient, rg); err != nil {
		t.Fatalf("delete after unlock should succeed: %v", err)
	}
}

// TestEnforceDataPlaneExemptE2E: locks are control-plane only. Even with a
// ReadOnly lock on the whole subscription, a data-plane-style path (not under
// /subscriptions/) is never answered with ScopeLocked.
func TestEnforceDataPlaneExemptE2E(t *testing.T) {
	ts := newEnforceServer(t)
	locksClient := newLocksClient(t, ts)
	ctx := context.Background()

	if _, err := locksClient.CreateOrUpdateAtSubscriptionLevel(ctx, "ro",
		armlocks.ManagementLockObject{Properties: &armlocks.ManagementLockProperties{
			Level: to.Ptr(armlocks.LockLevelReadOnly),
		}}, nil); err != nil {
		t.Fatalf("create subscription lock: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, ts.URL+"/mycontainer/blob.txt", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("data-plane request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusConflict {
		t.Fatalf("data-plane path was blocked with 409: %s", body)
	}
}
