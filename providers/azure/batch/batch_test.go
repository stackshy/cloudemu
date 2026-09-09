package batch_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/batch"
)

func newMock() *batch.Mock { return batch.New(config.NewOptions()) }

func iptr(v int) *int       { return &v }
func sptr(v string) *string { return &v }

func standardAccount() *batch.AccountInput {
	return &batch.AccountInput{Tags: map[string]string{"env": "dev"}}
}

func createAccount(t *testing.T, m *batch.Mock) batch.Account {
	t.Helper()

	a, isNew, err := m.CreateOrUpdateAccount(context.Background(), "sub", "rg", "acct1", "West US", standardAccount())
	if err != nil || !isNew {
		t.Fatalf("create account: err=%v isNew=%v", err, isNew)
	}

	return a
}

func TestCreateAccountComputedFields(t *testing.T) {
	m := newMock()
	a := createAccount(t, m)

	if a.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", a.ProvisioningState)
	}

	if a.AccountEndpoint != "acct1.westus.batch.azure.com" {
		t.Errorf("accountEndpoint = %q, want acct1.westus.batch.azure.com", a.AccountEndpoint)
	}

	if a.NodeManagementEndpoint != "acct1.westus.service.batch.azure.com" {
		t.Errorf("nodeManagementEndpoint = %q", a.NodeManagementEndpoint)
	}

	if a.PoolAllocationMode != "BatchService" {
		t.Errorf("poolAllocationMode = %q, want BatchService", a.PoolAllocationMode)
	}

	if a.DedicatedCoreQuota != 20 || a.LowPriorityCoreQuota != 20 || a.PoolQuota != 20 ||
		a.ActiveJobAndJobScheduleQuota != 20 {
		t.Errorf("quotas = %d/%d/%d/%d, want 20/20/20/20", a.DedicatedCoreQuota,
			a.LowPriorityCoreQuota, a.PoolQuota, a.ActiveJobAndJobScheduleQuota)
	}

	if a.PrimaryKey == "" || a.SecondaryKey == "" || a.PrimaryKey == a.SecondaryKey {
		t.Errorf("keys not minted distinctly: %q / %q", a.PrimaryKey, a.SecondaryKey)
	}

	if len(a.PrimaryKey) != 88 {
		t.Errorf("key length = %d, want 88", len(a.PrimaryKey))
	}
}

func TestAccountFieldsStableAcrossReads(t *testing.T) {
	m := newMock()
	created := createAccount(t, m)

	first, err := m.GetAccount(context.Background(), "sub", "rg", "acct1")
	if err != nil {
		t.Fatalf("get1: %v", err)
	}

	second, err := m.GetAccount(context.Background(), "sub", "rg", "acct1")
	if err != nil {
		t.Fatalf("get2: %v", err)
	}

	if first.AccountEndpoint != second.AccountEndpoint || first.ProvisioningState != second.ProvisioningState ||
		first.NodeManagementEndpoint != second.NodeManagementEndpoint {
		t.Errorf("computed fields drifted: %+v vs %+v", first, second)
	}

	// Keys are stored but stable; both reads must match the created values.
	if first.PrimaryKey != created.PrimaryKey || first.SecondaryKey != created.SecondaryKey {
		t.Errorf("keys drifted across reads")
	}
}

func TestListKeysStableAndRegenerateOnlyNamedKey(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createAccount(t, m)

	k1, err := m.ListKeys(ctx, "sub", "rg", "acct1")
	if err != nil {
		t.Fatalf("listKeys1: %v", err)
	}

	k2, err := m.ListKeys(ctx, "sub", "rg", "acct1")
	if err != nil {
		t.Fatalf("listKeys2: %v", err)
	}

	if k1 != k2 {
		t.Errorf("listKeys not byte-stable: %+v vs %+v", k1, k2)
	}

	if k1.AccountName != "acct1" {
		t.Errorf("accountName = %q, want acct1", k1.AccountName)
	}

	// Regenerate Primary: Primary changes, Secondary byte-stable.
	reg, err := m.RegenerateKey(ctx, "sub", "rg", "acct1", "Primary")
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}

	if reg.Primary == k1.Primary {
		t.Errorf("Primary key unchanged after regenerate: %q", reg.Primary)
	}

	if reg.Secondary != k1.Secondary {
		t.Errorf("Secondary key changed on Primary regenerate: %q vs %q", reg.Secondary, k1.Secondary)
	}

	// A subsequent listKeys reflects the regenerated Primary and stable Secondary.
	after, err := m.ListKeys(ctx, "sub", "rg", "acct1")
	if err != nil {
		t.Fatalf("listKeys after: %v", err)
	}

	if after.Primary != reg.Primary || after.Secondary != k1.Secondary {
		t.Errorf("listKeys after regenerate = %+v, want primary=%q secondary=%q", after, reg.Primary, k1.Secondary)
	}
}

func TestRegenerateInvalidKeyName(t *testing.T) {
	m := newMock()
	createAccount(t, m)

	if _, err := m.RegenerateKey(context.Background(), "sub", "rg", "acct1", "Tertiary"); !cerrors.IsInvalidArgument(err) {
		t.Errorf("bad keyName: err=%v, want InvalidArgument", err)
	}
}

func TestAccountPatchMergesAndLocationImmutable(t *testing.T) {
	m := newMock()
	created := createAccount(t, m)

	patch := &batch.AccountInput{Tags: map[string]string{"env": "prod"}}

	a, isNew, err := m.CreateOrUpdateAccount(context.Background(), "sub", "rg", "acct1", "East US", patch)
	if err != nil || isNew {
		t.Fatalf("update: err=%v isNew=%v", err, isNew)
	}

	if a.Location != "West US" {
		t.Errorf("location = %q, want West US (immutable)", a.Location)
	}

	if a.Tags["env"] != "prod" {
		t.Errorf("tags after patch = %v, want env=prod (REPLACE)", a.Tags)
	}

	if a.AccountEndpoint != created.AccountEndpoint || a.PrimaryKey != created.PrimaryKey {
		t.Errorf("computed fields drifted on patch")
	}
}

func standardPool() *batch.PoolInput {
	return &batch.PoolInput{
		VMSize:                  sptr("STANDARD_D1_V2"),
		TargetDedicatedNodes:    iptr(3),
		TargetLowPriorityNodes:  iptr(1),
		DeploymentConfiguration: json.RawMessage(`{"virtualMachineConfiguration":{"nodeAgentSkuId":"batch.node.ubuntu 22.04"}}`),
	}
}

func createPool(t *testing.T, m *batch.Mock) batch.Pool {
	t.Helper()

	p, isNew, err := m.CreateOrUpdatePool(context.Background(), "sub", "rg", "acct1", "pool1", standardPool())
	if err != nil || !isNew {
		t.Fatalf("create pool: err=%v isNew=%v", err, isNew)
	}

	return p
}

func TestCreatePoolSettlesSteady(t *testing.T) {
	m := newMock()
	createAccount(t, m)
	p := createPool(t, m)

	if p.AllocationState != "Steady" || p.ProvisioningState != "Succeeded" {
		t.Errorf("states = %q/%q, want Steady/Succeeded", p.AllocationState, p.ProvisioningState)
	}

	if p.TargetDedicatedNodes != 3 || p.CurrentDedicatedNodes != 3 {
		t.Errorf("dedicated nodes target/current = %d/%d, want 3/3", p.TargetDedicatedNodes, p.CurrentDedicatedNodes)
	}

	if p.TargetLowPriorityNodes != 1 || p.CurrentLowPriorityNodes != 1 {
		t.Errorf("low-priority nodes = %d/%d, want 1/1", p.TargetLowPriorityNodes, p.CurrentLowPriorityNodes)
	}

	if p.ResizeTimeout != "PT15M" {
		t.Errorf("resizeTimeout = %q, want PT15M", p.ResizeTimeout)
	}
}

func TestPoolUpdateSettlesSteadyOnNewTarget(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createAccount(t, m)
	createPool(t, m)

	// A PUT that raises the fixed-scale target while the pool is Steady settles
	// immediately: current tracks the new target and allocationState stays Steady
	// (a resize action is the only path to Resizing).
	in := standardPool()
	in.TargetDedicatedNodes = iptr(7)

	p, isNew, err := m.CreateOrUpdatePool(ctx, "sub", "rg", "acct1", "pool1", in)
	if err != nil || isNew {
		t.Fatalf("update pool: err=%v isNew=%v", err, isNew)
	}

	if p.AllocationState != "Steady" {
		t.Errorf("allocationState = %q, want Steady", p.AllocationState)
	}

	if p.TargetDedicatedNodes != 7 || p.CurrentDedicatedNodes != 7 {
		t.Errorf("dedicated target/current = %d/%d, want 7/7", p.TargetDedicatedNodes, p.CurrentDedicatedNodes)
	}
}

func TestPoolCreateRequiresParentAccount(t *testing.T) {
	m := newMock()

	if _, _, err := m.CreateOrUpdatePool(context.Background(), "sub", "rg", "ghost", "pool1", standardPool()); !cerrors.IsNotFound(err) {
		t.Errorf("missing parent: err=%v, want NotFound", err)
	}
}

func TestPoolResizeStateMachine(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createAccount(t, m)
	createPool(t, m)

	// Resize: Steady -> Resizing, targets updated, current pinned.
	resized, err := m.Resize(ctx, "sub", "rg", "acct1", "pool1", iptr(5), iptr(2))
	if err != nil {
		t.Fatalf("resize: %v", err)
	}

	if resized.AllocationState != "Resizing" {
		t.Errorf("allocationState = %q, want Resizing", resized.AllocationState)
	}

	if resized.TargetDedicatedNodes != 5 || resized.TargetLowPriorityNodes != 2 {
		t.Errorf("targets = %d/%d, want 5/2", resized.TargetDedicatedNodes, resized.TargetLowPriorityNodes)
	}

	if resized.CurrentDedicatedNodes != 3 {
		t.Errorf("current dedicated = %d, want 3 (pinned during resize)", resized.CurrentDedicatedNodes)
	}

	// stopResize: Resizing -> Steady, current settles onto targets.
	stopped, err := m.StopResize(ctx, "sub", "rg", "acct1", "pool1")
	if err != nil {
		t.Fatalf("stopResize: %v", err)
	}

	if stopped.AllocationState != "Steady" {
		t.Errorf("allocationState = %q, want Steady", stopped.AllocationState)
	}

	if stopped.CurrentDedicatedNodes != 5 || stopped.CurrentLowPriorityNodes != 2 {
		t.Errorf("current = %d/%d, want 5/2 (settled)", stopped.CurrentDedicatedNodes, stopped.CurrentLowPriorityNodes)
	}
}

func TestStopResizeOnSteadyRejected(t *testing.T) {
	m := newMock()
	createAccount(t, m)
	createPool(t, m)

	if _, err := m.StopResize(context.Background(), "sub", "rg", "acct1", "pool1"); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("stopResize on Steady: err=%v, want FailedPrecondition", err)
	}
}

func TestResizeWhileResizingRejected(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createAccount(t, m)
	createPool(t, m)

	if _, err := m.Resize(ctx, "sub", "rg", "acct1", "pool1", iptr(5), nil); err != nil {
		t.Fatalf("first resize: %v", err)
	}

	if _, err := m.Resize(ctx, "sub", "rg", "acct1", "pool1", iptr(7), nil); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("resize while resizing: err=%v, want FailedPrecondition", err)
	}
}

func TestPoolPatchMergesTagsReplace(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createAccount(t, m)

	in := standardPool()
	in.Tags = map[string]string{"team": "a", "keep": "yes"}

	if _, _, err := m.CreateOrUpdatePool(ctx, "sub", "rg", "acct1", "pool1", in); err != nil {
		t.Fatalf("create: %v", err)
	}

	// PATCH tags only: REPLACE, and vmSize/targets preserved.
	patched, _, err := m.CreateOrUpdatePool(ctx, "sub", "rg", "acct1", "pool1",
		&batch.PoolInput{Tags: map[string]string{"team": "b"}})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	if len(patched.Tags) != 1 || patched.Tags["team"] != "b" {
		t.Errorf("tags after patch = %v, want {team:b} (REPLACE)", patched.Tags)
	}

	if patched.VMSize != "STANDARD_D1_V2" || patched.TargetDedicatedNodes != 3 {
		t.Errorf("preserved fields drifted: vmSize=%q dedicated=%d", patched.VMSize, patched.TargetDedicatedNodes)
	}
}

func TestDeleteAccountCascadesPools(t *testing.T) {
	m := newMock()
	createAccount(t, m)
	createPool(t, m)

	existed, err := m.DeleteAccount(context.Background(), "sub", "rg", "acct1")
	if err != nil || !existed {
		t.Fatalf("delete account: err=%v existed=%v", err, existed)
	}

	if _, err := m.GetPool(context.Background(), "sub", "rg", "acct1", "pool1"); !cerrors.IsNotFound(err) {
		t.Errorf("pool survived account delete: err=%v", err)
	}
}

func TestPoolARMID(t *testing.T) {
	m := newMock()
	createAccount(t, m)
	p := createPool(t, m)

	want := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Batch/batchAccounts/acct1/pools/pool1"
	if p.ARMID() != want {
		t.Errorf("pool ARMID = %q, want %q", p.ARMID(), want)
	}
}

func TestListAccountsAndPools(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createAccount(t, m)
	createPool(t, m)

	if _, _, err := m.CreateOrUpdatePool(ctx, "sub", "rg", "acct1", "pool2", standardPool()); err != nil {
		t.Fatalf("create pool2: %v", err)
	}

	accounts, err := m.ListAccountsByResourceGroup(ctx, "sub", "rg")
	if err != nil || len(accounts) != 1 {
		t.Fatalf("list accounts: err=%v n=%d", err, len(accounts))
	}

	// Keys must never leak through the list/get surface projections at the driver
	// level they are present, but the wire layer omits them (covered in handler test).
	pools, err := m.ListPoolsByAccount(ctx, "sub", "rg", "acct1")
	if err != nil || len(pools) != 2 {
		t.Fatalf("list pools: err=%v n=%d", err, len(pools))
	}

	subList, err := m.ListAccountsBySubscription(ctx, "sub")
	if err != nil || len(subList) != 1 {
		t.Fatalf("list by sub: err=%v n=%d", err, len(subList))
	}
}

func TestPurgeResourceGroupCascades(t *testing.T) {
	m := newMock()
	createAccount(t, m)
	createPool(t, m)

	if err := m.PurgeResourceGroup(context.Background(), "sub", "rg"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	accounts, _ := m.ListAccountsByResourceGroup(context.Background(), "sub", "rg")
	if len(accounts) != 0 {
		t.Errorf("accounts after purge = %d, want 0", len(accounts))
	}

	pools, _ := m.ListPoolsByAccount(context.Background(), "sub", "rg", "acct1")
	if len(pools) != 0 {
		t.Errorf("pools after purge = %d, want 0", len(pools))
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	created := createAccount(t, m)
	createPool(t, m)

	// Regenerate to bump a key generation, so the restore must preserve the
	// regenerated value (not re-mint the gen-0 key).
	reg, err := m.RegenerateKey(ctx, "sub", "rg", "acct1", "Primary")
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}

	data, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored := newMock()
	if err := restored.Restore(ctx, data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	a, err := restored.GetAccount(ctx, "sub", "rg", "acct1")
	if err != nil {
		t.Fatalf("get account after restore: %v", err)
	}

	if a.AccountEndpoint != created.AccountEndpoint {
		t.Errorf("restored account lost endpoint: %q", a.AccountEndpoint)
	}

	keys, err := restored.ListKeys(ctx, "sub", "rg", "acct1")
	if err != nil {
		t.Fatalf("listKeys after restore: %v", err)
	}

	if keys.Primary != reg.Primary || keys.Secondary != reg.Secondary {
		t.Errorf("restored keys drifted: got %+v, want %+v", keys, reg)
	}

	p, err := restored.GetPool(ctx, "sub", "rg", "acct1", "pool1")
	if err != nil {
		t.Fatalf("get pool after restore: %v", err)
	}

	if p.AllocationState != "Steady" || p.TargetDedicatedNodes != 3 {
		t.Errorf("restored pool drifted: %+v", p)
	}
}
