package search

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/azuresearch/driver"
)

func seedSearchService(t *testing.T, m *Mock, rg, name string) {
	t.Helper()

	ctx := context.Background()

	if _, err := m.CreateService(ctx, driver.ServiceConfig{Name: name, ResourceGroup: rg, Location: "eastus"}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.CreateQueryKey(ctx, rg, name, "q1"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.CreateOrUpdateIndex(ctx, name, driver.Index{Name: "idx",
		Fields: []driver.Field{{Name: "id", Type: "Edm.String", Key: true}}}); err != nil {
		t.Fatal(err)
	}
}

// TestDeleteServiceCascades pins that a recreated service starts empty: the
// deleted service's query keys and indexes are gone with it.
func TestDeleteServiceCascades(t *testing.T) {
	ctx := context.Background()
	m := newSnapshotTestMock()

	seedSearchService(t, m, "rg", "srch")

	if err := m.DeleteService(ctx, "rg", "srch"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.CreateService(ctx, driver.ServiceConfig{Name: "srch", ResourceGroup: "rg", Location: "eastus"}); err != nil {
		t.Fatal(err)
	}

	keys, _ := m.ListQueryKeys(ctx, "rg", "srch")
	for _, k := range keys {
		if k.Name == "q1" {
			t.Error("recreated service inherited the deleted service's query key")
		}
	}

	if _, err := m.GetIndex(ctx, "srch", "idx"); err == nil {
		t.Error("recreated service inherited the deleted service's index")
	}
}

func TestPurgeResourceGroupBoundsByGroup(t *testing.T) {
	ctx := context.Background()
	m := newSnapshotTestMock()

	seedSearchService(t, m, "Cas1", "a")
	seedSearchService(t, m, "cas10", "ab")

	if err := m.PurgeResourceGroup(ctx, "sub-1", "cas1"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.GetService(ctx, "Cas1", "a"); err == nil {
		t.Error("service in Cas1 survived the purge")
	}

	if _, err := m.GetService(ctx, "cas10", "ab"); err != nil {
		t.Errorf("service in cas10 was purged with cas1: %v", err)
	}

	if _, err := m.GetIndex(ctx, "ab", "idx"); err != nil {
		t.Errorf("index of service ab was purged with service a: %v", err)
	}
}
