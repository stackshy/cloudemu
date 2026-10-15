package vault

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func TestCreateOCISecret(t *testing.T) {
	m := newTestMock()
	vaultID, keyID := newVaultAndKey(t, m, testCompartment)

	info, err := m.CreateOCISecret(&SecretSpec{
		CompartmentID: testCompartment,
		VaultID:       vaultID,
		KeyID:         keyID,
		Name:          "db-password",
		Description:   "the database password",
		Content:       []byte("hunter2"),
		ContentName:   "v1",
		FreeformTags:  map[string]string{"env": "test"},
	})
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(info.ID, "ocid1.vaultsecret.oc1.iad."), "got %q", info.ID)
	assert.Equal(t, "db-password", info.Name)
	assert.Equal(t, StateActive, info.LifecycleState)
	assert.Equal(t, int64(1), info.CurrentVersionNumber)
	assert.Equal(t, testCompartment, info.CompartmentID)
	assert.Equal(t, "test", info.FreeformTags["env"])
}

func TestCreateOCISecretRejections(t *testing.T) {
	m := newTestMock()
	vaultID, keyID := newVaultAndKey(t, m, testCompartment)
	otherVault, otherKey := newVaultAndKey(t, m, otherCompart)

	base := &SecretSpec{CompartmentID: testCompartment, VaultID: vaultID, KeyID: keyID, Name: "s"}

	_, err := m.CreateOCISecret(base)
	require.NoError(t, err)

	tests := []struct {
		name   string
		spec   *SecretSpec
		expect cerrors.Code
	}{
		{name: "duplicate name", spec: base, expect: cerrors.AlreadyExists},
		{
			name:   "no name",
			spec:   &SecretSpec{CompartmentID: testCompartment, VaultID: vaultID, KeyID: keyID},
			expect: cerrors.InvalidArgument,
		},
		{
			name:   "no vault",
			spec:   &SecretSpec{CompartmentID: testCompartment, KeyID: keyID, Name: "x"},
			expect: cerrors.InvalidArgument,
		},
		{
			name:   "no key",
			spec:   &SecretSpec{CompartmentID: testCompartment, VaultID: vaultID, Name: "x"},
			expect: cerrors.InvalidArgument,
		},
		{
			name:   "unknown vault",
			spec:   &SecretSpec{CompartmentID: testCompartment, VaultID: "ocid1.vault.oc1.iad.x", KeyID: keyID, Name: "x"},
			expect: cerrors.NotFound,
		},
		{
			name:   "unknown key",
			spec:   &SecretSpec{CompartmentID: testCompartment, VaultID: vaultID, KeyID: "ocid1.key.oc1.iad.x", Name: "x"},
			expect: cerrors.NotFound,
		},
		{
			name:   "key from another vault",
			spec:   &SecretSpec{CompartmentID: testCompartment, VaultID: vaultID, KeyID: otherKey, Name: "x"},
			expect: cerrors.InvalidArgument,
		},
		{
			name:   "vault from another compartment still resolves by OCID",
			spec:   &SecretSpec{CompartmentID: otherCompart, VaultID: otherVault, KeyID: otherKey, Name: "x"},
			expect: cerrors.OK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.CreateOCISecret(tc.spec)
			if tc.expect == cerrors.OK {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Equal(t, tc.expect, cerrors.GetCode(err))
		})
	}
}

func TestListOCISecretsFiltersByCompartmentVaultAndName(t *testing.T) {
	m := newTestMock()
	mine := newSecret(t, m, testCompartment, "mine", "a")
	newSecret(t, m, otherCompart, "theirs", "b")

	got, err := m.ListOCISecrets(testCompartment, "", "")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "mine", got[0].Name)

	byVault, err := m.ListOCISecrets(testCompartment, mine.VaultID, "")
	require.NoError(t, err)
	assert.Len(t, byVault, 1)

	byName, err := m.ListOCISecrets(testCompartment, "", "mine")
	require.NoError(t, err)
	assert.Len(t, byName, 1)

	missing, err := m.ListOCISecrets(testCompartment, "", "nope")
	require.NoError(t, err)
	assert.Empty(t, missing)
}

func TestGetSecretBundleByNameLookups(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "named", "v")

	got, err := m.GetSecretBundleByName(s.VaultID, "named", BundleSelector{})
	require.NoError(t, err)
	assert.Equal(t, s.ID, got.SecretID)

	_, err = m.GetSecretBundleByName(s.VaultID, "other", BundleSelector{})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.GetSecretBundleByName("", "named", BundleSelector{})
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	_, err = m.GetSecretBundleByName(s.VaultID, "", BundleSelector{})
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	_, err = m.GetOCISecret("ocid1.vaultsecret.oc1.iad.x")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestSecretScheduledDeletionAndCancellation(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "doomed", "v")

	scheduled, err := m.ScheduleOCISecretDeletion(s.ID, "")
	require.NoError(t, err)
	assert.Equal(t, StatePendingDeletion, scheduled.LifecycleState)
	assert.Equal(t, "2026-01-31T00:00:00Z", scheduled.TimeOfDeletion)

	// Still addressable by OCID, and still listed by the OCI-shaped surface.
	listed, err := m.ListOCISecrets(testCompartment, "", "")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, StatePendingDeletion, listed[0].LifecycleState)

	_, err = m.ScheduleOCISecretDeletion(s.ID, "")
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	// A secret pending deletion cannot be updated.
	_, err = m.UpdateOCISecret(s.ID, &SecretUpdate{})
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	restored, err := m.CancelOCISecretDeletion(s.ID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, restored.LifecycleState)
	assert.Empty(t, restored.TimeOfDeletion)

	_, err = m.CancelOCISecretDeletion(s.ID)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
}

// A secret pending deletion keeps its name in its vault, as in real OCI, so
// recreating it is refused until the deletion is cancelled or completes.
func TestPendingSecretKeepsItsName(t *testing.T) {
	m := newTestMock()
	first := newSecret(t, m, testCompartment, "reused", "a")

	_, err := m.ScheduleOCISecretDeletion(first.ID, "")
	require.NoError(t, err)

	_, err = m.CreateOCISecret(&SecretSpec{
		CompartmentID: testCompartment,
		VaultID:       first.VaultID,
		KeyID:         first.KeyID,
		Name:          "reused",
		Content:       []byte("b"),
	})
	require.Error(t, err)
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
	assert.Contains(t, err.Error(), "already exists")

	restored, err := m.CancelOCISecretDeletion(first.ID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, restored.LifecycleState)
}

// A secret's key must be ENABLED to encrypt it, at create and at re-key.
func TestSecretsRequireAnEnabledKey(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "keyed", "a")

	other, err := m.CreateKey(&KeySpec{
		CompartmentID: testCompartment,
		VaultID:       s.VaultID,
		DisplayName:   "k2",
		Shape:         KeyShape{Algorithm: AlgorithmAES, Length: 32},
	})
	require.NoError(t, err)

	_, err = m.ScheduleKeyDeletion(other.ID, "")
	require.NoError(t, err)

	_, err = m.CreateOCISecret(&SecretSpec{
		CompartmentID: testCompartment,
		VaultID:       s.VaultID,
		KeyID:         other.ID,
		Name:          "fresh",
		Content:       []byte("b"),
	})
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
}

func TestUpdateOCISecret(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "updatable", "a")

	desc := "now described"

	got, err := m.UpdateOCISecret(s.ID, &SecretUpdate{
		Description:  &desc,
		FreeformTags: map[string]string{"k": "v"},
	})
	require.NoError(t, err)
	assert.Equal(t, "now described", got.Description)
	assert.Equal(t, "v", got.FreeformTags["k"])
	assert.Equal(t, int64(1), got.CurrentVersionNumber)

	_, err = m.UpdateOCISecret("ocid1.vaultsecret.oc1.iad.x", &SecretUpdate{})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestChangeSecretCompartment(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "movable", "a")

	require.NoError(t, m.ChangeSecretCompartment(s.ID, otherCompart))
	assert.Equal(t, otherCompart, m.SecretCompartment(s.ID))

	left, err := m.ListOCISecrets(testCompartment, "", "")
	require.NoError(t, err)
	assert.Empty(t, left)

	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(m.ChangeSecretCompartment(s.ID, "")))
	assert.Equal(t, cerrors.NotFound,
		cerrors.GetCode(m.ChangeSecretCompartment("ocid1.vaultsecret.oc1.iad.x", otherCompart)))
	assert.Empty(t, m.SecretCompartment("ocid1.vaultsecret.oc1.iad.x"))
}

// OCI scopes secret names to the vault, so the same name in two vaults is two
// secrets rather than a conflict.
func TestSecretNamesAreUniquePerVault(t *testing.T) {
	m := newTestMock()
	vaultA, keyA := newVaultAndKey(t, m, testCompartment)
	vaultB, keyB := newVaultAndKey(t, m, testCompartment)

	first, err := m.CreateOCISecret(&SecretSpec{
		CompartmentID: testCompartment,
		VaultID:       vaultA, KeyID: keyA,
		Name: "db-password", Content: []byte("a"),
	})
	require.NoError(t, err)

	second, err := m.CreateOCISecret(&SecretSpec{
		CompartmentID: testCompartment,
		VaultID:       vaultB, KeyID: keyB,
		Name: "db-password", Content: []byte("b"),
	})
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID)
	assert.Equal(t, vaultA, first.VaultID)
	assert.Equal(t, vaultB, second.VaultID)

	// Each vault's getByName resolves to its own secret.
	got, err := m.GetSecretBundleByName(vaultA, "db-password", BundleSelector{})
	require.NoError(t, err)
	assert.Equal(t, first.ID, got.SecretID)

	got, err = m.GetSecretBundleByName(vaultB, "db-password", BundleSelector{})
	require.NoError(t, err)
	assert.Equal(t, second.ID, got.SecretID)

	// Within one vault the name is still taken.
	_, err = m.CreateOCISecret(&SecretSpec{
		CompartmentID: testCompartment,
		VaultID:       vaultA, KeyID: keyA,
		Name: "db-password", Content: []byte("c"),
	})
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))
}

// An unrelated vault reusing the name must not block a restore.
func TestCancelDeletionAllowsTheSameNameInAnotherVault(t *testing.T) {
	m := newTestMock()
	first := newSecret(t, m, testCompartment, "shared", "a")
	vaultB, keyB := newVaultAndKey(t, m, testCompartment)

	_, err := m.ScheduleOCISecretDeletion(first.ID, "")
	require.NoError(t, err)

	_, err = m.CreateOCISecret(&SecretSpec{
		CompartmentID: testCompartment,
		VaultID:       vaultB, KeyID: keyB,
		Name: "shared", Content: []byte("b"),
	})
	require.NoError(t, err)

	restored, err := m.CancelOCISecretDeletion(first.ID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, restored.LifecycleState)
}

// A secret pending deletion is inaccessible at once: neither bundle read
// serves it until the deletion is cancelled.
func TestBundlesRefuseAPendingSecret(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "revoked", "a")

	_, err := m.ScheduleOCISecretDeletion(s.ID, "")
	require.NoError(t, err)

	_, err = m.GetSecretBundle(s.ID, BundleSelector{})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.GetSecretBundleByName(s.VaultID, "revoked", BundleSelector{})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.CancelOCISecretDeletion(s.ID)
	require.NoError(t, err)

	got, err := m.GetSecretBundleByName(s.VaultID, "revoked", BundleSelector{})
	require.NoError(t, err)
	assert.Equal(t, []byte("a"), got.Content)
}
