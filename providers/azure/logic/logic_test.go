package logic_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/logic"
)

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) //nolint:gochecknoglobals // fixed test epoch

func newMock() (*logic.Mock, *config.FakeClock) {
	fc := config.NewFakeClock(t0)
	return logic.New(config.NewOptions(config.WithClock(fc))), fc
}

const def = `{"$schema":"https://schema.management.azure.com/providers/Microsoft.Logic/schemas/2016-06-01/workflowdefinition.json#",` +
	`"contentVersion":"1.0.0.0","triggers":{},"actions":{}}`

func TestCreateMintsComputedFields(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	wf, created, err := m.CreateOrUpdate(ctx, "sub", "rg", "wf1", &logic.Input{
		Location:   "West Europe",
		Tags:       map[string]string{"env": "dev"},
		Definition: json.RawMessage(def),
		Parameters: json.RawMessage(`{"p":{"value":1}}`),
		Identity:   &logic.Identity{Type: "SystemAssigned"},
	})
	if err != nil || !created {
		t.Fatalf("create: err=%v created=%v", err, created)
	}

	if wf.State != logic.StateEnabled {
		t.Errorf("default state = %q, want Enabled", wf.State)
	}

	if wf.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q", wf.ProvisioningState)
	}

	if !strings.HasPrefix(wf.AccessEndpoint, "https://prod-00.westeurope.logic.azure.com:443/workflows/") {
		t.Errorf("accessEndpoint = %q", wf.AccessEndpoint)
	}

	if !wf.CreatedTime.Equal(t0) || !wf.ChangedTime.Equal(t0) {
		t.Errorf("times = %v/%v, want %v", wf.CreatedTime, wf.ChangedTime, t0)
	}

	if wf.Version() != "00000000000000000001" {
		t.Errorf("version = %q", wf.Version())
	}

	if string(wf.Definition) != def {
		t.Errorf("definition not verbatim: %s", wf.Definition)
	}

	if wf.Identity == nil || wf.Identity.PrincipalID == "" || wf.Identity.TenantID == "" {
		t.Errorf("identity ids not minted: %+v", wf.Identity)
	}

	if want := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Logic/workflows/wf1"; wf.ARMID() != want {
		t.Errorf("ARMID = %q, want %q", wf.ARMID(), want)
	}
}

func TestUpdatePreservesComputedFieldsAndMovesVersion(t *testing.T) {
	ctx := context.Background()
	m, fc := newMock()

	first, _, _ := m.CreateOrUpdate(ctx, "sub", "rg", "wf1", &logic.Input{Location: "eastus", State: "disabled"})
	if first.State != logic.StateDisabled {
		t.Fatalf("state canonicalisation: got %q", first.State)
	}

	fc.Advance(time.Minute)

	second, created, err := m.CreateOrUpdate(ctx, "sub", "rg", "wf1", &logic.Input{
		Location:   "westus", // immutable: ignored
		Definition: json.RawMessage(def),
	})
	if err != nil || created {
		t.Fatalf("update: err=%v created=%v", err, created)
	}

	if second.Location != "eastus" || second.AccessEndpoint != first.AccessEndpoint {
		t.Errorf("location/accessEndpoint drifted: %q %q", second.Location, second.AccessEndpoint)
	}

	if !second.CreatedTime.Equal(t0) || !second.ChangedTime.Equal(t0.Add(time.Minute)) {
		t.Errorf("times = %v/%v", second.CreatedTime, second.ChangedTime)
	}

	if second.State != logic.StateDisabled {
		t.Errorf("state not preserved when omitted: %q", second.State)
	}

	if second.Version() == first.Version() {
		t.Errorf("version did not move: %q", second.Version())
	}
}

func TestEnableDisableToggleAndKeepVersion(t *testing.T) {
	ctx := context.Background()
	m, fc := newMock()

	created, _, _ := m.CreateOrUpdate(ctx, "sub", "rg", "wf1", &logic.Input{Location: "eastus"})

	fc.Advance(time.Hour)

	disabled, err := m.Disable(ctx, "sub", "RG", "WF1")
	if err != nil {
		t.Fatalf("disable: %v", err)
	}

	if disabled.State != logic.StateDisabled || disabled.Version() != created.Version() {
		t.Errorf("disable: state=%q version=%q", disabled.State, disabled.Version())
	}

	if !disabled.ChangedTime.Equal(t0.Add(time.Hour)) {
		t.Errorf("changedTime = %v", disabled.ChangedTime)
	}

	got, _ := m.Get(ctx, "sub", "rg", "wf1")
	if got.State != logic.StateDisabled {
		t.Errorf("stored state = %q", got.State)
	}

	enabled, err := m.Enable(ctx, "sub", "rg", "wf1")
	if err != nil || enabled.State != logic.StateEnabled {
		t.Errorf("enable: err=%v state=%q", err, enabled.State)
	}

	if _, err := m.Enable(ctx, "sub", "rg", "missing"); !cerrors.IsNotFound(err) {
		t.Errorf("Enable missing: err=%v, want NotFound", err)
	}

	if _, err := m.Disable(ctx, "sub", "rg", "missing"); !cerrors.IsNotFound(err) {
		t.Errorf("Disable missing: err=%v, want NotFound", err)
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	cases := map[string]logic.Input{
		"missing location": {},
		"service state":    {Location: "eastus", State: "Deleted"},
	}

	for name, in := range cases {
		if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "wf", &in); !cerrors.IsInvalidArgument(err) {
			t.Errorf("%s: err=%v, want InvalidArgument", name, err)
		}
	}
}

func TestGetMissingIsNotFound(t *testing.T) {
	m, _ := newMock()

	if _, err := m.Get(context.Background(), "sub", "rg", "nope"); !cerrors.IsNotFound(err) {
		t.Fatalf("err=%v, want NotFound", err)
	}
}

func TestListDeleteAndPurge(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	for _, p := range [][3]string{{"sub", "rg", "b"}, {"sub", "rg", "a"}, {"sub", "rg2", "c"}, {"other", "rg", "d"}} {
		if _, _, err := m.CreateOrUpdate(ctx, p[0], p[1], p[2], &logic.Input{Location: "eastus"}); err != nil {
			t.Fatal(err)
		}
	}

	byRG, _ := m.ListByResourceGroup(ctx, "SUB", "rg")
	if len(byRG) != 2 || byRG[0].Name != "a" || byRG[1].Name != "b" {
		t.Errorf("ListByResourceGroup = %+v", byRG)
	}

	bySub, _ := m.ListBySubscription(ctx, "sub")
	if len(bySub) != 3 {
		t.Errorf("ListBySubscription len = %d, want 3", len(bySub))
	}

	if existed, _ := m.Delete(ctx, "sub", "rg2", "c"); !existed {
		t.Error("delete existing reported missing")
	}

	if existed, _ := m.Delete(ctx, "sub", "rg2", "c"); existed {
		t.Error("second delete reported existing")
	}

	if err := m.PurgeResourceGroup(ctx, "sub", "RG"); err != nil {
		t.Fatal(err)
	}

	all, _ := m.DiscoverWorkflows(ctx)
	if len(all) != 1 || all[0].Name != "d" {
		t.Errorf("after purge = %+v, want only d", all)
	}
}

func TestReturnedValuesDoNotAliasStore(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	wf, _, _ := m.CreateOrUpdate(ctx, "sub", "rg", "wf1", &logic.Input{
		Location:   "eastus",
		Tags:       map[string]string{"k": "v"},
		Definition: json.RawMessage(def),
	})
	wf.Tags["k"] = "mutated"
	wf.Definition[0] = '['

	got, _ := m.Get(ctx, "sub", "rg", "wf1")
	if got.Tags["k"] != "v" || string(got.Definition) != def {
		t.Errorf("store aliased: tags=%v def=%s", got.Tags, got.Definition)
	}
}

func TestSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	m, _ := newMock()

	orig, _, _ := m.CreateOrUpdate(ctx, "sub", "rg", "wf1", &logic.Input{
		Location: "eastus", Definition: json.RawMessage(def), State: logic.StateDisabled,
	})

	data, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatal(err)
	}

	restored, _ := newMock()
	if err := restored.Restore(ctx, data); err != nil {
		t.Fatal(err)
	}

	got, err := restored.Get(ctx, "sub", "rg", "wf1")
	if err != nil {
		t.Fatal(err)
	}

	if got.AccessEndpoint != orig.AccessEndpoint || got.State != logic.StateDisabled ||
		got.Version() != orig.Version() || string(got.Definition) != def {
		t.Errorf("restore drift: got %+v, want %+v", got, orig)
	}
}
