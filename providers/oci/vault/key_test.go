package vault

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func TestCreateKey(t *testing.T) {
	tests := []struct {
		name       string
		shape      KeyShape
		mode       string
		expectErr  cerrors.Code
		expectMode string
	}{
		{
			name:       "AES key defaults to HSM",
			shape:      KeyShape{Algorithm: AlgorithmAES, Length: 32},
			expectMode: ProtectionModeHSM,
		},
		{
			name:       "software RSA key",
			shape:      KeyShape{Algorithm: AlgorithmRSA, Length: 256},
			mode:       ProtectionModeSoftware,
			expectMode: ProtectionModeSoftware,
		},
		{
			name:       "ECDSA key with a curve",
			shape:      KeyShape{Algorithm: AlgorithmECDSA, Length: 32, CurveID: "NIST_P256"},
			expectMode: ProtectionModeHSM,
		},
		{
			name:      "unknown algorithm",
			shape:     KeyShape{Algorithm: "TWOFISH", Length: 32},
			expectErr: cerrors.InvalidArgument,
		},
		{
			name:      "bad length for the algorithm",
			shape:     KeyShape{Algorithm: AlgorithmAES, Length: 17},
			expectErr: cerrors.InvalidArgument,
		},
		{
			name:      "ECDSA without a curve",
			shape:     KeyShape{Algorithm: AlgorithmECDSA, Length: 32},
			expectErr: cerrors.InvalidArgument,
		},
		{
			name:      "curve on a non-ECDSA key",
			shape:     KeyShape{Algorithm: AlgorithmAES, Length: 32, CurveID: "NIST_P256"},
			expectErr: cerrors.InvalidArgument,
		},
		{
			name:      "unknown protection mode",
			shape:     KeyShape{Algorithm: AlgorithmAES, Length: 32},
			mode:      "PAPER",
			expectErr: cerrors.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			v, err := m.CreateVault(VaultSpec{CompartmentID: testCompartment, DisplayName: "v"})
			require.NoError(t, err)

			info, err := m.CreateKey(KeySpec{
				CompartmentID:  testCompartment,
				VaultID:        v.ID,
				DisplayName:    "k",
				Shape:          tc.shape,
				ProtectionMode: tc.mode,
			})
			if tc.expectErr != cerrors.OK {
				require.Error(t, err)
				assert.Equal(t, tc.expectErr, cerrors.GetCode(err))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expectMode, info.ProtectionMode)
			assert.Equal(t, StateActive, info.LifecycleState)
			assert.True(t, strings.HasPrefix(info.ID, "ocid1.key.oc1.iad."), "got %q", info.ID)
			assert.True(t, strings.HasPrefix(info.CurrentKeyVersion, "ocid1.keyversion.oc1.iad."),
				"got %q", info.CurrentKeyVersion)
		})
	}
}

func TestCreateKeyRequiresAnActiveVault(t *testing.T) {
	m := newTestMock()

	v, err := m.CreateVault(VaultSpec{CompartmentID: testCompartment, DisplayName: "v"})
	require.NoError(t, err)

	_, err = m.ScheduleVaultDeletion(v.ID, "")
	require.NoError(t, err)

	shape := KeyShape{Algorithm: AlgorithmAES, Length: 32}

	_, err = m.CreateKey(KeySpec{CompartmentID: testCompartment, VaultID: v.ID, DisplayName: "k", Shape: shape})
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	_, err = m.CreateKey(KeySpec{CompartmentID: testCompartment, VaultID: "ocid1.vault.oc1.iad.x", DisplayName: "k", Shape: shape})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.CreateKey(KeySpec{CompartmentID: testCompartment, DisplayName: "k", Shape: shape})
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

	_, err = m.CreateKey(KeySpec{CompartmentID: testCompartment, VaultID: v.ID, Shape: shape})
	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))
}

func TestListKeysFiltersByCompartmentAndVault(t *testing.T) {
	m := newTestMock()

	vaultA, _ := newVaultAndKey(t, m, testCompartment)
	vaultB, _ := newVaultAndKey(t, m, otherCompart)

	mine, err := m.ListKeys(testCompartment, "")
	require.NoError(t, err)
	require.Len(t, mine, 1)
	assert.Equal(t, vaultA, mine[0].VaultID)

	byVault, err := m.ListKeys(otherCompart, vaultB)
	require.NoError(t, err)
	assert.Len(t, byVault, 1)

	crossed, err := m.ListKeys(testCompartment, vaultB)
	require.NoError(t, err)
	assert.Empty(t, crossed)
}

func TestKeyScheduledDeletionAndCancellation(t *testing.T) {
	m := newTestMock()
	_, keyID := newVaultAndKey(t, m, testCompartment)

	scheduled, err := m.ScheduleKeyDeletion(keyID, "")
	require.NoError(t, err)
	assert.Equal(t, StatePendingDeletion, scheduled.LifecycleState)
	assert.Equal(t, "2026-01-31T00:00:00Z", scheduled.TimeOfDeletion)

	_, err = m.ScheduleKeyDeletion(keyID, "")
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	// A key pending deletion cannot be rotated.
	_, err = m.CreateKeyVersion(keyID)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	restored, err := m.CancelKeyDeletion(keyID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, restored.LifecycleState)

	_, err = m.CancelKeyDeletion(keyID)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
}

func TestKeyRotationCreatesVersions(t *testing.T) {
	m := newTestMock()
	_, keyID := newVaultAndKey(t, m, testCompartment)

	first, err := m.GetKey(keyID)
	require.NoError(t, err)

	rotated, err := m.CreateKeyVersion(keyID)
	require.NoError(t, err)
	assert.NotEqual(t, first.CurrentKeyVersion, rotated.ID)

	after, err := m.GetKey(keyID)
	require.NoError(t, err)
	assert.Equal(t, rotated.ID, after.CurrentKeyVersion)

	versions, err := m.ListKeyVersions(keyID)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, first.CurrentKeyVersion, versions[0].ID)
	assert.Equal(t, rotated.ID, versions[1].ID)

	got, err := m.GetKeyVersion(keyID, rotated.ID)
	require.NoError(t, err)
	assert.Equal(t, keyID, got.KeyID)

	_, err = m.GetKeyVersion(keyID, "ocid1.keyversion.oc1.iad.missing")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.ListKeyVersions("ocid1.key.oc1.iad.missing")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestUpdateAndMoveKey(t *testing.T) {
	m := newTestMock()
	_, keyID := newVaultAndKey(t, m, testCompartment)

	name := "renamed"

	got, err := m.UpdateKey(keyID, Update{DisplayName: &name, FreeformTags: map[string]string{"a": "b"}})
	require.NoError(t, err)
	assert.Equal(t, "renamed", got.DisplayName)
	assert.Equal(t, "b", got.FreeformTags["a"])

	require.NoError(t, m.ChangeKeyCompartment(keyID, otherCompart))
	assert.Equal(t, otherCompart, m.KeyCompartment(keyID))

	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(m.ChangeKeyCompartment(keyID, "")))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.ChangeKeyCompartment("ocid1.key.oc1.iad.x", otherCompart)))
	assert.Empty(t, m.KeyCompartment("ocid1.key.oc1.iad.x"))

	_, err = m.UpdateKey("ocid1.key.oc1.iad.x", Update{})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.GetKey("ocid1.key.oc1.iad.x")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}
