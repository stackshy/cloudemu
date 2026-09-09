package datafactory

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/datafactory/driver"
)

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock()
	mustCreate(t, m, driver.FactoryConfig{
		Name:          "adf1",
		ResourceGroup: "rg1",
		Location:      "eastus",
		Tags:          map[string]string{"env": "prod"},
		Identity:      &driver.ManagedIdentity{Type: "SystemAssigned"},
		GlobalParameters: map[string]driver.GlobalParameterSpec{
			"region": {Type: "String", Value: "east"},
		},
	})

	before, err := m.GetFactory(context.Background(), "rg1", "adf1")
	if err != nil {
		t.Fatalf("GetFactory: %v", err)
	}

	data, err := m.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newMock()
	if err := restored.Restore(context.Background(), data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	after, err := restored.GetFactory(context.Background(), "rg1", "adf1")
	if err != nil {
		t.Fatalf("GetFactory after restore: %v", err)
	}

	if after.ID != before.ID || after.CreateTime != before.CreateTime {
		t.Errorf("identity/createTime not preserved: %+v vs %+v", after, before)
	}

	if after.Identity == nil || after.Identity.PrincipalID != before.Identity.PrincipalID {
		t.Errorf("identity principalId not preserved: %+v", after.Identity)
	}

	if after.GlobalParameters["region"].Value != "east" {
		t.Errorf("globalParameters not preserved: %+v", after.GlobalParameters)
	}
}
