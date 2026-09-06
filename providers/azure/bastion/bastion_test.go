package bastion_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/bastion"
	"github.com/stackshy/cloudemu/v2/services/bastion/driver"
)

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newMock() *bastion.Mock {
	return bastion.New(config.NewOptions())
}

func ptr(b bool) *bool { return &b }

func TestCreateInjectsDefaults(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	stored, created, err := m.CreateOrUpdateBastionHost(ctx, "rg", "b1", driver.BastionHost{Location: "eastus"})
	requireNoError(t, err)

	if !created {
		t.Error("created = false, want true on first create")
	}

	if stored.SKUName != "Standard" {
		t.Errorf("sku = %q, want Standard default", stored.SKUName)
	}

	if stored.ScaleUnits != 2 {
		t.Errorf("scaleUnits = %d, want 2 default", stored.ScaleUnits)
	}

	if stored.DNSName == "" {
		t.Error("dnsName not generated")
	}
}

func TestCreateHonorsExplicitValues(t *testing.T) {
	m := newMock()

	stored, _, err := m.CreateOrUpdateBastionHost(context.Background(), "rg", "b1", driver.BastionHost{
		SKUName:    "Basic",
		ScaleUnits: 5,
	})
	requireNoError(t, err)

	if stored.SKUName != "Basic" {
		t.Errorf("sku = %q, want Basic", stored.SKUName)
	}

	if stored.ScaleUnits != 5 {
		t.Errorf("scaleUnits = %d, want 5", stored.ScaleUnits)
	}
}

func TestDNSNameStableAcrossUpdate(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	first, _, err := m.CreateOrUpdateBastionHost(ctx, "rg", "b1", driver.BastionHost{ScaleUnits: 2})
	requireNoError(t, err)

	dns := first.DNSName

	second, created, err := m.CreateOrUpdateBastionHost(ctx, "rg", "b1", driver.BastionHost{ScaleUnits: 4})
	requireNoError(t, err)

	if created {
		t.Error("created = true on update, want false")
	}

	if second.DNSName != dns {
		t.Errorf("dnsName drifted on update: %q != %q", second.DNSName, dns)
	}
}

func TestDNSNameDeterministic(t *testing.T) {
	// Two independent stores must derive the same dnsName for the same identity.
	a, _, err := newMock().CreateOrUpdateBastionHost(context.Background(), "rg", "b1", driver.BastionHost{})
	requireNoError(t, err)

	b, _, err := newMock().CreateOrUpdateBastionHost(context.Background(), "rg", "b1", driver.BastionHost{})
	requireNoError(t, err)

	if a.DNSName != b.DNSName {
		t.Errorf("dnsName not deterministic: %q != %q", a.DNSName, b.DNSName)
	}

	// A different name must derive a different dnsName.
	c, _, err := newMock().CreateOrUpdateBastionHost(context.Background(), "rg", "b2", driver.BastionHost{})
	requireNoError(t, err)

	if a.DNSName == c.DNSName {
		t.Error("distinct hosts share a dnsName")
	}
}

func TestFlagsPreserved(t *testing.T) {
	m := newMock()

	stored, _, err := m.CreateOrUpdateBastionHost(context.Background(), "rg", "b1", driver.BastionHost{
		DisableCopyPaste: ptr(false),
		EnableTunneling:  ptr(true),
	})
	requireNoError(t, err)

	if stored.DisableCopyPaste == nil || *stored.DisableCopyPaste {
		t.Errorf("disableCopyPaste = %v, want explicit false", stored.DisableCopyPaste)
	}

	if stored.EnableTunneling == nil || !*stored.EnableTunneling {
		t.Errorf("enableTunneling = %v, want true", stored.EnableTunneling)
	}

	// Unset flag stays nil (the wire layer, not the store, projects it to false).
	if stored.EnableFileCopy != nil {
		t.Errorf("unset enableFileCopy = %v, want nil", stored.EnableFileCopy)
	}
}

func TestClonePreventsAliasing(t *testing.T) {
	m := newMock()

	in := driver.BastionHost{
		Tags:             map[string]string{"env": "prod"},
		IPConfigurations: []driver.BastionHostIPConfig{{Name: "c1", SubnetID: "s"}},
		DisableCopyPaste: ptr(true),
	}
	_, _, err := m.CreateOrUpdateBastionHost(context.Background(), "rg", "b1", in)
	requireNoError(t, err)

	// Mutating the caller's inputs must not touch the store.
	in.Tags["env"] = "dev"
	in.IPConfigurations[0].SubnetID = "mutated"
	*in.DisableCopyPaste = false

	got, err := m.GetBastionHost(context.Background(), "rg", "b1")
	requireNoError(t, err)

	if got.Tags["env"] != "prod" {
		t.Errorf("tag aliased: %q", got.Tags["env"])
	}

	if got.IPConfigurations[0].SubnetID != "s" {
		t.Errorf("ipconfig aliased: %q", got.IPConfigurations[0].SubnetID)
	}

	if got.DisableCopyPaste == nil || !*got.DisableCopyPaste {
		t.Errorf("flag pointer aliased: %v", got.DisableCopyPaste)
	}
}

func TestGetNotFound(t *testing.T) {
	_, err := newMock().GetBastionHost(context.Background(), "rg", "missing")
	if !cerrors.IsNotFound(err) {
		t.Errorf("err = %v, want NotFound", err)
	}
}

func TestCreateRequiresName(t *testing.T) {
	_, _, err := newMock().CreateOrUpdateBastionHost(context.Background(), "rg", "", driver.BastionHost{})
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("err = %v, want InvalidArgument", err)
	}
}

func TestDelete(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateOrUpdateBastionHost(ctx, "rg", "b1", driver.BastionHost{})
	requireNoError(t, err)

	requireNoError(t, m.DeleteBastionHost(ctx, "rg", "b1"))

	if err = m.DeleteBastionHost(ctx, "rg", "b1"); !cerrors.IsNotFound(err) {
		t.Errorf("second delete err = %v, want NotFound", err)
	}
}

func TestListScopedAndSorted(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for _, n := range []string{"b-c", "b-a", "b-b"} {
		_, _, err := m.CreateOrUpdateBastionHost(ctx, "rg1", n, driver.BastionHost{})
		requireNoError(t, err)
	}

	_, _, err := m.CreateOrUpdateBastionHost(ctx, "rg2", "other", driver.BastionHost{})
	requireNoError(t, err)

	scoped, err := m.ListBastionHosts(ctx, "rg1")
	requireNoError(t, err)

	if len(scoped) != 3 {
		t.Fatalf("rg1 list len = %d, want 3", len(scoped))
	}

	want := []string{"b-a", "b-b", "b-c"}
	for i := range want {
		if scoped[i].Name != want[i] {
			t.Errorf("list[%d] = %q, want %q", i, scoped[i].Name, want[i])
		}
	}

	all, err := m.ListBastionHosts(ctx, "")
	requireNoError(t, err)

	if len(all) != 4 {
		t.Errorf("subscription-wide list len = %d, want 4", len(all))
	}
}
