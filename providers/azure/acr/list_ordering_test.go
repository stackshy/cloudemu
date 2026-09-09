package acr

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListRegistriesSortedByName confirms ListRegistries returns a stable,
// alphabetically sorted order rather than the randomized order of the
// underlying map iteration real callers would otherwise observe across
// repeated calls.
func TestListRegistriesSortedByName(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	for _, name := range []string{"zreg", "areg", "mreg"} {
		_, _, err := m.CreateOrUpdateRegistry(ctx, "rg-1", name, driver.AzureRegistryConfig{Location: "eastus"})
		require.NoError(t, err)
	}

	got, err := m.ListRegistries(ctx, "rg-1")
	require.NoError(t, err)
	require.Len(t, got, 3)

	names := []string{got[0].Name, got[1].Name, got[2].Name}
	assert.Equal(t, []string{"areg", "mreg", "zreg"}, names)
}

// TestListWebhooksSortedByName mirrors TestListRegistriesSortedByName for
// webhook sub-resources.
func TestListWebhooksSortedByName(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, _, err := m.CreateOrUpdateRegistry(ctx, "rg-1", "reg1", driver.AzureRegistryConfig{Location: "eastus"})
	require.NoError(t, err)

	for _, name := range []string{"whc", "wha", "whb"} {
		_, _, err := m.CreateOrUpdateWebhook(ctx, "rg-1", "reg1", name, driver.AzureWebhookConfig{Location: "eastus"})
		require.NoError(t, err)
	}

	got, err := m.ListWebhooks(ctx, "rg-1", "reg1")
	require.NoError(t, err)
	require.Len(t, got, 3)

	names := []string{got[0].Name, got[1].Name, got[2].Name}
	assert.Equal(t, []string{"wha", "whb", "whc"}, names)
}

// TestListReplicationsSortedByName mirrors TestListRegistriesSortedByName for
// replication sub-resources.
func TestListReplicationsSortedByName(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	_, _, err := m.CreateOrUpdateRegistry(ctx, "rg-1", "reg1", driver.AzureRegistryConfig{Location: "eastus"})
	require.NoError(t, err)

	for _, loc := range []string{"westus", "eastasia", "westeurope"} {
		_, _, err := m.CreateOrUpdateReplication(ctx, "rg-1", "reg1", loc, driver.AzureReplicationConfig{Location: loc})
		require.NoError(t, err)
	}

	got, err := m.ListReplications(ctx, "rg-1", "reg1")
	require.NoError(t, err)
	require.Len(t, got, 3)

	names := []string{got[0].Name, got[1].Name, got[2].Name}
	assert.Equal(t, []string{"eastasia", "westeurope", "westus"}, names)
}

// TestListRepositoriesSortedByName confirms the data-plane catalog is sorted,
// matching the ARM-side list-ordering guarantee above.
func TestListRepositoriesSortedByName(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	for _, name := range []string{"zapp", "aapp", "mapp"} {
		_, err := m.CreateRepository(ctx, driver.RepositoryConfig{Name: name})
		require.NoError(t, err)
	}

	got, err := m.ListRepositories(ctx)
	require.NoError(t, err)
	require.Len(t, got, 3)

	// Repository.Name holds the full Azure resource ID
	// (".../registries/{name}"); the shared prefix is identical for all three
	// here, so sorting by the full ID is equivalent to sorting by the bare
	// trailing name.
	names := []string{got[0].Name, got[1].Name, got[2].Name}
	assert.Equal(t, []string{
		"/subscriptions/123456789012/resourceGroups/cloudemu-rg/providers/Microsoft.ContainerRegistry/registries/aapp",
		"/subscriptions/123456789012/resourceGroups/cloudemu-rg/providers/Microsoft.ContainerRegistry/registries/mapp",
		"/subscriptions/123456789012/resourceGroups/cloudemu-rg/providers/Microsoft.ContainerRegistry/registries/zapp",
	}, names)
}

// TestListImagesSortedByDigest confirms the manifest listing behind
// /_manifests and /_tags is sorted for a stable order across calls.
func TestListImagesSortedByDigest(t *testing.T) {
	m, _ := newTestMock()
	ctx := context.Background()

	createTestRepo(t, m, "app")

	var digests []string

	for _, tag := range []string{"v1", "v2", "v3"} {
		img, err := m.PutImage(ctx, &driver.ImageManifest{Repository: "app", Tag: tag, SizeBytes: 1})
		require.NoError(t, err)
		digests = append(digests, img.Digest)
	}

	got, err := m.ListImages(ctx, "app")
	require.NoError(t, err)
	require.Len(t, got, 3)

	for i := 1; i < len(got); i++ {
		assert.LessOrEqual(t, got[i-1].Digest, got[i].Digest, "ListImages must be sorted by digest")
	}
}
