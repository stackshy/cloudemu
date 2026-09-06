package frontdoor_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/frontdoor"
	"github.com/stackshy/cloudemu/v2/services/frontdoor/driver"
)

const (
	rg      = "rg-1"
	profile = "profile-1"
)

func newMock() *frontdoor.Mock {
	return frontdoor.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error, msg string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

func TestProfileCRUD(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	stored, created, err := m.CreateOrUpdateProfile(ctx, rg, profile, driver.AzureFrontDoorProfile{
		Location: "global",
		SKUName:  "Standard_AzureFrontDoor",
		Tags:     map[string]string{"env": "prod"},
	})
	requireNoError(t, err, "create profile")

	if !created {
		t.Fatal("first create should report created=true")
	}

	if stored.SKUName != "Standard_AzureFrontDoor" {
		t.Errorf("sku = %q, want Standard_AzureFrontDoor", stored.SKUName)
	}

	// Re-put is an update, not a create.
	_, created, err = m.CreateOrUpdateProfile(ctx, rg, profile, driver.AzureFrontDoorProfile{SKUName: "Premium_AzureFrontDoor"})
	requireNoError(t, err, "update profile")

	if created {
		t.Error("second create should report created=false")
	}

	got, err := m.GetProfile(ctx, rg, profile)
	requireNoError(t, err, "get profile")

	if got.SKUName != "Premium_AzureFrontDoor" {
		t.Errorf("after update sku = %q, want Premium_AzureFrontDoor", got.SKUName)
	}
}

func TestProfileNameRequired(t *testing.T) {
	m := newMock()

	_, _, err := m.CreateOrUpdateProfile(context.Background(), rg, "", driver.AzureFrontDoorProfile{})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("empty name err = %v, want InvalidArgument", err)
	}
}

func TestChildRequiresParentProfile(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateOrUpdateEndpoint(ctx, rg, "missing", "ep-1", driver.AzureFrontDoorEndpoint{})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("endpoint under missing profile err = %v, want NotFound", err)
	}

	_, _, err = m.CreateOrUpdateOriginGroup(ctx, rg, "missing", "og-1", driver.AzureFrontDoorOriginGroup{})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("origin group under missing profile err = %v, want NotFound", err)
	}
}

func TestDeleteProfileCascadesChildren(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateOrUpdateProfile(ctx, rg, profile, driver.AzureFrontDoorProfile{})
	requireNoError(t, err, "create profile")

	_, _, err = m.CreateOrUpdateEndpoint(ctx, rg, profile, "ep-1", driver.AzureFrontDoorEndpoint{})
	requireNoError(t, err, "create endpoint")

	_, _, err = m.CreateOrUpdateOriginGroup(ctx, rg, profile, "og-1", driver.AzureFrontDoorOriginGroup{})
	requireNoError(t, err, "create origin group")

	requireNoError(t, m.DeleteProfile(ctx, rg, profile), "delete profile")

	if _, err = m.GetProfile(ctx, rg, profile); !cerrors.IsNotFound(err) {
		t.Errorf("profile after delete err = %v, want NotFound", err)
	}

	if _, err = m.GetEndpoint(ctx, rg, profile, "ep-1"); !cerrors.IsNotFound(err) {
		t.Errorf("endpoint after cascade err = %v, want NotFound", err)
	}

	if _, err = m.GetOriginGroup(ctx, rg, profile, "og-1"); !cerrors.IsNotFound(err) {
		t.Errorf("origin group after cascade err = %v, want NotFound", err)
	}
}

func TestCloneIsolation(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	tags := map[string]string{"env": "prod"}
	_, _, err := m.CreateOrUpdateProfile(ctx, rg, profile, driver.AzureFrontDoorProfile{Tags: tags})
	requireNoError(t, err, "create profile")

	// Mutating the caller's map must not affect the store.
	tags["env"] = "mutated"

	got, err := m.GetProfile(ctx, rg, profile)
	requireNoError(t, err, "get profile")

	if got.Tags["env"] != "prod" {
		t.Errorf("stored tag mutated to %q; clone not isolated", got.Tags["env"])
	}
}

func TestListProfilesDeterministic(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for _, name := range []string{"p-c", "p-a", "p-b"} {
		_, _, err := m.CreateOrUpdateProfile(ctx, rg, name, driver.AzureFrontDoorProfile{})
		requireNoError(t, err, "create "+name)
	}

	got, err := m.ListProfiles(ctx, rg)
	requireNoError(t, err, "list profiles")

	want := []string{"p-a", "p-b", "p-c"}
	if len(got) != len(want) {
		t.Fatalf("list len = %d, want %d", len(got), len(want))
	}

	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("list[%d] = %q, want %q", i, got[i].Name, want[i])
		}
	}
}

func TestListChildrenScopedToProfile(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for _, p := range []string{"p-1", "p-2"} {
		_, _, err := m.CreateOrUpdateProfile(ctx, rg, p, driver.AzureFrontDoorProfile{})
		requireNoError(t, err, "create "+p)

		_, _, err = m.CreateOrUpdateEndpoint(ctx, rg, p, "ep", driver.AzureFrontDoorEndpoint{})
		requireNoError(t, err, "create endpoint under "+p)
	}

	eps, err := m.ListEndpoints(ctx, rg, "p-1")
	requireNoError(t, err, "list endpoints")

	if len(eps) != 1 || eps[0].Profile != "p-1" {
		t.Errorf("endpoints for p-1 = %+v, want exactly one scoped to p-1", eps)
	}
}
