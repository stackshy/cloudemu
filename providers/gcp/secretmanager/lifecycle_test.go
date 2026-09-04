package secretmanager

import (
	"context"
	"errors"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionLifecycleStateMachine(t *testing.T) {
	m := newTestMock()
	info := createTestSecret(t, m, "state-secret", "v1")
	ctx := context.Background()

	versions, err := m.ListSecretVersions(ctx, info.Name)
	require.NoError(t, err)
	require.Len(t, versions, 1)

	v1 := versions[0].VersionID
	assert.Equal(t, driver.VersionEnabled, versions[0].State)

	dis, err := m.DisableSecretVersion(ctx, info.Name, v1, "")
	require.NoError(t, err)
	assert.Equal(t, driver.VersionDisabled, dis.State)
	assert.Nil(t, dis.Value, "lifecycle response carries metadata only")

	_, err = m.GetSecretValue(ctx, info.Name, v1)
	require.NoError(t, err, "GetSecretValue does not itself gate on state")

	en, err := m.EnableSecretVersion(ctx, info.Name, v1, "")
	require.NoError(t, err)
	assert.Equal(t, driver.VersionEnabled, en.State)

	des, err := m.DestroySecretVersion(ctx, info.Name, v1, "")
	require.NoError(t, err)
	assert.Equal(t, driver.VersionDestroyed, des.State)
	assert.NotEmpty(t, des.DestroyTime)

	// The payload is wiped in the store, not just the response.
	got, err := m.GetSecretValue(ctx, info.Name, v1)
	require.NoError(t, err)
	assert.Empty(t, got.Value)
}

func TestVersionLifecycleDestroyedIsTerminal(t *testing.T) {
	m := newTestMock()
	info := createTestSecret(t, m, "terminal-secret", "v1")
	ctx := context.Background()

	versions, err := m.ListSecretVersions(ctx, info.Name)
	require.NoError(t, err)
	v1 := versions[0].VersionID

	_, err = m.DestroySecretVersion(ctx, info.Name, v1, "")
	require.NoError(t, err)

	_, err = m.DestroySecretVersion(ctx, info.Name, v1, "")
	require.Error(t, err)
	assert.True(t, cerrors.IsFailedPrecondition(err))

	_, err = m.DisableSecretVersion(ctx, info.Name, v1, "")
	require.Error(t, err)
	assert.True(t, cerrors.IsFailedPrecondition(err))

	_, err = m.EnableSecretVersion(ctx, info.Name, v1, "")
	require.Error(t, err)
	assert.True(t, cerrors.IsFailedPrecondition(err))
}

func TestVersionLifecycleNotFound(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.DisableSecretVersion(ctx, "no-such-secret", "1", "")
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))

	createTestSecret(t, m, "vnf-secret", "v1")

	_, err = m.DisableSecretVersion(ctx, "vnf-secret", "no-such-version", "")
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
}

// TestVersionLifecycleEtagPrecondition proves enable/disable/destroy honor an
// optional etag precondition, checked atomically alongside the state
// transition: a mismatched etag is rejected without applying the transition,
// a matching etag succeeds and mints a fresh one, and an empty etag always
// skips the check (real GCP's leniency when the caller omits it).
func TestVersionLifecycleEtagPrecondition(t *testing.T) {
	m := newTestMock()
	info := createTestSecret(t, m, "etag-secret", "v1")
	ctx := context.Background()

	versions, err := m.ListSecretVersions(ctx, info.Name)
	require.NoError(t, err)
	v1 := versions[0]

	_, err = m.DisableSecretVersion(ctx, info.Name, v1.VersionID, "stale-etag")
	require.Error(t, err)

	var preErr *driver.GCPSecretPreconditionError
	require.True(t, errors.As(err, &preErr), "want a *GCPSecretPreconditionError, got %T", err)

	// The mismatched call must not have applied the transition.
	unchanged, err := m.ListSecretVersions(ctx, info.Name)
	require.NoError(t, err)
	assert.Equal(t, driver.VersionEnabled, unchanged[0].State)

	dis, err := m.DisableSecretVersion(ctx, info.Name, v1.VersionID, v1.Etag)
	require.NoError(t, err)
	assert.Equal(t, driver.VersionDisabled, dis.State)
	assert.NotEqual(t, v1.Etag, dis.Etag, "a successful transition mints a fresh etag")

	// An empty etag always succeeds, regardless of the version's current one.
	en, err := m.EnableSecretVersion(ctx, info.Name, v1.VersionID, "")
	require.NoError(t, err)
	assert.Equal(t, driver.VersionEnabled, en.State)
}

func TestPatchSecret(t *testing.T) {
	m := newTestMock()
	info := createTestSecret(t, m, "patch-secret", "v1")
	ctx := context.Background()

	updated, err := m.PatchSecret(ctx, info.Name, driver.GCPSecretPatch{
		Labels:    map[string]string{"team": "platform"},
		SetLabels: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "platform", updated.Tags["team"])
	assert.NotEqual(t, info.Etag, updated.Etag, "a successful patch mints a fresh etag")
}

func TestPatchSecretNotFound(t *testing.T) {
	m := newTestMock()

	_, err := m.PatchSecret(context.Background(), "no-such-secret", driver.GCPSecretPatch{})
	require.Error(t, err)
	assert.True(t, cerrors.IsNotFound(err))
}

// TestPatchSecretEtagPrecondition proves secrets.patch honors an optional
// etag precondition, independent of which fields the update mask names.
func TestPatchSecretEtagPrecondition(t *testing.T) {
	m := newTestMock()
	info := createTestSecret(t, m, "patch-etag-secret", "v1")
	ctx := context.Background()

	_, err := m.PatchSecret(ctx, info.Name, driver.GCPSecretPatch{
		Labels:    map[string]string{"team": "platform"},
		SetLabels: true,
		Etag:      "stale-etag",
	})
	require.Error(t, err)

	var preErr *driver.GCPSecretPreconditionError
	require.True(t, errors.As(err, &preErr), "want a *GCPSecretPreconditionError, got %T", err)

	// The mismatched call must not have applied the patch.
	unchanged, err := m.GetSecret(ctx, info.Name)
	require.NoError(t, err)
	assert.Empty(t, unchanged.Tags)

	updated, err := m.PatchSecret(ctx, info.Name, driver.GCPSecretPatch{
		Labels:    map[string]string{"team": "platform"},
		SetLabels: true,
		Etag:      info.Etag,
	})
	require.NoError(t, err)
	assert.Equal(t, "platform", updated.Tags["team"])

	// An empty etag always succeeds, regardless of the secret's current one.
	final, err := m.PatchSecret(ctx, info.Name, driver.GCPSecretPatch{
		Labels:    map[string]string{"team": "sre"},
		SetLabels: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "sre", final.Tags["team"])
}
