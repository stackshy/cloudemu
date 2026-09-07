package vault

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// TestSnapshotRestoreRoundTrip seeds every store — a vault, a key with a
// rotated second version, and a secret with two versions — snapshots, restores
// into a fresh mock and asserts each resource comes back under its original
// OCID with its cross-references, version stages and tags intact.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	ctx := t.Context()
	src := newTestMock()

	v, err := src.CreateVault(&VaultSpec{
		CompartmentID: testCompartment,
		DisplayName:   "prod",
		FreeformTags:  map[string]string{"env": "prod"},
	})
	require.NoError(t, err)

	k, err := src.CreateKey(&KeySpec{
		CompartmentID: testCompartment,
		VaultID:       v.ID,
		DisplayName:   "master",
		Shape:         KeyShape{Algorithm: AlgorithmAES, Length: 32},
		FreeformTags:  map[string]string{"owner": "platform"},
	})
	require.NoError(t, err)

	// Rotate, so the key-versions store holds more than the create-time one.
	rotated, err := src.CreateKeyVersion(k.ID)
	require.NoError(t, err)

	s, err := src.CreateOCISecret(&SecretSpec{
		CompartmentID: testCompartment,
		VaultID:       v.ID,
		KeyID:         k.ID,
		Name:          "db-password",
		Description:   "the database password",
		Content:       []byte("v1-value"),
		ContentName:   "one",
		FreeformTags:  map[string]string{"tier": "db"},
	})
	require.NoError(t, err)

	// A second version, so the restored secret must carry stages, not just one
	// blob: version 2 becomes CURRENT and version 1 becomes PREVIOUS.
	_, err = src.UpdateOCISecret(s.ID, &SecretUpdate{
		Content: []byte("v2-value"), ContentName: "two", ContentGiven: true,
	})
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	// The vault store.
	gotVault, err := dst.GetVault(v.ID)
	require.NoError(t, err)
	assert.Equal(t, "prod", gotVault.DisplayName)
	assert.Equal(t, StateActive, gotVault.LifecycleState)
	assert.Equal(t, "prod", gotVault.FreeformTags["env"])

	// The key store, still pointing at its vault.
	gotKey, err := dst.GetKey(k.ID)
	require.NoError(t, err)
	assert.Equal(t, v.ID, gotKey.VaultID)
	assert.Equal(t, "master", gotKey.DisplayName)
	assert.Equal(t, KeyShape{Algorithm: AlgorithmAES, Length: 32}, gotKey.Shape)
	assert.Equal(t, "platform", gotKey.FreeformTags["owner"])
	assert.Equal(t, rotated.ID, gotKey.CurrentKeyVersion)

	// The key-version store: both versions survive, keyed to their key.
	versions, err := dst.ListKeyVersions(k.ID)
	require.NoError(t, err)
	require.Len(t, versions, 2)

	for _, kv := range versions {
		assert.Equal(t, k.ID, kv.KeyID)
		assert.Equal(t, v.ID, kv.VaultID)
	}

	gotKV, err := dst.GetKeyVersion(k.ID, rotated.ID)
	require.NoError(t, err)
	assert.Equal(t, rotated.ID, gotKV.ID)

	// The secret store, still pointing at its vault and key.
	gotSecret, err := dst.GetOCISecret(s.ID)
	require.NoError(t, err)
	assert.Equal(t, v.ID, gotSecret.VaultID)
	assert.Equal(t, k.ID, gotSecret.KeyID)
	assert.Equal(t, "db-password", gotSecret.Name)
	assert.Equal(t, "the database password", gotSecret.Description)
	assert.Equal(t, int64(2), gotSecret.CurrentVersionNumber)
	assert.Equal(t, "db", gotSecret.FreeformTags["tier"])

	// getByName still resolves inside the restored vault.
	byName, err := dst.GetOCISecretByName(v.ID, "db-password")
	require.NoError(t, err)
	assert.Equal(t, s.ID, byName.ID)

	// Secret versions and their stages survive.
	secretVersions, err := dst.ListOCISecretVersions(s.ID)
	require.NoError(t, err)
	require.Len(t, secretVersions, 2)
	assert.Equal(t, "one", secretVersions[0].Name)
	assert.Contains(t, secretVersions[0].Stages, StagePrevious)
	assert.Equal(t, "two", secretVersions[1].Name)
	assert.Contains(t, secretVersions[1].Stages, StageCurrent)

	// The values themselves round-trip through the data plane.
	bundle, err := dst.GetSecretBundle(s.ID, BundleSelector{})
	require.NoError(t, err)
	assert.Equal(t, []byte("v2-value"), bundle.Content)

	one := int64(1)

	bundle, err = dst.GetSecretBundle(s.ID, BundleSelector{VersionNumber: &one})
	require.NoError(t, err)
	assert.Equal(t, []byte("v1-value"), bundle.Content)
}

// A deletion scheduled before the snapshot is still pending after the restore,
// with its deletion time, and can still be cancelled.
func TestSnapshotPreservesScheduledDeletion(t *testing.T) {
	ctx := t.Context()
	src := newTestMock()

	s := newSecret(t, src, testCompartment, "doomed", "v")

	scheduled, err := src.ScheduleOCISecretDeletion(s.ID, "")
	require.NoError(t, err)
	require.Equal(t, StatePendingDeletion, scheduled.LifecycleState)

	// A vault-level deletion too, so both lifecycles are covered.
	otherVault, err := src.CreateVault(&VaultSpec{CompartmentID: testCompartment, DisplayName: "going"})
	require.NoError(t, err)

	_, err = src.ScheduleVaultDeletion(otherVault.ID, "")
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	got, err := dst.GetOCISecret(s.ID)
	require.NoError(t, err)
	assert.Equal(t, StatePendingDeletion, got.LifecycleState)
	assert.Equal(t, scheduled.TimeOfDeletion, got.TimeOfDeletion)

	gotVault, err := dst.GetVault(otherVault.ID)
	require.NoError(t, err)
	assert.Equal(t, StatePendingDeletion, gotVault.LifecycleState)

	// The restored secret is still restorable, which proves the pending state
	// came back as state rather than as a frozen projection.
	restored, err := dst.CancelOCISecretDeletion(s.ID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, restored.LifecycleState)
}

// The portable driver's default vault and key must survive, or a restored mock
// would mint a second default vault and orphan the secrets it just restored.
func TestSnapshotPreservesThePortableDefaultVault(t *testing.T) {
	ctx := t.Context()
	src := newTestMock()

	created, err := src.CreateSecret(ctx, driver.SecretConfig{Name: "api-key"}, []byte("secret"))
	require.NoError(t, err)

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	// Readable through the portable surface, under its original OCID.
	got, err := dst.GetSecret(ctx, "api-key")
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)

	value, err := dst.GetSecretValue(ctx, "api-key", "")
	require.NoError(t, err)
	assert.Equal(t, []byte("secret"), value.Value)

	// A further portable create reuses the restored vault rather than minting
	// a second one.
	_, err = dst.CreateSecret(ctx, driver.SecretConfig{Name: "another"}, []byte("v"))
	require.NoError(t, err)

	vaults, err := dst.ListVaults(testCompartment)
	require.NoError(t, err)
	assert.Len(t, vaults, 1)
}

// Restoring must not alias the source mock's values: mutating the restored copy
// leaves the original untouched.
func TestSnapshotRestoreDeepCopies(t *testing.T) {
	ctx := t.Context()
	src := newTestMock()

	s := newSecret(t, src, testCompartment, "shared", "original")

	data, err := src.Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, data))

	_, err = dst.UpdateOCISecret(s.ID, &SecretUpdate{
		Content: []byte("changed"), ContentGiven: true,
		FreeformTags: map[string]string{"touched": "yes"},
	})
	require.NoError(t, err)

	// The source still has one version, its original value and no new tag.
	srcVersions, err := src.ListOCISecretVersions(s.ID)
	require.NoError(t, err)
	assert.Len(t, srcVersions, 1)

	bundle, err := src.GetSecretBundle(s.ID, BundleSelector{})
	require.NoError(t, err)
	assert.Equal(t, []byte("original"), bundle.Content)

	srcSecret, err := src.GetOCISecret(s.ID)
	require.NoError(t, err)
	assert.NotContains(t, srcSecret.FreeformTags, "touched")
}

// An empty snapshot restores cleanly, and malformed JSON is an error rather
// than a panic or a half-loaded store.
func TestSnapshotRestoreEdgeCases(t *testing.T) {
	ctx := t.Context()

	empty, err := newTestMock().Snapshot(ctx, false)
	require.NoError(t, err)

	dst := newTestMock()
	require.NoError(t, dst.Restore(ctx, empty))

	vaults, err := dst.ListVaults(testCompartment)
	require.NoError(t, err)
	assert.Empty(t, vaults)

	require.Error(t, dst.Restore(ctx, []byte("not-json")))
}
