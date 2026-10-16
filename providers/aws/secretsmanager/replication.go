package secretsmanager

import (
	"context"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// Multi-Region secret replication (ReplicateSecretToRegions,
// RemoveRegionsFromReplication, StopReplicationToReplica, CreateSecret
// AddReplicaRegions).
//
// cloudemu runs one Secrets Manager mock per Region and those mocks don't
// reach one another, so replication is modeled as control-plane metadata on
// the primary secret: the replica list and per-replica status that
// DescribeSecret/CreateSecret/ReplicateSecretToRegions report. No replica
// secret materializes in the target Region's mock. This mirrors how the KMS
// mock models ReplicateKey and the DynamoDB mock models global-table replicas.
// Replication completes synchronously, so a replica is reported InSync
// immediately rather than passing through InProgress.

// Replica status values (StatusType) and the message real Secrets Manager
// reports for a replica that finished replicating.
const (
	replicaStatusInSync        = "InSync"
	replicaStatusMessageInSync = "Replication succeeded"
)

// addReplicas validates add against the existing replica list and the
// primary Region and returns a NEW slice (existing is never mutated, so
// SecretInfo copies that share it stay consistent) with the added replicas
// appended. A replica without a KmsKeyId is encrypted with
// alias/aws/secretsmanager, which is what real Secrets Manager echoes.
func addReplicas(
	existing []driver.SecretReplicationStatus, add []driver.SecretReplicaRegion, primaryRegion string,
) ([]driver.SecretReplicationStatus, error) {
	if len(add) == 0 {
		return nil, errors.New(errors.InvalidArgument, "AddReplicaRegions must contain at least one Region")
	}

	out := make([]driver.SecretReplicationStatus, len(existing), len(existing)+len(add))
	copy(out, existing)

	for _, r := range add {
		region := strings.TrimSpace(r.Region)
		if region == "" {
			return nil, errors.New(errors.InvalidArgument, "a replica Region is required")
		}

		if region == primaryRegion {
			return nil, errors.Newf(errors.InvalidArgument,
				"Invalid replica Region %s: it is the primary Region of the secret", region)
		}

		if replicaIndex(out, region) >= 0 {
			return nil, errors.Newf(errors.InvalidArgument,
				"The secret is already replicated to Region %s", region)
		}

		kmsKey := r.KMSKeyID
		if kmsKey == "" {
			kmsKey = defaultKMSKey
		}

		out = append(out, driver.SecretReplicationStatus{
			Region:        region,
			KMSKeyID:      kmsKey,
			Status:        replicaStatusInSync,
			StatusMessage: replicaStatusMessageInSync,
		})
	}

	return out, nil
}

// initialReplicas builds CreateSecret's AddReplicaRegions replica list (nil
// when none were requested).
func (m *Mock) initialReplicas(add []driver.SecretReplicaRegion) ([]driver.SecretReplicationStatus, error) {
	if len(add) == 0 {
		return nil, nil
	}

	return addReplicas(nil, add, m.opts.Region)
}

// replicaIndex returns the index of region in replicas, or -1.
func replicaIndex(replicas []driver.SecretReplicationStatus, region string) int {
	for i := range replicas {
		if replicas[i].Region == region {
			return i
		}
	}

	return -1
}

// primaryRegion is the secret's home Region: the stored PrimaryRegion, else
// the Region in its ARN, else this mock's Region. Callers must hold sd.mu.
func (m *Mock) primaryRegion(sd *secretData) string {
	if sd.info.PrimaryRegion != "" {
		return sd.info.PrimaryRegion
	}

	return arnRegion(sd.info.ResourceID, m.opts.Region)
}

// arnRegion returns the region field of a Secrets Manager ARN
// (arn:aws:secretsmanager:<region>:<account>:secret:<name>), or fallback when
// the ARN is malformed.
func arnRegion(arn, fallback string) string {
	const regionField, minFields = 3, 6

	parts := strings.Split(arn, ":")
	if len(parts) < minFields || parts[regionField] == "" {
		return fallback
	}

	return parts[regionField]
}

// activeSecret returns the named secret locked for writing, or the NotFound /
// marked-for-deletion error real Secrets Manager reports. On success the
// caller must unlock sd.mu.
func (m *Mock) activeSecret(name string) (*secretData, error) {
	sd, ok := m.secrets.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "secret %q not found", name)
	}

	sd.mu.Lock()

	if !sd.deletedAt.IsZero() {
		sd.mu.Unlock()

		return nil, errors.New(errors.FailedPrecondition, msgMarkedForDeletion)
	}

	return sd, nil
}

// ReplicateSecretToRegions adds replica Regions to a primary secret and returns
// the secret's metadata, whose ReplicationStatus lists every replica.
// forceOverwrite (ForceOverwriteReplicaSecret) is accepted for API parity; with
// no replica secret materialized in the target Region there is never a
// same-named secret there to overwrite.
func (m *Mock) ReplicateSecretToRegions(
	_ context.Context, name string, regions []driver.SecretReplicaRegion, _ bool,
) (*driver.SecretInfo, error) {
	sd, err := m.activeSecret(name)
	if err != nil {
		return nil, err
	}
	defer sd.mu.Unlock()

	replicas, err := addReplicas(sd.info.ReplicationStatus, regions, m.primaryRegion(sd))
	if err != nil {
		return nil, err
	}

	sd.info.ReplicationStatus = replicas
	sd.info.PrimaryRegion = m.primaryRegion(sd)

	info := sd.info

	return &info, nil
}

// RemoveRegionsFromReplication deletes the named replicas from a primary
// secret and returns the secret's metadata with the remaining replicas.
func (m *Mock) RemoveRegionsFromReplication(
	_ context.Context, name string, regions []string,
) (*driver.SecretInfo, error) {
	if len(regions) == 0 {
		return nil, errors.New(errors.InvalidArgument, "RemoveReplicaRegions must contain at least one Region")
	}

	sd, err := m.activeSecret(name)
	if err != nil {
		return nil, err
	}
	defer sd.mu.Unlock()

	remaining := make([]driver.SecretReplicationStatus, len(sd.info.ReplicationStatus))
	copy(remaining, sd.info.ReplicationStatus)

	for _, region := range regions {
		i := replicaIndex(remaining, region)
		if i < 0 {
			return nil, errors.Newf(errors.InvalidArgument,
				"The secret is not replicated to Region %s", region)
		}

		remaining = append(remaining[:i], remaining[i+1:]...)
	}

	if len(remaining) == 0 {
		remaining = nil
	}

	sd.info.ReplicationStatus = remaining

	info := sd.info

	return &info, nil
}

// StopReplicationToReplica promotes a replica secret to a standalone primary.
// It must be called in the replica's Region on the replica secret. Every
// secret this mock holds is a primary in this Region (replicas are not
// materialized; see the package note above), so the call is rejected with
// InvalidRequestException for an existing secret and ResourceNotFoundException
// for a missing one.
func (m *Mock) StopReplicationToReplica(_ context.Context, name string) (*driver.SecretInfo, error) {
	sd, err := m.activeSecret(name)
	if err != nil {
		return nil, err
	}
	defer sd.mu.Unlock()

	return nil, errors.Newf(errors.FailedPrecondition,
		"Secret %s is not a replica secret in Region %s, so replication can't be stopped. "+
			"Call StopReplicationToReplica in the Region of the replica.", sd.info.Name, m.primaryRegion(sd))
}

// retrieveVersion decrypts a copy of v for a GetSecretValue-style read and, on
// success, stamps the secret's LastAccessedDate. AWS reports LastAccessedDate
// at date resolution (truncated to midnight UTC). Callers must hold sd.mu for
// writing.
func (m *Mock) retrieveVersion(
	ctx context.Context, sd *secretData, v *driver.SecretVersion,
) (*driver.SecretVersion, error) {
	out, err := m.decryptVersion(ctx, copyVersion(v))
	if err != nil {
		return nil, err
	}

	day := m.opts.Clock.Now().UTC().Truncate(24 * time.Hour) //nolint:mnd // one day
	sd.info.LastAccessedDate = day.Format(time.RFC3339)

	return out, nil
}
