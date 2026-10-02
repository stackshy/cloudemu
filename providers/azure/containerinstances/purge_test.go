package containerinstances

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/containerinstances/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

func TestPurgeResourceGroup(t *testing.T) {
	ctx := context.Background()
	m := New(config.NewOptions())

	scopes := []scope.Scope{
		{Subscription: "s1", ResourceGroup: "Cas1"},
		{Subscription: "s1", ResourceGroup: "cas10"},
		{},
	}

	for _, sc := range scopes {
		_, err := m.CreateContainerGroup(ctx, driver.ContainerGroupConfig{
			Name: "cg", OSType: "Linux", Scope: sc,
			Containers: []driver.ContainerConfig{{Name: "c", Image: "nginx"}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := m.PurgeResourceGroup(ctx, "s1", "cas1"); err != nil {
		t.Fatalf("PurgeResourceGroup: %v", err)
	}

	if _, err := m.GetContainerGroup(ctx, "s1", "Cas1", "cg"); err == nil {
		t.Error("container group in Cas1 survived the purge")
	}

	if _, err := m.GetContainerGroup(ctx, "s1", "cas10", "cg"); err != nil {
		t.Errorf("container group in cas10 was purged with cas1: %v", err)
	}

	if _, err := m.GetContainerGroup(ctx, "", "", "cg"); err != nil {
		t.Errorf("unscoped container group was purged: %v", err)
	}
}
