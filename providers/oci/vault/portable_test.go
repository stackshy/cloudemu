package vault

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

func TestPortableCreateSecretMintsTheDefaultVault(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	info, err := m.CreateSecret(ctx, driver.SecretConfig{
		Name:        "api-key",
		Description: "the api key",
		Tags:        map[string]string{"env": "dev"},
	}, []byte("s3cret"))
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(info.ID, "ocid1.vaultsecret.oc1.iad."), "got %q", info.ID)
	assert.Equal(t, info.ID, info.ResourceID)
	assert.Equal(t, "api-key", info.Name)
	assert.Equal(t, "the api key", info.Description)
	assert.Equal(t, "dev", info.Tags["env"])
	assert.NotEmpty(t, info.CreatedAt)
	assert.NotEmpty(t, info.UpdatedAt)

	// The vault and key OCI requires were created on demand.
	vaults, err := m.ListVaults(testCompartment)
	require.NoError(t, err)
	require.Len(t, vaults, 1)
	assert.Equal(t, defaultVaultName, vaults[0].DisplayName)

	keys, err := m.ListKeys(testCompartment, vaults[0].ID)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, defaultKeyName, keys[0].DisplayName)

	// A second secret reuses them rather than minting another vault.
	_, err = m.CreateSecret(ctx, driver.SecretConfig{Name: "second"}, []byte("v"))
	require.NoError(t, err)

	vaults, err = m.ListVaults(testCompartment)
	require.NoError(t, err)
	assert.Len(t, vaults, 1)
}

func TestPortableCreateSecretErrors(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateSecret(ctx, driver.SecretConfig{}, []byte("v"))
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	_, err = m.CreateSecret(ctx, driver.SecretConfig{Name: "dup"}, []byte("a"))
	require.NoError(t, err)

	_, err = m.CreateSecret(ctx, driver.SecretConfig{Name: "dup"}, []byte("b"))
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))
}

func TestPortableGetAndListSecrets(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	for _, name := range []string{"a", "b"} {
		_, err := m.CreateSecret(ctx, driver.SecretConfig{Name: name}, []byte(name))
		require.NoError(t, err)
	}

	got, err := m.GetSecret(ctx, "a")
	require.NoError(t, err)
	assert.Equal(t, "a", got.Name)

	_, err = m.GetSecret(ctx, "missing")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	list, err := m.ListSecrets(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

// The portable delete maps onto OCI's scheduled deletion: the secret moves to
// PENDING_DELETION at the soonest OCI permits and the portable operations then
// treat it as gone, while the OCI-shaped surface can still bring it back.
func TestPortableDeleteSchedulesDeletion(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	created, err := m.CreateSecret(ctx, driver.SecretConfig{Name: "soft"}, []byte("v"))
	require.NoError(t, err)

	require.NoError(t, m.DeleteSecret(ctx, "soft"))

	oci, err := m.GetOCISecret(created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatePendingDeletion, oci.LifecycleState)
	assert.Equal(t, "2026-01-02T00:00:00Z", oci.TimeOfDeletion)

	// Gone as far as every portable operation is concerned.
	_, err = m.GetSecret(ctx, "soft")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	list, err := m.ListSecrets(ctx)
	require.NoError(t, err)
	assert.Empty(t, list)

	_, err = m.PutSecretValue(ctx, "soft", []byte("v2"))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.GetSecretValue(ctx, "soft", "")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.ListSecretVersions(ctx, "soft")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.DeleteSecret(ctx, "soft")))

	// Cancelling the OCI-side deletion brings it back to the portable view.
	_, err = m.CancelOCISecretDeletion(created.ID)
	require.NoError(t, err)

	back, err := m.GetSecret(ctx, "soft")
	require.NoError(t, err)
	assert.Equal(t, created.ID, back.ID)
}

func TestPortableSecretValues(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateSecret(ctx, driver.SecretConfig{Name: "rotated"}, []byte("one"))
	require.NoError(t, err)

	second, err := m.PutSecretValue(ctx, "rotated", []byte("two"))
	require.NoError(t, err)
	assert.Equal(t, "2", second.VersionID)
	assert.True(t, second.Current)

	current, err := m.GetSecretValue(ctx, "rotated", "")
	require.NoError(t, err)
	assert.Equal(t, []byte("two"), current.Value)

	first, err := m.GetSecretValue(ctx, "rotated", "1")
	require.NoError(t, err)
	assert.Equal(t, []byte("one"), first.Value)
	assert.False(t, first.Current)

	versions, err := m.ListSecretVersions(ctx, "rotated")
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, "1", versions[0].VersionID)

	_, err = m.GetSecretValue(ctx, "rotated", "9")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	// OCI numbers versions, so a non-numeric identifier cannot name one.
	_, err = m.GetSecretValue(ctx, "rotated", "AWSCURRENT")
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
}

// The portable write goes through the same stage ladder as an OCI update.
func TestPortablePutSecretValueStagesLikeOCI(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	created, err := m.CreateSecret(ctx, driver.SecretConfig{Name: "staged"}, []byte("one"))
	require.NoError(t, err)

	_, err = m.PutSecretValue(ctx, "staged", []byte("two"))
	require.NoError(t, err)

	assert.Equal(t, map[int64][]string{
		1: {StagePrevious},
		2: {StageCurrent, StageLatest},
	}, stagesOf(t, m, created.ID))
}
