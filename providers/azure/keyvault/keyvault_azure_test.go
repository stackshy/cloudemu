package keyvault

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetKeyVaultSecretRoundTrip(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	kv, err := m.SetKeyVaultSecret(ctx, "default", "s", driver.KVSetParams{
		Value:       []byte("v"),
		ContentType: "text/plain",
		Tags:        map[string]string{"k": "val"},
		Attributes:  driver.KVAttributes{Enabled: false, Expires: 111, NotBefore: 222},
	})
	require.NoError(t, err)

	assert.Equal(t, "text/plain", kv.ContentType)
	assert.False(t, kv.Enabled)
	assert.Equal(t, int64(111), kv.Expires)
	assert.Equal(t, int64(222), kv.NotBefore)
	assert.Equal(t, "val", kv.Tags["k"])

	got, err := m.GetKeyVaultSecret(ctx, "default", "s", "")
	require.NoError(t, err)
	assert.Equal(t, "text/plain", got.ContentType)
	assert.False(t, got.Enabled)
}

func TestUpdateKeyVaultSecretPartial(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	set, err := m.SetKeyVaultSecret(ctx, "default", "s", driver.KVSetParams{
		Value:       []byte("v"),
		ContentType: "text/plain",
		Attributes:  driver.KVAttributes{Enabled: true},
	})
	require.NoError(t, err)

	enabled := false
	upd, err := m.UpdateKeyVaultSecret(ctx, "default", "s", set.Version, driver.KVPatch{Enabled: &enabled})
	require.NoError(t, err)

	// Content type untouched, enabled patched.
	assert.Equal(t, "text/plain", upd.ContentType)
	assert.False(t, upd.Enabled)
}

func TestKeyVaultSoftDeleteLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.SetKeyVaultSecret(ctx, "default", "s", driver.KVSetParams{Value: []byte("v"), Attributes: driver.KVAttributes{Enabled: true}})
	require.NoError(t, err)

	del, err := m.DeleteKeyVaultSecret(ctx, "default", "s")
	require.NoError(t, err)
	assert.NotZero(t, del.DeletedDate)
	assert.NotZero(t, del.ScheduledPurgeDate)

	_, err = m.GetKeyVaultSecret(ctx, "default", "s", "")
	require.Error(t, err)

	dl, err := m.ListDeletedKeyVaultSecrets(ctx, "default")
	require.NoError(t, err)
	assert.Len(t, dl, 1)

	_, err = m.RecoverDeletedKeyVaultSecret(ctx, "default", "s")
	require.NoError(t, err)

	_, err = m.GetKeyVaultSecret(ctx, "default", "s", "")
	require.NoError(t, err)
}

func TestKeyVaultPurge(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.SetKeyVaultSecret(ctx, "default", "s", driver.KVSetParams{Value: []byte("v"), Attributes: driver.KVAttributes{Enabled: true}})
	require.NoError(t, err)

	_, err = m.DeleteKeyVaultSecret(ctx, "default", "s")
	require.NoError(t, err)

	require.NoError(t, m.PurgeDeletedKeyVaultSecret(ctx, "default", "s"))

	_, err = m.GetDeletedKeyVaultSecret(ctx, "default", "s")
	require.Error(t, err)
}

func TestKeyVaultBackupRestore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.SetKeyVaultSecret(ctx, "default", "s", driver.KVSetParams{
		Value:       []byte("secret"),
		ContentType: "text/plain",
		Attributes:  driver.KVAttributes{Enabled: true},
	})
	require.NoError(t, err)

	blob, err := m.BackupKeyVaultSecret(ctx, "default", "s")
	require.NoError(t, err)
	require.NotEmpty(t, blob)

	_, err = m.DeleteKeyVaultSecret(ctx, "default", "s")
	require.NoError(t, err)

	require.NoError(t, m.PurgeDeletedKeyVaultSecret(ctx, "default", "s"))

	restored, err := m.RestoreKeyVaultSecret(ctx, "default", blob)
	require.NoError(t, err)
	assert.Equal(t, "secret", string(restored.Value))
	assert.Equal(t, "text/plain", restored.ContentType)
}
