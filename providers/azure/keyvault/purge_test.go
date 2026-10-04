package keyvault

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

func enabledSecret() driver.KVSetParams {
	return driver.KVSetParams{Value: []byte("v"), Attributes: driver.KVAttributes{Enabled: true}}
}

func TestPurgeResourceGroupDropsVaultsAndContents(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	for name, rg := range map[string]string{"kv1": "Cas1", "kv10": "cas10", "kvbare": ""} {
		if _, err := m.CreateOrUpdateVault(ctx, sampleVaultConfig(name, "s1", rg)); err != nil {
			t.Fatal(err)
		}

		if _, err := m.SetKeyVaultSecret(ctx, name, "s", enabledSecret()); err != nil {
			t.Fatal(err)
		}
	}

	if err := m.PurgeResourceGroup(ctx, "s1", "cas1"); err != nil {
		t.Fatalf("PurgeResourceGroup: %v", err)
	}

	if _, err := m.GetVault(ctx, "kv1"); err == nil {
		t.Error("vault in Cas1 survived the purge")
	}

	if _, err := m.GetKeyVaultSecret(ctx, "kv1", "s", ""); err == nil {
		t.Error("secret of the purged vault survived")
	}

	for _, name := range []string{"kv10", "kvbare"} {
		if _, err := m.GetVault(ctx, name); err != nil {
			t.Errorf("vault %s was purged with cas1: %v", name, err)
		}

		if _, err := m.GetKeyVaultSecret(ctx, name, "s", ""); err != nil {
			t.Errorf("secret of vault %s was purged with cas1: %v", name, err)
		}
	}
}

// TestDeleteVaultDropsContents pins that a recreated vault does not inherit
// the deleted vault's secrets.
func TestDeleteVaultDropsContents(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateOrUpdateVault(ctx, sampleVaultConfig("kv", "s1", "rg")); err != nil {
		t.Fatal(err)
	}

	if _, err := m.SetKeyVaultSecret(ctx, "kv", "s", enabledSecret()); err != nil {
		t.Fatal(err)
	}

	if err := m.DeleteVault(ctx, "kv"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.CreateOrUpdateVault(ctx, sampleVaultConfig("kv", "s1", "rg")); err != nil {
		t.Fatal(err)
	}

	if _, err := m.GetKeyVaultSecret(ctx, "kv", "s", ""); err == nil {
		t.Error("recreated vault inherited the deleted vault's secret")
	}
}
