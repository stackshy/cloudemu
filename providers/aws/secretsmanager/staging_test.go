package secretsmanager

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
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

	// Promoting AWSCURRENT onto the pending version auto-removes its AWSPENDING
	// label (as the real service does at finishSecret), so it carries AWSCURRENT
	// exactly — AWSPENDING must not ride forward onto the current version.
	stages, err := m.SecretVersionStages(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, []string{"AWSCURRENT"}, stages[pending.VersionID])
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

// TestRotateSecretConfiguresAndRotates verifies RotateSecret stores the
// lambda ARN/rules, enables rotation, and (RotateImmediately=true) advances
// the version and stamps LastRotatedDate/NextRotationDate.
func TestRotateSecretConfiguresAndRotates(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	createTestSecret(t, m, "s", "v1")

	rules := driver.SecretRotationRules{AutomaticallyAfterDays: 30}

	ver, err := m.RotateSecret(ctx, "s", "arn:aws:lambda:us-east-1:123456789012:function:rotate", rules, true)
	require.NoError(t, err)
	assert.NotEmpty(t, ver.VersionID)

	details, err := m.SecretRotationDetails(ctx, "s")
	require.NoError(t, err)
	assert.True(t, details.Enabled)
	assert.Equal(t, "arn:aws:lambda:us-east-1:123456789012:function:rotate", details.LambdaARN)
	assert.Equal(t, rules, details.Rules)
	assert.NotEmpty(t, details.LastRotatedDate)
	assert.NotEmpty(t, details.NextRotationDate)

	cur, err := m.GetSecretValueStage(ctx, "s", "", "")
	require.NoError(t, err)
	assert.Equal(t, ver.VersionID, cur.VersionID)
}

// TestRotateSecretImmediateFalseConfiguresOnly verifies RotateImmediately=false
// enables rotation and stores the config without advancing the version.
func TestRotateSecretImmediateFalseConfiguresOnly(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	createTestSecret(t, m, "s", "v1")

	cur, err := m.GetSecretValueStage(ctx, "s", "", "")
	require.NoError(t, err)

	ver, err := m.RotateSecret(ctx, "s", "arn:aws:lambda:us-east-1:123456789012:function:rotate",
		driver.SecretRotationRules{}, false)
	require.NoError(t, err)
	assert.Equal(t, cur.VersionID, ver.VersionID, "RotateImmediately=false must not advance the version")

	details, err := m.SecretRotationDetails(ctx, "s")
	require.NoError(t, err)
	assert.True(t, details.Enabled)
	assert.Empty(t, details.LastRotatedDate, "no rotation ran, so LastRotatedDate must stay unset")
}

// TestRotateSecretReusesConfiguredLambda verifies a second RotateSecret call
// with no lambda ARN keeps the previously configured one.
func TestRotateSecretReusesConfiguredLambda(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	createTestSecret(t, m, "s", "v1")

	_, err := m.RotateSecret(ctx, "s", "arn:aws:lambda:us-east-1:123456789012:function:rotate",
		driver.SecretRotationRules{}, true)
	require.NoError(t, err)

	_, err = m.RotateSecret(ctx, "s", "", driver.SecretRotationRules{}, true)
	require.NoError(t, err)

	details, err := m.SecretRotationDetails(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, "arn:aws:lambda:us-east-1:123456789012:function:rotate", details.LambdaARN)
}

// TestCancelRotateSecret verifies CancelRotateSecret disables rotation while
// keeping the configured lambda ARN in place.
func TestCancelRotateSecret(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	createTestSecret(t, m, "s", "v1")

	_, err := m.RotateSecret(ctx, "s", "arn:aws:lambda:us-east-1:123456789012:function:rotate",
		driver.SecretRotationRules{}, true)
	require.NoError(t, err)

	cur, err := m.GetSecretValueStage(ctx, "s", "", "")
	require.NoError(t, err)

	info, versionID, err := m.CancelRotateSecret(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, "s", info.Name)
	assert.Equal(t, cur.VersionID, versionID)

	details, err := m.SecretRotationDetails(ctx, "s")
	require.NoError(t, err)
	assert.False(t, details.Enabled)
	assert.Equal(t, "arn:aws:lambda:us-east-1:123456789012:function:rotate", details.LambdaARN,
		"cancel must not clear the configured lambda ARN")
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

// TestSnapshotRestoreRotationConfig verifies rotation configuration survives
// snapshot/restore.
func TestSnapshotRestoreRotationConfig(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	createTestSecret(t, m, "s", "v1")

	rules := driver.SecretRotationRules{AutomaticallyAfterDays: 14}

	_, err := m.RotateSecret(ctx, "s", "arn:aws:lambda:us-east-1:123456789012:function:rotate", rules, true)
	require.NoError(t, err)

	data, err := m.Snapshot(ctx, true)
	require.NoError(t, err)

	restored := newTestMock()
	require.NoError(t, restored.Restore(ctx, data))

	before, err := m.SecretRotationDetails(ctx, "s")
	require.NoError(t, err)

	after, err := restored.SecretRotationDetails(ctx, "s")
	require.NoError(t, err)

	assert.Equal(t, before, after)
}
