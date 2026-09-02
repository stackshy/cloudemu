package sqlvirtualmachine_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/sqlvirtualmachine"
)

const (
	sub  = "sub-1"
	rg   = "rg-1"
	name = "sqlvm-1"
)

func newMock() *sqlvirtualmachine.Mock {
	return sqlvirtualmachine.New(config.NewOptions())
}

func vmID(vm string) string {
	return "/subscriptions/" + sub + "/resourceGroups/" + rg +
		"/providers/Microsoft.Compute/virtualMachines/" + vm
}

func createInput(vm string) *sqlvirtualmachine.Input {
	return &sqlvirtualmachine.Input{
		Location: "eastus",
		Tags:     map[string]string{"env": "prod", "team": "data"},
		Properties: sqlvirtualmachine.Properties{
			VirtualMachineResourceID: vmID(vm),
			SQLServerLicenseType:     "PAYG",
			SQLImageOffer:            "SQL2022-WS2022",
		},
	}
}

func TestCreateOrUpdateMintsDefaultsAndSucceeds(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	rec, created, err := m.CreateOrUpdate(ctx, sub, rg, name, createInput(name))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if !created {
		t.Error("first create should report created=true")
	}

	if rec.Properties.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", rec.Properties.ProvisioningState)
	}

	// Documented defaults filled in when the caller omits them.
	if rec.Properties.SQLImageSku != "Unknown" {
		t.Errorf("sqlImageSku default = %q, want Unknown", rec.Properties.SQLImageSku)
	}

	if rec.Properties.SQLManagement != "Full" {
		t.Errorf("sqlManagement default = %q, want Full", rec.Properties.SQLManagement)
	}

	if rec.Properties.VirtualMachineResourceID != vmID(name) {
		t.Errorf("virtualMachineResourceId = %q, want %q", rec.Properties.VirtualMachineResourceID, vmID(name))
	}

	want := "/subscriptions/" + sub + "/resourceGroups/" + rg +
		"/providers/Microsoft.SqlVirtualMachine/sqlVirtualMachines/" + name
	if rec.ARMID() != want {
		t.Errorf("ARMID = %q, want %q", rec.ARMID(), want)
	}

	// Second create-or-update on the same name is a replace, not a new create.
	_, created2, err := m.CreateOrUpdate(ctx, sub, rg, name, createInput(name))
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if created2 {
		t.Error("second create should report created=false")
	}
}

func TestCreateOrUpdateRequiresVMResourceID(t *testing.T) {
	m := newMock()

	in := createInput(name)
	in.Properties.VirtualMachineResourceID = ""

	_, _, err := m.CreateOrUpdate(context.Background(), sub, rg, name, in)
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}

func TestGetNotFound(t *testing.T) {
	m := newMock()

	_, err := m.Get(context.Background(), sub, rg, "missing")
	if !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestNestedSettingsRoundTrip(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	patch := json.RawMessage(`{"enable":true,"dayOfWeek":"Sunday","maintenanceWindowStartingHour":2}`)

	in := createInput(name)
	in.Properties.AutoPatchingSettings = patch

	rec, _, err := m.CreateOrUpdate(ctx, sub, rg, name, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := m.Get(ctx, sub, rg, name)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if string(got.Properties.AutoPatchingSettings) != string(patch) {
		t.Errorf("autoPatchingSettings round-trip = %s, want %s", got.Properties.AutoPatchingSettings, patch)
	}

	_ = rec
}

func TestUpdateTagsReplacesWholesale(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.CreateOrUpdate(ctx, sub, rg, name, createInput(name)); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Replace: only the new tag survives; the create-time tags are dropped.
	rec, err := m.UpdateTags(ctx, sub, rg, name, map[string]string{"env": "staging"})
	if err != nil {
		t.Fatalf("update tags: %v", err)
	}

	if _, ok := rec.Tags["team"]; ok {
		t.Errorf("UpdateTags kept stale tag team: %v", rec.Tags)
	}

	if rec.Tags["env"] != "staging" {
		t.Errorf("UpdateTags env = %q, want staging", rec.Tags["env"])
	}

	// Properties are untouched by a tag update.
	if rec.Properties.VirtualMachineResourceID != vmID(name) {
		t.Errorf("UpdateTags altered properties: %+v", rec.Properties)
	}

	// tags:{} wipes.
	wiped, err := m.UpdateTags(ctx, sub, rg, name, map[string]string{})
	if err != nil {
		t.Fatalf("wipe tags: %v", err)
	}

	if len(wiped.Tags) != 0 {
		t.Errorf("tags:{} did not wipe: %v", wiped.Tags)
	}
}

func TestUpdateTagsNotFound(t *testing.T) {
	m := newMock()

	_, err := m.UpdateTags(context.Background(), sub, rg, "missing", nil)
	if !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestListingScopes(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.CreateOrUpdate(ctx, sub, "rg-a", "vm-a", createInputRG("rg-a", "vm-a")); err != nil {
		t.Fatalf("create a: %v", err)
	}

	if _, _, err := m.CreateOrUpdate(ctx, sub, "rg-b", "vm-b", createInputRG("rg-b", "vm-b")); err != nil {
		t.Fatalf("create b: %v", err)
	}

	byRG, err := m.ListByResourceGroup(ctx, sub, "rg-a")
	if err != nil {
		t.Fatalf("list by rg: %v", err)
	}

	if len(byRG) != 1 || byRG[0].Name != "vm-a" {
		t.Fatalf("ListByResourceGroup(rg-a) = %+v, want just vm-a", byRG)
	}

	bySub, err := m.ListBySubscription(ctx, sub)
	if err != nil {
		t.Fatalf("list by sub: %v", err)
	}

	if len(bySub) != 2 {
		t.Fatalf("ListBySubscription = %d records, want 2", len(bySub))
	}
}

func TestDeleteAndPurge(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, _, err := m.CreateOrUpdate(ctx, sub, rg, name, createInput(name)); err != nil {
		t.Fatalf("create: %v", err)
	}

	existed, err := m.Delete(ctx, sub, rg, name)
	if err != nil || !existed {
		t.Fatalf("delete: existed=%v err=%v", existed, err)
	}

	// Idempotent: deleting again reports existed=false.
	existed2, err := m.Delete(ctx, sub, rg, name)
	if err != nil || existed2 {
		t.Fatalf("second delete: existed=%v err=%v", existed2, err)
	}

	// Purge tears down every record in a resource group.
	if _, _, err := m.CreateOrUpdate(ctx, sub, rg, "p1", createInputRG(rg, "p1")); err != nil {
		t.Fatalf("create p1: %v", err)
	}

	if _, _, err := m.CreateOrUpdate(ctx, sub, rg, "p2", createInputRG(rg, "p2")); err != nil {
		t.Fatalf("create p2: %v", err)
	}

	if err := m.PurgeResourceGroup(ctx, sub, rg); err != nil {
		t.Fatalf("purge: %v", err)
	}

	left, err := m.ListByResourceGroup(ctx, sub, rg)
	if err != nil {
		t.Fatalf("list after purge: %v", err)
	}

	if len(left) != 0 {
		t.Fatalf("purge left %d records, want 0", len(left))
	}
}

func createInputRG(group, vm string) *sqlvirtualmachine.Input {
	return &sqlvirtualmachine.Input{
		Location: "eastus",
		Properties: sqlvirtualmachine.Properties{
			VirtualMachineResourceID: "/subscriptions/" + sub + "/resourceGroups/" + group +
				"/providers/Microsoft.Compute/virtualMachines/" + vm,
		},
	}
}
