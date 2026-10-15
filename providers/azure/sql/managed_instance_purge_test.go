package sql

import (
	"context"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

func subnetIn(rg string) string {
	return "/subscriptions/s1/resourceGroups/" + rg + "/providers/Microsoft.Network/virtualNetworks/v/subnets/mi"
}

func TestPurgeResourceGroupManagedInstances(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	for _, c := range []struct{ name, sub, rg string }{
		{"mi-gone", "s1", "RG1"}, {"mi-rg10", "s1", "rg10"}, {"mi-other-sub", "s2", "rg1"},
	} {
		_, err := m.CreateManagedInstance(ctx, rdsdriver.ManagedInstanceConfig{
			Name: c.name, SubnetID: subnetIn(c.rg), Scope: scope.Scope{Subscription: c.sub, ResourceGroup: c.rg},
		})
		requireNoError(t, err)
	}

	_, err := m.CreateManagedDatabase(ctx, rdsdriver.ManagedDatabaseConfig{Instance: "mi-gone", Name: "db1"})
	requireNoError(t, err)

	requireNoError(t, m.PurgeResourceGroup(ctx, "S1", "rg1"))

	tests := []struct {
		name string
		want bool
	}{
		{"mi-gone", false}, {"mi-rg10", true}, {"mi-other-sub", true},
	}

	for _, tc := range tests {
		_, err := m.GetManagedInstance(ctx, tc.name)
		if got := err == nil; got != tc.want {
			t.Errorf("%s exists = %v, want %v", tc.name, got, tc.want)
		}
	}

	if _, err := m.GetManagedDatabase(ctx, "mi-gone", "db1"); err == nil {
		t.Error("managed database of a purged instance survived")
	}
}

// TestRestoreMigratesManagedInstanceScope covers a snapshot taken before
// managed instances recorded their scope: restore derives it from the subnet,
// so the restored instance still joins the resource-group cascade.
func TestRestoreMigratesManagedInstanceScope(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()

	_, err := src.CreateManagedInstance(ctx, rdsdriver.ManagedInstanceConfig{Name: "legacy", SubnetID: subnetIn("rg1")})
	requireNoError(t, err)

	data, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, data))

	mi, err := dst.GetManagedInstance(ctx, "legacy")
	requireNoError(t, err)
	assertEqual(t, scope.Scope{Subscription: "s1", ResourceGroup: "rg1"}, mi.Scope)

	requireNoError(t, dst.PurgeResourceGroup(ctx, "s1", "rg1"))

	if _, err := dst.GetManagedInstance(ctx, "legacy"); err == nil {
		t.Error("migrated instance survived its group's purge")
	}
}
