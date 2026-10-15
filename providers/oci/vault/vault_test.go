package vault

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const (
	testCompartment = "ocid1.compartment.oc1..testaaa"
	otherCompart    = "ocid1.compartment.oc1..otherbbb"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	return New(config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-ashburn-1"),
		config.WithCompartmentID(testCompartment),
	))
}

// newVaultAndKey creates a vault with one AES key in the given compartment.
func newVaultAndKey(t *testing.T, m *Mock, compartmentID string) (vaultID, keyID string) {
	t.Helper()

	v, err := m.CreateVault(&VaultSpec{CompartmentID: compartmentID, DisplayName: "v"})
	require.NoError(t, err)

	k, err := m.CreateKey(&KeySpec{
		CompartmentID: compartmentID,
		VaultID:       v.ID,
		DisplayName:   "k",
		Shape:         KeyShape{Algorithm: AlgorithmAES, Length: 32},
	})
	require.NoError(t, err)

	return v.ID, k.ID
}

func newSecret(t *testing.T, m *Mock, compartmentID, name, value string) *SecretInfo {
	t.Helper()

	vaultID, keyID := newVaultAndKey(t, m, compartmentID)

	s, err := m.CreateOCISecret(&SecretSpec{
		CompartmentID: compartmentID,
		VaultID:       vaultID,
		KeyID:         keyID,
		Name:          name,
		Content:       []byte(value),
	})
	require.NoError(t, err)

	return s
}

func TestCreateVault(t *testing.T) {
	tests := []struct {
		name       string
		spec       *VaultSpec
		expectErr  cerrors.Code
		expectType string
	}{
		{
			name:       "defaults to a DEFAULT vault",
			spec:       &VaultSpec{CompartmentID: testCompartment, DisplayName: "v1"},
			expectType: VaultTypeDefault,
		},
		{
			name:       "virtual private",
			spec:       &VaultSpec{CompartmentID: testCompartment, DisplayName: "v2", VaultType: VaultTypeVirtualPrivate},
			expectType: VaultTypeVirtualPrivate,
		},
		{
			name:      "display name required",
			spec:      &VaultSpec{CompartmentID: testCompartment},
			expectErr: cerrors.InvalidArgument,
		},
		{
			name:      "unknown vault type",
			spec:      &VaultSpec{CompartmentID: testCompartment, DisplayName: "v3", VaultType: "SUPER_PRIVATE"},
			expectErr: cerrors.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			info, err := m.CreateVault(tc.spec)
			if tc.expectErr != cerrors.OK {
				require.Error(t, err)
				assert.Equal(t, tc.expectErr, cerrors.GetCode(err))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expectType, info.VaultType)
			assert.Equal(t, StateActive, info.LifecycleState)
			assert.Equal(t, testCompartment, info.CompartmentID)
			assert.True(t, strings.HasPrefix(info.ID, "ocid1.vault.oc1.iad."), "got %q", info.ID)
			assert.Equal(t, EndpointLabel(info.ID), info.EndpointLabel)
			assert.True(t, strings.HasPrefix(info.WrappingKeyID, "ocid1.key.oc1.iad."), "got %q", info.WrappingKeyID)
		})
	}
}

func TestVaultNotFound(t *testing.T) {
	m := newTestMock()

	_, err := m.GetVault("ocid1.vault.oc1.iad.missing")
	require.Error(t, err)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestListVaultsFiltersByCompartment(t *testing.T) {
	m := newTestMock()

	_, err := m.CreateVault(&VaultSpec{CompartmentID: testCompartment, DisplayName: "mine"})
	require.NoError(t, err)

	_, err = m.CreateVault(&VaultSpec{CompartmentID: otherCompart, DisplayName: "theirs"})
	require.NoError(t, err)

	mine, err := m.ListVaults(testCompartment)
	require.NoError(t, err)
	require.Len(t, mine, 1)
	assert.Equal(t, "mine", mine[0].DisplayName)

	theirs, err := m.ListVaults(otherCompart)
	require.NoError(t, err)
	require.Len(t, theirs, 1)
	assert.Equal(t, "theirs", theirs[0].DisplayName)

	none, err := m.ListVaults("ocid1.compartment.oc1..emptyccc")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestUpdateVault(t *testing.T) {
	m := newTestMock()

	v, err := m.CreateVault(&VaultSpec{CompartmentID: testCompartment, DisplayName: "before"})
	require.NoError(t, err)

	name := "after"

	got, err := m.UpdateVault(v.ID, Update{DisplayName: &name, FreeformTags: map[string]string{"env": "test"}})
	require.NoError(t, err)
	assert.Equal(t, "after", got.DisplayName)
	assert.Equal(t, "test", got.FreeformTags["env"])

	_, err = m.UpdateVault("ocid1.vault.oc1.iad.missing", Update{})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestVaultScheduledDeletionAndCancellation(t *testing.T) {
	m := newTestMock()

	v, err := m.CreateVault(&VaultSpec{CompartmentID: testCompartment, DisplayName: "v"})
	require.NoError(t, err)

	scheduled, err := m.ScheduleVaultDeletion(v.ID, "")
	require.NoError(t, err)
	assert.Equal(t, StatePendingDeletion, scheduled.LifecycleState)
	assert.Equal(t, "2026-01-31T00:00:00Z", scheduled.TimeOfDeletion)

	// The vault is still there: OCI schedules, it does not delete.
	got, err := m.GetVault(v.ID)
	require.NoError(t, err)
	assert.Equal(t, StatePendingDeletion, got.LifecycleState)

	_, err = m.ScheduleVaultDeletion(v.ID, "")
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	restored, err := m.CancelVaultDeletion(v.ID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, restored.LifecycleState)
	assert.Empty(t, restored.TimeOfDeletion)

	_, err = m.CancelVaultDeletion(v.ID)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))
}

// Scheduling a vault's deletion takes every key not already pending with it,
// and canceling restores exactly those keys.
func TestVaultDeletionCascadesToItsKeys(t *testing.T) {
	m := newTestMock()
	vaultID, moved := newVaultAndKey(t, m, testCompartment)

	own, err := m.CreateKey(&KeySpec{
		CompartmentID: testCompartment, VaultID: vaultID, DisplayName: "own",
		Shape: KeyShape{Algorithm: AlgorithmAES, Length: 32},
	})
	require.NoError(t, err)

	_, err = m.ScheduleKeyDeletion(own.ID, "2026-01-10T00:00:00Z")
	require.NoError(t, err)

	_, untouched := newVaultAndKey(t, m, testCompartment)

	_, err = m.ScheduleVaultDeletion(vaultID, "2026-01-20T00:00:00Z")
	require.NoError(t, err)

	k, err := m.GetKey(moved)
	require.NoError(t, err)
	assert.Equal(t, StatePendingDeletion, k.LifecycleState)
	assert.Equal(t, "2026-01-20T00:00:00Z", k.TimeOfDeletion)

	k, err = m.GetKey(own.ID)
	require.NoError(t, err)
	assert.Equal(t, "2026-01-10T00:00:00Z", k.TimeOfDeletion, "an earlier key deletion is kept")

	k, err = m.GetKey(untouched)
	require.NoError(t, err)
	assert.Equal(t, StateEnabled, k.LifecycleState)

	_, err = m.CancelVaultDeletion(vaultID)
	require.NoError(t, err)

	k, err = m.GetKey(moved)
	require.NoError(t, err)
	assert.Equal(t, StateEnabled, k.LifecycleState)
	assert.Empty(t, k.TimeOfDeletion)

	k, err = m.GetKey(own.ID)
	require.NoError(t, err)
	assert.Equal(t, StatePendingDeletion, k.LifecycleState, "a key scheduled on its own stays scheduled")
}

// A key already due after the vault would outlive it, so OCI refuses.
func TestVaultDeletionRefusesAKeyDueLater(t *testing.T) {
	m := newTestMock()
	vaultID, keyID := newVaultAndKey(t, m, testCompartment)

	_, err := m.ScheduleKeyDeletion(keyID, "2026-01-25T00:00:00Z")
	require.NoError(t, err)

	_, err = m.ScheduleVaultDeletion(vaultID, "2026-01-20T00:00:00Z")
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	v, err := m.GetVault(vaultID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, v.LifecycleState)
}

func TestVaultDeletionWindow(t *testing.T) {
	tests := []struct {
		name      string
		at        string
		expectErr bool
	}{
		{name: "inside the window", at: "2026-01-15T00:00:00Z"},
		{name: "too soon", at: "2026-01-02T00:00:00Z", expectErr: true},
		{name: "too late", at: "2026-06-01T00:00:00Z", expectErr: true},
		{name: "not a timestamp", at: "next tuesday", expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			v, err := m.CreateVault(&VaultSpec{CompartmentID: testCompartment, DisplayName: "v"})
			require.NoError(t, err)

			got, err := m.ScheduleVaultDeletion(v.ID, tc.at)
			if tc.expectErr {
				require.Error(t, err)
				assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.at, got.TimeOfDeletion)
		})
	}
}

func TestChangeVaultCompartment(t *testing.T) {
	m := newTestMock()

	v, err := m.CreateVault(&VaultSpec{CompartmentID: testCompartment, DisplayName: "v"})
	require.NoError(t, err)

	require.NoError(t, m.ChangeVaultCompartment(v.ID, otherCompart))

	movedVault, err := m.GetVault(v.ID)
	require.NoError(t, err)
	assert.Equal(t, otherCompart, movedVault.CompartmentID)

	moved, err := m.ListVaults(otherCompart)
	require.NoError(t, err)
	assert.Len(t, moved, 1)

	left, err := m.ListVaults(testCompartment)
	require.NoError(t, err)
	assert.Empty(t, left)

	assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(m.ChangeVaultCompartment(v.ID, "")))
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(m.ChangeVaultCompartment("ocid1.vault.oc1.iad.x", otherCompart)))
}
