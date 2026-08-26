package secretsmanager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPutSecretValueStagedPending verifies a staged put with [AWSPENDING] does
// not move AWSCURRENT.
func TestPutSecretValueStagedPending(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	createTestSecret(t, m, "s", "v1")

	pending, err := m.PutSecretValueStaged(ctx, "s", []byte("pending"), "", []string{"AWSPENDING"})
	require.NoError(t, err)
	assert.False(t, pending.Current, "AWSPENDING version must not be AWSCURRENT")

	stages, err := m.SecretVersionStages(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, []string{"AWSPENDING"}, stages[pending.VersionID])

	// Default read still returns the original AWSCURRENT value.
	cur, err := m.GetSecretValueStage(ctx, "s", "", "")
	require.NoError(t, err)
	assert.Equal(t, "v1", string(cur.Value))
}

// TestClientRequestTokenIdempotency verifies same-token+same-content is a no-op
// and different content is a conflict.
func TestClientRequestTokenIdempotency(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	createTestSecret(t, m, "s", "v1")

	const token = "22222222-2222-2222-2222-222222222222"

	first, err := m.PutSecretValueStaged(ctx, "s", []byte("v2"), token, nil)
	require.NoError(t, err)
	assert.Equal(t, token, first.VersionID)

	second, err := m.PutSecretValueStaged(ctx, "s", []byte("v2"), token, nil)
	require.NoError(t, err)
	assert.Equal(t, token, second.VersionID)

	versions, err := m.ListSecretVersions(ctx, "s")
	require.NoError(t, err)
	assert.Len(t, versions, 2, "idempotent put must not add a version")

	_, err = m.PutSecretValueStaged(ctx, "s", []byte("different"), token, nil)
	require.Error(t, err, "same token + different content must fail")
}

// TestUpdateSecretVersionStagePromotes verifies moving AWSCURRENT demotes the
// prior current to AWSPREVIOUS.
func TestUpdateSecretVersionStagePromotes(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	created := createTestSecret(t, m, "s", "current")
	_ = created

	cur, err := m.GetSecretValueStage(ctx, "s", "", "AWSCURRENT")
	require.NoError(t, err)
	oldCurrentID := cur.VersionID

	pending, err := m.PutSecretValueStaged(ctx, "s", []byte("pending"), "", []string{"AWSPENDING"})
	require.NoError(t, err)

	_, err = m.UpdateSecretVersionStage(ctx, "s", "AWSCURRENT", oldCurrentID, pending.VersionID)
	require.NoError(t, err)

	// The promoted version keeps its AWSPENDING label (real rotation's
	// finishSecret only moves AWSCURRENT; it does not strip AWSPENDING), so it
	// now carries both, sorted.
	stages, err := m.SecretVersionStages(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, []string{"AWSCURRENT", "AWSPENDING"}, stages[pending.VersionID])
	assert.Equal(t, []string{"AWSPREVIOUS"}, stages[oldCurrentID])

	got, err := m.GetSecretValueStage(ctx, "s", "", "")
	require.NoError(t, err)
	assert.Equal(t, "pending", string(got.Value))
}

// TestUpdateSecretVersionStageValidation verifies argument validation.
func TestUpdateSecretVersionStageValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	createTestSecret(t, m, "s", "v1")

	_, err := m.UpdateSecretVersionStage(ctx, "s", "", "", "someversion")
	require.Error(t, err, "empty VersionStage must fail")

	_, err = m.UpdateSecretVersionStage(ctx, "s", "AWSPENDING", "", "")
	require.Error(t, err, "neither move nor remove must fail")

	_, err = m.UpdateSecretVersionStage(ctx, "s", "AWSPENDING", "", "nonexistent")
	require.Error(t, err, "unknown MoveToVersionId must fail")
}

// TestUpdateSecretReturnsVersionID verifies UpdateSecret surfaces the created
// version id on a value change and empty when only the description changes.
func TestUpdateSecretReturnsVersionID(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	createTestSecret(t, m, "s", "v1")

	_, verID, err := m.UpdateSecret(ctx, "s", "", []byte("v2"))
	require.NoError(t, err)
	assert.NotEmpty(t, verID)

	_, verID2, err := m.UpdateSecret(ctx, "s", "new-desc", nil)
	require.NoError(t, err)
	assert.Empty(t, verID2, "no value change must not create a version")
}

// TestSnapshotRestoreStages verifies staging labels survive snapshot/restore.
func TestSnapshotRestoreStages(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	createTestSecret(t, m, "s", "v1")
	_, err := m.PutSecretValueStaged(ctx, "s", []byte("v2"), "", nil)
	require.NoError(t, err)

	data, err := m.Snapshot(ctx, true)
	require.NoError(t, err)

	restored := newTestMock()
	require.NoError(t, restored.Restore(ctx, data))

	before, err := m.SecretVersionStages(ctx, "s")
	require.NoError(t, err)

	after, err := restored.SecretVersionStages(ctx, "s")
	require.NoError(t, err)

	assert.Equal(t, before, after)

	cur, err := restored.GetSecretValueStage(ctx, "s", "", "")
	require.NoError(t, err)
	assert.Equal(t, "v2", string(cur.Value))
}
