package keyvault

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVaultLazyCreateIsRaceSafe exercises the concurrent first-touch path in
// Mock.vault: many goroutines racing to address the same brand-new vault name
// (each writing its own distinct secret) must all end up sharing exactly one
// store for that vault — the SetIfAbsent/Get double-check in vault() must
// never let two goroutines settle on two different winning stores, which
// would silently split the vault's secrets across them. Run with -race.
func TestVaultLazyCreateIsRaceSafe(t *testing.T) {
	m := newTestMock()

	const goroutines = 32

	var wg sync.WaitGroup

	wg.Add(goroutines)

	for i := range goroutines {
		go func(i int) {
			defer wg.Done()

			_, err := m.SetKeyVaultSecret(context.Background(), "race-vault", fmt.Sprintf("s-%d", i),
				driver.KVSetParams{Value: []byte("v"), Attributes: driver.KVAttributes{Enabled: true}})
			assert.NoError(t, err, "goroutine %d", i)
		}(i)
	}

	wg.Wait()

	all, err := m.ListKeyVaultSecrets(context.Background(), "race-vault")
	require.NoError(t, err)
	assert.Len(t, all, goroutines, "vault() must not split concurrent first-touches of a new vault across two different stores")
}

// TestVaultIsolation is the package-internal counterpart to the SDK-level
// isolation regression tests in server/azure/keyvault: the same secret and
// key name in two different vaults must not alias each other, and the
// "default" vault must stay aliased to the shared, vault-less Secrets
// interface for backward compatibility.
func TestVaultIsolation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.SetKeyVaultSecret(ctx, "vault-a", "shared-name", driver.KVSetParams{
		Value: []byte("a-value"), Attributes: driver.KVAttributes{Enabled: true},
	})
	require.NoError(t, err)

	// vault-b must not see vault-a's secret.
	_, err = m.GetKeyVaultSecret(ctx, "vault-b", "shared-name", "")
	require.Error(t, err)

	_, err = m.SetKeyVaultSecret(ctx, "vault-b", "shared-name", driver.KVSetParams{
		Value: []byte("b-value"), Attributes: driver.KVAttributes{Enabled: true},
	})
	require.NoError(t, err)

	gotA, err := m.GetKeyVaultSecret(ctx, "vault-a", "shared-name", "")
	require.NoError(t, err)
	assert.Equal(t, "a-value", string(gotA.Value), "vault-a must be unaffected by vault-b's SetKeyVaultSecret")

	// The "default" vault is the same namespace the shared portable Secrets
	// interface (CreateSecret/GetSecret/...) manages.
	_, err = m.CreateSecret(ctx, driver.SecretConfig{Name: "portable-secret"}, []byte("portable-value"))
	require.NoError(t, err)

	viaKeyVault, err := m.GetKeyVaultSecret(ctx, "default", "portable-secret", "")
	require.NoError(t, err)
	assert.Equal(t, "portable-value", string(viaKeyVault.Value))
}
