package secretsmanager

import (
	"context"
	"encoding/json"
	"sync"
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

func TestReplicaRegionNameValidated(t *testing.T) {
	for _, region := range []string{"not-a-region", "eu-west", "EU-WEST-1", "euwest1", "eu-west-1 "} {
		t.Run(region, func(t *testing.T) {
			m := newTestMock()
			ctx := context.Background()

			_, err := m.CreateSecret(ctx, driver.SecretConfig{
				Name: "c", AddReplicaRegions: []driver.SecretReplicaRegion{{Region: region}},
			}, []byte("v"))
			require.Error(t, err)
			assert.Equal(t, errors.InvalidArgument, errors.GetCode(err))

			_, getErr := m.GetSecret(ctx, "c")
			assert.True(t, errors.IsNotFound(getErr), "rejected create must not store the secret")

			createTestSecret(t, m, "s", "v")

			_, err = m.ReplicateSecretToRegions(ctx, "s", []driver.SecretReplicaRegion{{Region: region}}, false)
			assert.Equal(t, errors.InvalidArgument, errors.GetCode(err))

			meta, err := m.SecretMetadata(ctx, "s")
			require.NoError(t, err)
			assert.Empty(t, meta.ReplicationStatus)
		})
	}
}

func TestReplicationStampsLastChangedDate(t *testing.T) {
	m, fc := newClockedMock()
	ctx := context.Background()
	createTestSecret(t, m, "s", "v")

	created, err := m.GetSecret(ctx, "s")
	require.NoError(t, err)

	fc.Advance(time.Hour)

	info, err := m.ReplicateSecretToRegions(ctx, "s", []driver.SecretReplicaRegion{{Region: "eu-west-3"}}, false)
	require.NoError(t, err)
	assert.Equal(t, fc.Now().UTC().Format(time.RFC3339), info.UpdatedAt)
	assert.NotEqual(t, created.UpdatedAt, info.UpdatedAt)

	fc.Advance(time.Hour)

	info, err = m.RemoveRegionsFromReplication(ctx, "s", []string{"eu-west-3"})
	require.NoError(t, err)
	assert.Equal(t, fc.Now().UTC().Format(time.RFC3339), info.UpdatedAt)

	got, err := m.GetSecret(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, info.UpdatedAt, got.UpdatedAt)
}

// TestDeleteAndReplicateAreAtomic races DeleteSecret against
// ReplicateSecretToRegions on the same secret. Whatever the interleaving, a
// secret must never end up both scheduled for deletion (or force-deleted) and
// replicated: exactly one of the two calls may win.
func TestDeleteAndReplicateAreAtomic(t *testing.T) {
	ctx := context.Background()

	for _, force := range []bool{false, true} {
		for i := 0; i < 500; i++ {
			m := newTestMock()
			createTestSecret(t, m, "s", "v")

			var (
				wg             sync.WaitGroup
				delErr, repErr error
				start          = make(chan struct{})
			)

			wg.Add(2)

			go func() {
				defer wg.Done()
				<-start
				_, _, delErr = m.DeleteSecretWithOptions(ctx, "s", nil, force)
			}()

			go func() {
				defer wg.Done()
				<-start
				_, repErr = m.ReplicateSecretToRegions(ctx, "s",
					[]driver.SecretReplicaRegion{{Region: "eu-west-3"}}, false)
			}()

			close(start)
			wg.Wait()

			require.False(t, delErr == nil && repErr == nil,
				"force=%v: both DeleteSecret and ReplicateSecretToRegions succeeded", force)

			if sd, ok := m.secrets.Get("s"); ok {
				sd.mu.RLock()
				bad := !sd.deletedAt.IsZero() && len(sd.info.ReplicationStatus) > 0
				sd.mu.RUnlock()
				require.False(t, bad, "secret is scheduled for deletion and replicated")
			}
		}
	}
}
