package secretsmanager

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResourcePolicyRoundTrip verifies Put stores the policy, Get returns it,
// and Delete clears it (Get then returns empty, not an error).
func TestResourcePolicyRoundTrip(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	createTestSecret(t, m, "s", "v1")

	const policy = `{"Version":"2012-10-17","Statement":[]}`

	_, err := m.PutResourcePolicy(ctx, "s", policy)
	require.NoError(t, err)

	_, got, err := m.GetResourcePolicy(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, policy, got)

	_, err = m.DeleteResourcePolicy(ctx, "s")
	require.NoError(t, err)

	_, cleared, err := m.GetResourcePolicy(ctx, "s")
	require.NoError(t, err)
	assert.Empty(t, cleared)
}

// TestResourcePolicyMissingSecret verifies a missing secret is a NotFound, not a
// silent success.
func TestResourcePolicyMissingSecret(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.PutResourcePolicy(ctx, "nope", "{}")
	require.Error(t, err)

	_, _, err = m.GetResourcePolicy(ctx, "nope")
	require.Error(t, err)
}

// TestResourcePolicySurvivesSnapshot verifies the resource policy is preserved
// across snapshot/restore.
func TestResourcePolicySurvivesSnapshot(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	createTestSecret(t, m, "s", "v1")

	const policy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow"}]}`

	_, err := m.PutResourcePolicy(ctx, "s", policy)
	require.NoError(t, err)

	data, err := m.Snapshot(ctx, true)
	require.NoError(t, err)

	restored := newTestMock()
	require.NoError(t, restored.Restore(ctx, data))

	_, got, err := restored.GetResourcePolicy(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, policy, got)
}
