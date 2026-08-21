package vault

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// addVersion writes a new version through the OCI-shaped update path.
func addVersion(t *testing.T, m *Mock, secretID, value, name, stage string) {
	t.Helper()

	_, err := m.UpdateOCISecret(secretID, SecretUpdate{
		Content:      []byte(value),
		ContentName:  name,
		Stage:        stage,
		ContentGiven: true,
	})
	require.NoError(t, err)
}

// stagesOf indexes a secret's version stages by version number.
func stagesOf(t *testing.T, m *Mock, secretID string) map[int64][]string {
	t.Helper()

	versions, err := m.ListOCISecretVersions(secretID)
	require.NoError(t, err)

	out := make(map[int64][]string, len(versions))
	for _, v := range versions {
		out[v.VersionNumber] = v.Stages
	}

	return out
}

func TestVersionStagesSlideAsVersionsAreWritten(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "staged", "one")

	assert.Equal(t, map[int64][]string{1: {StageCurrent, StageLatest}}, stagesOf(t, m, s.ID))

	addVersion(t, m, s.ID, "two", "", StageCurrent)
	assert.Equal(t, map[int64][]string{
		1: {StagePrevious},
		2: {StageCurrent, StageLatest},
	}, stagesOf(t, m, s.ID))

	addVersion(t, m, s.ID, "three", "", StageCurrent)
	assert.Equal(t, map[int64][]string{
		1: {StageDeprecated},
		2: {StagePrevious},
		3: {StageCurrent, StageLatest},
	}, stagesOf(t, m, s.ID))

	got, err := m.GetOCISecret(s.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), got.CurrentVersionNumber)
}

func TestPendingVersionIsStagedButNotCurrent(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "rotating", "live")

	addVersion(t, m, s.ID, "staged", "", StagePending)
	assert.Equal(t, map[int64][]string{
		1: {StageCurrent},
		2: {StagePending, StageLatest},
	}, stagesOf(t, m, s.ID))

	// The CURRENT read still returns the live value.
	bundle, err := m.GetSecretBundle(s.ID, BundleSelector{})
	require.NoError(t, err)
	assert.Equal(t, []byte("live"), bundle.Content)

	pending, err := m.GetSecretBundle(s.ID, BundleSelector{Stage: StagePending})
	require.NoError(t, err)
	assert.Equal(t, []byte("staged"), pending.Content)

	// A second PENDING version deprecates the first: OCI holds only one.
	addVersion(t, m, s.ID, "restaged", "", StagePending)
	assert.Equal(t, map[int64][]string{
		1: {StageCurrent},
		2: {StageDeprecated},
		3: {StagePending, StageLatest},
	}, stagesOf(t, m, s.ID))
}

func TestPromotingAPendingVersionFinishesRotation(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "promotable", "live")

	addVersion(t, m, s.ID, "staged", "", StagePending)

	two := int64(2)

	got, err := m.UpdateOCISecret(s.ID, SecretUpdate{CurrentVersionNumber: &two})
	require.NoError(t, err)
	assert.Equal(t, int64(2), got.CurrentVersionNumber)

	assert.Equal(t, map[int64][]string{
		1: {StagePrevious},
		2: {StageCurrent, StageLatest},
	}, stagesOf(t, m, s.ID))

	bundle, err := m.GetSecretBundle(s.ID, BundleSelector{})
	require.NoError(t, err)
	assert.Equal(t, []byte("staged"), bundle.Content)

	// Promoting the version that is already current is a no-op.
	_, err = m.UpdateOCISecret(s.ID, SecretUpdate{CurrentVersionNumber: &two})
	require.NoError(t, err)

	missing := int64(99)
	_, err = m.UpdateOCISecret(s.ID, SecretUpdate{CurrentVersionNumber: &missing})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestNewVersionStageIsRestricted(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "restricted", "a")

	for _, stage := range []string{StagePrevious, StageDeprecated, StageLatest, "NONSENSE"} {
		_, err := m.UpdateOCISecret(s.ID, SecretUpdate{Content: []byte("b"), Stage: stage, ContentGiven: true})
		require.Error(t, err, stage)
		assert.Equal(t, cerrors.InvalidArgument, cerrors.GetCode(err), stage)
	}
}

func TestVersionNamesAreUniquePerSecret(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "named-versions", "a")

	addVersion(t, m, s.ID, "b", "release-1", StageCurrent)

	_, err := m.UpdateOCISecret(s.ID, SecretUpdate{
		Content:      []byte("c"),
		ContentName:  "release-1",
		ContentGiven: true,
	})
	assert.Equal(t, cerrors.AlreadyExists, cerrors.GetCode(err))
}

func TestBundleSelectors(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "selectable", "one")
	addVersion(t, m, s.ID, "two", "second", StageCurrent)

	two := int64(2)
	missing := int64(9)

	tests := []struct {
		name    string
		sel     BundleSelector
		expect  string
		errCode cerrors.Code
	}{
		{name: "default is CURRENT", expect: "two"},
		{name: "by number", sel: BundleSelector{VersionNumber: &two}, expect: "two"},
		{name: "by name", sel: BundleSelector{VersionName: "second"}, expect: "two"},
		{name: "by stage", sel: BundleSelector{Stage: StagePrevious}, expect: "one"},
		{name: "unknown number", sel: BundleSelector{VersionNumber: &missing}, errCode: cerrors.NotFound},
		{name: "unknown name", sel: BundleSelector{VersionName: "nope"}, errCode: cerrors.NotFound},
		{name: "empty stage", sel: BundleSelector{Stage: StagePending}, errCode: cerrors.NotFound},
		{name: "invalid stage", sel: BundleSelector{Stage: "SOON"}, errCode: cerrors.InvalidArgument},
		{
			name:    "two selectors at once",
			sel:     BundleSelector{VersionNumber: &two, Stage: StageCurrent},
			errCode: cerrors.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := m.GetSecretBundle(s.ID, tc.sel)
			if tc.errCode != cerrors.OK {
				require.Error(t, err)
				assert.Equal(t, tc.errCode, cerrors.GetCode(err))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expect, string(got.Content))
			assert.Equal(t, s.ID, got.SecretID)
		})
	}
}

func TestGetSecretBundleByName(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "by-name", "value")

	got, err := m.GetSecretBundleByName(s.VaultID, "by-name", BundleSelector{})
	require.NoError(t, err)
	assert.Equal(t, "value", string(got.Content))

	_, err = m.GetSecretBundleByName(s.VaultID, "absent", BundleSelector{})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	_, err = m.GetSecretBundle("ocid1.vaultsecret.oc1.iad.x", BundleSelector{})
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestListSecretBundleVersionsMatchesTheManagementListing(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "listable", "a")
	addVersion(t, m, s.ID, "b", "", StageCurrent)

	bundles, err := m.ListSecretBundleVersions(s.ID)
	require.NoError(t, err)

	management, err := m.ListOCISecretVersions(s.ID)
	require.NoError(t, err)

	assert.Equal(t, management, bundles)
	require.Len(t, bundles, 2)
	assert.Equal(t, int64(1), bundles[0].VersionNumber)

	_, err = m.ListOCISecretVersions("ocid1.vaultsecret.oc1.iad.x")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}

func TestSecretVersionScheduledDeletionAndCancellation(t *testing.T) {
	m := newTestMock()
	s := newSecret(t, m, testCompartment, "versioned", "one")
	addVersion(t, m, s.ID, "two", "", StageCurrent)

	// The CURRENT version cannot be scheduled: it would leave the secret unreadable.
	_, err := m.ScheduleSecretVersionDeletion(s.ID, 2, "")
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	scheduled, err := m.ScheduleSecretVersionDeletion(s.ID, 1, "")
	require.NoError(t, err)
	assert.Equal(t, "2026-01-31T00:00:00Z", scheduled.TimeOfDeletion)

	_, err = m.ScheduleSecretVersionDeletion(s.ID, 1, "")
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	// A version pending deletion cannot be promoted back to CURRENT.
	one := int64(1)
	_, err = m.UpdateOCISecret(s.ID, SecretUpdate{CurrentVersionNumber: &one})
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	restored, err := m.CancelSecretVersionDeletion(s.ID, 1)
	require.NoError(t, err)
	assert.Empty(t, restored.TimeOfDeletion)

	_, err = m.CancelSecretVersionDeletion(s.ID, 1)
	assert.Equal(t, cerrors.FailedPrecondition, cerrors.GetCode(err))

	_, err = m.ScheduleSecretVersionDeletion(s.ID, 99, "")
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))

	got, err := m.GetOCISecretVersion(s.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.VersionNumber)

	_, err = m.GetOCISecretVersion(s.ID, 99)
	assert.Equal(t, cerrors.NotFound, cerrors.GetCode(err))
}
