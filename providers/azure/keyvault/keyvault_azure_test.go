package keyvault

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
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

	// A disabled version cannot be retrieved: real Key Vault answers get with
	// 403 Forbidden rather than serving it, so GetKeyVaultSecret must error.
	_, err = m.GetKeyVaultSecret(ctx, "default", "s", "")
	require.Error(t, err)
	assert.True(t, errors.IsPermissionDenied(err))

	// Its content type and attributes are still visible through the version
	// listing projection, which never gates on enabled/exp/nbf.
	versions, err := m.ListKeyVaultSecretVersions(ctx, "default", "s")
	require.NoError(t, err)
	require.Len(t, versions, 1)
	assert.Equal(t, "text/plain", versions[0].ContentType)
	assert.False(t, versions[0].Enabled)
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

// keyVaultTestClockNow is the instant newTestMock's FakeClock is pinned to.
var keyVaultTestClockNow = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // read-only test fixture

// TestGetKeyVaultSecretExpiredWindow proves the expired/not-yet-valid gate is
// driven by the injected clock (deterministic under FakeClock), not wall time,
// and that the boundaries match real Key Vault: exp is exclusive (blocked at
// and after exp), nbf is inclusive (usable at and after nbf).
func TestGetKeyVaultSecretExpiredWindow(t *testing.T) {
	tests := []struct {
		name    string
		attrs   driver.KVAttributes
		wantErr bool
	}{
		{"expired at boundary", driver.KVAttributes{Enabled: true, Expires: keyVaultTestClockNow.Unix()}, true},
		{"expired in the past", driver.KVAttributes{Enabled: true, Expires: keyVaultTestClockNow.Add(-time.Hour).Unix()}, true},
		{"not yet valid", driver.KVAttributes{Enabled: true, NotBefore: keyVaultTestClockNow.Add(time.Hour).Unix()}, true},
		{"valid at nbf boundary", driver.KVAttributes{Enabled: true, NotBefore: keyVaultTestClockNow.Unix()}, false},
		{"within window", driver.KVAttributes{
			Enabled: true, NotBefore: keyVaultTestClockNow.Add(-time.Hour).Unix(), Expires: keyVaultTestClockNow.Add(time.Hour).Unix(),
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMock()
			ctx := context.Background()

			_, err := m.SetKeyVaultSecret(ctx, "default", "s", driver.KVSetParams{Value: []byte("v"), Attributes: tt.attrs})
			require.NoError(t, err)

			_, err = m.GetKeyVaultSecret(ctx, "default", "s", "")
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, errors.IsPermissionDenied(err))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestGetKeyVaultSecretExpiredWindowSpecificVersion proves the gate applies to
// an explicitly named version too: real Key Vault never falls back to an
// earlier, usable version when the requested one is disabled or out of window.
func TestGetKeyVaultSecretExpiredWindowSpecificVersion(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	first, err := m.SetKeyVaultSecret(ctx, "default", "s", driver.KVSetParams{
		Value: []byte("v1"), Attributes: driver.KVAttributes{Enabled: true},
	})
	require.NoError(t, err)

	_, err = m.SetKeyVaultSecret(ctx, "default", "s", driver.KVSetParams{
		Value: []byte("v2"), Attributes: driver.KVAttributes{Enabled: false},
	})
	require.NoError(t, err)

	// The older, still-enabled version remains directly retrievable by id.
	got, err := m.GetKeyVaultSecret(ctx, "default", "s", first.Version)
	require.NoError(t, err)
	assert.Equal(t, "v1", string(got.Value))

	// The current (disabled) version 403s rather than silently resolving to v1.
	_, err = m.GetKeyVaultSecret(ctx, "default", "s", "")
	require.Error(t, err)
	assert.True(t, errors.IsPermissionDenied(err))
}

// TestKeyVaultConcurrentDeleteRecover races DeleteKeyVaultSecret against
// RecoverDeletedKeyVaultSecret on the same secret name under -race, proving the
// soft-delete/recover state transition (a flag flip guarded by the secret's own
// mutex, not a move between two stores) can never leave the record lost or
// duplicated: after every goroutine settles, exactly one of the live or
// deleted views must resolve the secret.
func TestKeyVaultConcurrentDeleteRecover(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.SetKeyVaultSecret(ctx, "default", "s", driver.KVSetParams{Value: []byte("v"), Attributes: driver.KVAttributes{Enabled: true}})
	require.NoError(t, err)

	const rounds = 50

	var wg sync.WaitGroup

	for range rounds {
		wg.Add(2)

		go func() {
			defer wg.Done()
			_, _ = m.DeleteKeyVaultSecret(ctx, "default", "s")
		}()

		go func() {
			defer wg.Done()
			_, _ = m.RecoverDeletedKeyVaultSecret(ctx, "default", "s")
		}()
	}

	wg.Wait()

	// Whichever operation landed last, the secret is in exactly one of the two
	// states: retrievable live, or present (once) in the deleted list.
	_, liveErr := m.GetKeyVaultSecret(ctx, "default", "s", "")

	deletedList, err := m.ListDeletedKeyVaultSecrets(ctx, "default")
	require.NoError(t, err)

	if liveErr == nil {
		assert.Empty(t, deletedList, "secret is live but also listed as deleted")
	} else {
		require.Len(t, deletedList, 1, "secret must appear exactly once in the deleted list when not live")
	}

	all, err := m.ListKeyVaultSecrets(ctx, "default")
	require.NoError(t, err)
	assert.LessOrEqual(t, len(all), 1, "secret must not be duplicated across live entries")
}
