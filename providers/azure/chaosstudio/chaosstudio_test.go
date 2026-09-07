package chaosstudio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/chaosstudio"
)

func newMock() *chaosstudio.Mock {
	return chaosstudio.New(config.NewOptions())
}

const (
	selectorsJSON = `[{"id":"Selector1","type":"List","filter":null,` +
		`"targets":[{"id":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1` +
		`/providers/Microsoft.Chaos/targets/Microsoft-VirtualMachine","type":"ChaosTarget"}]}]`
	stepsJSON = `[{"name":"step1","branches":[{"name":"branch1","actions":[` +
		`{"type":"continuous","name":"urn:csci:microsoft:virtualMachine:shutdown/1.0","selectorId":"Selector1",` +
		`"duration":"PT10M","parameters":[{"key":"abruptShutdown","value":"false"}]}]}]}]`
)

func standardInput() *chaosstudio.Input {
	return &chaosstudio.Input{
		Tags:      map[string]string{"env": "dev"},
		Identity:  &chaosstudio.Identity{Type: "SystemAssigned"},
		Selectors: json.RawMessage(selectorsJSON),
		Steps:     json.RawMessage(stepsJSON),
	}
}

func createStd(t *testing.T, m *chaosstudio.Mock) chaosstudio.Experiment {
	t.Helper()

	s, isNew, err := m.CreateOrUpdate(context.Background(), "sub", "rg", "exp1", "West US", standardInput())
	if err != nil || !isNew {
		t.Fatalf("create: err=%v isNew=%v", err, isNew)
	}

	return s
}

func TestCreateComputesStableFields(t *testing.T) {
	m := newMock()
	created := createStd(t, m)

	if created.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", created.ProvisioningState)
	}

	if created.Identity == nil || created.Identity.PrincipalID == "" || created.Identity.TenantID == "" {
		t.Errorf("system identity ids not minted: %+v", created.Identity)
	}

	if !bytes.Equal(created.Selectors, json.RawMessage(selectorsJSON)) {
		t.Errorf("selectors not round-tripped verbatim:\n got=%s\nwant=%s", created.Selectors, selectorsJSON)
	}

	if !bytes.Equal(created.Steps, json.RawMessage(stepsJSON)) {
		t.Errorf("steps not round-tripped verbatim:\n got=%s\nwant=%s", created.Steps, stepsJSON)
	}
}

func TestDefaultSelectorsStepsAreEmptyArrays(t *testing.T) {
	s, _, err := newMock().CreateOrUpdate(context.Background(), "sub", "rg", "exp0", "eastus", &chaosstudio.Input{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if string(s.Selectors) != "[]" || string(s.Steps) != "[]" {
		t.Errorf("default selectors/steps = %s / %s, want [] / []", s.Selectors, s.Steps)
	}
}

func TestGetStableAcrossReadsAndUpdates(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	created := createStd(t, m)

	got1, err := m.Get(ctx, "sub", "rg", "exp1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// A tag-only update must not move any computed field, nor the selectors/steps.
	updated, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "exp1", "West US", &chaosstudio.Input{
		Tags: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	for _, tc := range []struct {
		name    string
		a, b, c string
	}{
		{"principalId", created.Identity.PrincipalID, got1.Identity.PrincipalID, updated.Identity.PrincipalID},
		{"tenantId", created.Identity.TenantID, got1.Identity.TenantID, updated.Identity.TenantID},
		{"selectors", string(created.Selectors), string(got1.Selectors), string(updated.Selectors)},
		{"steps", string(created.Steps), string(got1.Steps), string(updated.Steps)},
	} {
		if tc.a != tc.b || tc.b != tc.c {
			t.Errorf("%s drifted: create=%q get=%q update=%q", tc.name, tc.a, tc.b, tc.c)
		}
	}

	if updated.Tags["env"] != "prod" || len(updated.Tags) != 1 {
		t.Errorf("tags not replaced: %v", updated.Tags)
	}

	if updated.Identity == nil {
		t.Errorf("identity wiped by tag-only update")
	}
}

func TestUpdateReplacesSelectorsSteps(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createStd(t, m)

	newSelectors := `[{"id":"S2","type":"List","filter":null,"targets":[]}]`
	updated, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "exp1", "West US", &chaosstudio.Input{
		Selectors: json.RawMessage(newSelectors),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if string(updated.Selectors) != newSelectors {
		t.Errorf("selectors = %s, want %s", updated.Selectors, newSelectors)
	}

	// Steps omitted on the update → preserved.
	if !bytes.Equal(updated.Steps, json.RawMessage(stepsJSON)) {
		t.Errorf("steps not preserved on selector-only update: %s", updated.Steps)
	}
}

func TestExplicitNoneClearsIdentity(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createStd(t, m)

	updated, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "exp1", "West US", &chaosstudio.Input{
		Identity: &chaosstudio.Identity{Type: "None"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if updated.Identity != nil {
		t.Errorf("identity = %+v, want nil after None", updated.Identity)
	}
}

func TestUserAssignedIdentity(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	uaID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/uai1"
	s, _, err := m.CreateOrUpdate(ctx, "sub", "rg", "exp2", "eastus", &chaosstudio.Input{
		Identity: &chaosstudio.Identity{
			Type:         "UserAssigned",
			UserAssigned: map[string]chaosstudio.UserAssignedValue{uaID: {}},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	v, ok := s.Identity.UserAssigned[uaID]
	if !ok || v.PrincipalID == "" || v.ClientID == "" {
		t.Errorf("user-assigned ids not minted: %+v", s.Identity.UserAssigned)
	}

	if s.Identity.PrincipalID != "" || s.Identity.TenantID != "" {
		t.Errorf("unexpected system ids on user-assigned identity: %+v", s.Identity)
	}
}

func TestGetNotFound(t *testing.T) {
	_, err := newMock().Get(context.Background(), "sub", "rg", "missing")
	if !cerrors.IsNotFound(err) {
		t.Errorf("err = %v, want NotFound", err)
	}
}

func TestDeleteIdempotent(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createStd(t, m)

	existed, err := m.Delete(ctx, "sub", "rg", "exp1")
	if err != nil || !existed {
		t.Fatalf("first delete: err=%v existed=%v", err, existed)
	}

	existed, err = m.Delete(ctx, "sub", "rg", "exp1")
	if err != nil || existed {
		t.Fatalf("second delete: err=%v existed=%v, want existed=false", err, existed)
	}
}

func TestListAndPurge(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	for _, n := range []string{"a", "b"} {
		if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg", n, "eastus", &chaosstudio.Input{}); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
	}

	if _, _, err := m.CreateOrUpdate(ctx, "sub", "rg2", "c", "eastus", &chaosstudio.Input{}); err != nil {
		t.Fatalf("create c: %v", err)
	}

	byRG, _ := m.ListByResourceGroup(ctx, "sub", "rg")
	if len(byRG) != 2 {
		t.Errorf("ListByResourceGroup = %d, want 2", len(byRG))
	}

	bySub, _ := m.ListBySubscription(ctx, "sub")
	if len(bySub) != 3 {
		t.Errorf("ListBySubscription = %d, want 3", len(bySub))
	}

	if err := m.PurgeResourceGroup(ctx, "sub", "rg"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	bySub, _ = m.ListBySubscription(ctx, "sub")
	if len(bySub) != 1 {
		t.Errorf("after purge ListBySubscription = %d, want 1", len(bySub))
	}
}

func TestSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	created := createStd(t, m)

	data, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored := newMock()
	if err := restored.Restore(ctx, data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := restored.Get(ctx, "sub", "rg", "exp1")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}

	if got.Identity.PrincipalID != created.Identity.PrincipalID ||
		!bytes.Equal(got.Selectors, created.Selectors) || !bytes.Equal(got.Steps, created.Steps) {
		t.Errorf("restore lost stable fields")
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	if _, _, err := m.CreateOrUpdate(ctx, "", "rg", "n", "eastus", &chaosstudio.Input{}); !cerrors.IsInvalidArgument(err) {
		t.Errorf("empty sub err = %v, want InvalidArgument", err)
	}
}
