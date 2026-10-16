package secretsmanager

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newClockedMock() (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 15, 4, 5, 0, time.UTC))

	return New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))), fc
}

func TestCreateSecretRecordsPrimaryRegionAndReplicas(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	info, err := m.CreateSecret(ctx, driver.SecretConfig{
		Name: "rep",
		AddReplicaRegions: []driver.SecretReplicaRegion{
			{Region: "eu-west-3"},
			{Region: "us-west-2", KMSKeyID: "alias/custom"},
		},
	}, []byte("v"))
	require.NoError(t, err)

	assert.Equal(t, "us-east-1", info.PrimaryRegion)
	require.Len(t, info.ReplicationStatus, 2)
	assert.Equal(t, driver.SecretReplicationStatus{
		Region: "eu-west-3", KMSKeyID: "alias/aws/secretsmanager", Status: "InSync", StatusMessage: "Replication succeeded",
	}, info.ReplicationStatus[0])
	assert.Equal(t, "alias/custom", info.ReplicationStatus[1].KMSKeyID)

	meta, err := m.SecretMetadata(ctx, "rep")
	require.NoError(t, err)
	assert.Len(t, meta.ReplicationStatus, 2)
}

func TestCreateSecretRejectsInvalidReplicaRegions(t *testing.T) {
	tests := []struct {
		name    string
		regions []driver.SecretReplicaRegion
	}{
		{"primary region", []driver.SecretReplicaRegion{{Region: "us-east-1"}}},
		{"duplicate", []driver.SecretReplicaRegion{{Region: "eu-west-1"}, {Region: "eu-west-1"}}},
		{"empty region", []driver.SecretReplicaRegion{{Region: ""}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			_, err := m.CreateSecret(context.Background(),
				driver.SecretConfig{Name: "s", AddReplicaRegions: tc.regions}, []byte("v"))
			require.Error(t, err)
			assert.Equal(t, errors.InvalidArgument, errors.GetCode(err))

			_, getErr := m.GetSecret(context.Background(), "s")
			assert.True(t, errors.IsNotFound(getErr), "rejected create must not store the secret")
		})
	}
}

func TestReplicateAndRemoveRegions(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestSecret(t, m, "s", "v")

	info, err := m.ReplicateSecretToRegions(ctx, "s", []driver.SecretReplicaRegion{{Region: "eu-west-3"}}, false)
	require.NoError(t, err)
	require.Len(t, info.ReplicationStatus, 1)
	assert.Equal(t, "eu-west-3", info.ReplicationStatus[0].Region)

	_, err = m.ReplicateSecretToRegions(ctx, "s", []driver.SecretReplicaRegion{{Region: "eu-west-3"}}, false)
	assert.Equal(t, errors.InvalidArgument, errors.GetCode(err), "re-adding a replica Region is rejected")

	info, err = m.ReplicateSecretToRegions(ctx, "s", []driver.SecretReplicaRegion{{Region: "ap-south-1"}}, true)
	require.NoError(t, err)
	assert.Len(t, info.ReplicationStatus, 2)

	// A primary with replicas can't be deleted (with or without recovery).
	_, _, err = m.DeleteSecretWithOptions(ctx, "s", nil, false)
	assert.Equal(t, errors.FailedPrecondition, errors.GetCode(err))
	_, _, err = m.DeleteSecretWithOptions(ctx, "s", nil, true)
	assert.Equal(t, errors.FailedPrecondition, errors.GetCode(err))

	_, err = m.RemoveRegionsFromReplication(ctx, "s", []string{"us-west-1"})
	assert.Equal(t, errors.InvalidArgument, errors.GetCode(err), "removing a non-replica Region is rejected")

	info, err = m.RemoveRegionsFromReplication(ctx, "s", []string{"eu-west-3", "ap-south-1"})
	require.NoError(t, err)
	assert.Empty(t, info.ReplicationStatus)

	_, _, err = m.DeleteSecretWithOptions(ctx, "s", nil, false)
	require.NoError(t, err)

	_, err = m.ReplicateSecretToRegions(ctx, "s", []driver.SecretReplicaRegion{{Region: "eu-west-3"}}, false)
	assert.Equal(t, errors.FailedPrecondition, errors.GetCode(err))
	assert.Equal(t, msgMarkedForDeletion, errors.Message(err))
}

func TestReplicationNotFound(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.ReplicateSecretToRegions(ctx, "nope", []driver.SecretReplicaRegion{{Region: "eu-west-3"}}, false)
	assert.True(t, errors.IsNotFound(err))
	_, err = m.RemoveRegionsFromReplication(ctx, "nope", []string{"eu-west-3"})
	assert.True(t, errors.IsNotFound(err))
	_, err = m.StopReplicationToReplica(ctx, "nope")
	assert.True(t, errors.IsNotFound(err))
}

func TestStopReplicationToReplicaRejectsPrimary(t *testing.T) {
	m := newTestMock()
	createTestSecret(t, m, "s", "v")

	_, err := m.StopReplicationToReplica(context.Background(), "s")
	assert.Equal(t, errors.FailedPrecondition, errors.GetCode(err))
}

func TestGetSecretValueStampsLastAccessedDateAtDayResolution(t *testing.T) {
	m, fc := newClockedMock()
	ctx := context.Background()
	createTestSecret(t, m, "s", "v")

	info, err := m.SecretMetadata(ctx, "s")
	require.NoError(t, err)
	assert.Empty(t, info.LastAccessedDate, "never retrieved: no LastAccessedDate")

	_, err = m.GetSecretValueStage(ctx, "s", "", "")
	require.NoError(t, err)

	info, err = m.SecretMetadata(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, "2025-01-01T00:00:00Z", info.LastAccessedDate)

	fc.Advance(36 * time.Hour)

	_, err = m.GetSecretValue(ctx, "s", "")
	require.NoError(t, err)

	info, err = m.SecretMetadata(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, "2025-01-03T00:00:00Z", info.LastAccessedDate)
}

func TestListSecretsWithOptionsIncludePlannedDeletion(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestSecret(t, m, "live", "v")
	createTestSecret(t, m, "gone", "v")
	require.NoError(t, m.DeleteSecret(ctx, "gone"))

	active, err := m.ListSecretsWithOptions(ctx, false)
	require.NoError(t, err)
	assert.Len(t, active, 1)

	all, err := m.ListSecretsWithOptions(ctx, true)
	require.NoError(t, err)
	assert.Len(t, all, 2)

	portable, err := m.ListSecrets(ctx)
	require.NoError(t, err)
	assert.Len(t, portable, 1, "portable ListSecrets still hides scheduled-for-deletion secrets")
}

func TestScheduledForDeletionMessages(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	createTestSecret(t, m, "s", "v")
	require.NoError(t, m.DeleteSecret(ctx, "s"))

	_, err := m.GetSecretValue(ctx, "s", "")
	assert.Equal(t, "You can't perform this operation on the secret because it was marked for deletion.",
		errors.Message(err))

	_, err = m.CreateSecret(ctx, driver.SecretConfig{Name: "s"}, []byte("v"))
	assert.Equal(t,
		"You can't create this secret because a secret with this name is already scheduled for deletion.",
		errors.Message(err))
}

func TestReplicationSurvivesSnapshot(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateSecret(ctx, driver.SecretConfig{
		Name: "s", AddReplicaRegions: []driver.SecretReplicaRegion{{Region: "eu-west-3"}},
	}, []byte("v"))
	require.NoError(t, err)
	_, err = m.GetSecretValue(ctx, "s", "")
	require.NoError(t, err)

	snap, err := m.Snapshot(ctx, false)
	require.NoError(t, err)

	restored := newTestMock()
	require.NoError(t, restored.Restore(ctx, snap))

	info, err := restored.SecretMetadata(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", info.PrimaryRegion)
	assert.Equal(t, "2025-01-01T00:00:00Z", info.LastAccessedDate)
	require.Len(t, info.ReplicationStatus, 1)
	assert.Equal(t, "eu-west-3", info.ReplicationStatus[0].Region)
}

func TestRestoreBackfillsPrimaryRegionFromARN(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	legacy, err := json.Marshal(secretsSnapshot{Secrets: map[string]*secretSnapshot{
		"old": {Info: driver.SecretInfo{
			Name: "old", ResourceID: "arn:aws:secretsmanager:eu-central-1:123456789012:secret:old-AbCdEf",
		}},
	}})
	require.NoError(t, err)
	require.NoError(t, m.Restore(ctx, legacy))

	info, err := m.SecretMetadata(ctx, "old")
	require.NoError(t, err)
	assert.Equal(t, "eu-central-1", info.PrimaryRegion)
}
