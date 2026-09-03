package rds

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

var _ rdsdriver.ClusterRoles = (*Mock)(nil)

// clusterRoleStatusActive is the association state a freshly added role reports.
// Real Aurora transitions PENDING -> ACTIVE; the emulator settles immediately.
const clusterRoleStatusActive = "ACTIVE"

// AddRoleToDBCluster associates an IAM role with an Aurora DB cluster. Roles are
// keyed by RoleArn: re-adding the same ARN is rejected, matching real RDS
// (DBClusterRoleAlreadyExists). The association is recorded on the cluster's
// AssociatedRoles under a single write-locked span with copy-on-write of the
// slice so a concurrently-described cluster never sees a mutated store value.
func (m *Mock) AddRoleToDBCluster(_ context.Context, clusterID, roleARN, featureName string) error {
	if roleARN == "" {
		return cerrors.New(cerrors.InvalidArgument, "RoleArn is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(clusterID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", clusterID)
	}

	for i := range cluster.AssociatedRoles {
		if cluster.AssociatedRoles[i].RoleARN == roleARN {
			return cerrors.Newf(cerrors.AlreadyExists,
				"IAM role %q is already associated with the cluster", roleARN)
		}
	}

	roles := cloneSlice(cluster.AssociatedRoles)
	roles = append(roles, rdsdriver.DBClusterRole{
		RoleARN:     roleARN,
		FeatureName: featureName,
		Status:      clusterRoleStatusActive,
	})
	cluster.AssociatedRoles = roles

	m.clusters.Set(clusterID, cluster)

	return nil
}

// RemoveRoleFromDBCluster disassociates an IAM role from an Aurora DB cluster.
// Removing a role that is not associated is rejected (DBClusterRoleNotFound).
// The remaining roles are rebuilt into a fresh slice so a slice previously
// handed out by DescribeClusters is never clobbered underneath its caller.
func (m *Mock) RemoveRoleFromDBCluster(_ context.Context, clusterID, roleARN, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters.Get(clusterID)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "DB cluster %q not found", clusterID)
	}

	kept := make([]rdsdriver.DBClusterRole, 0, len(cluster.AssociatedRoles))

	for i := range cluster.AssociatedRoles {
		if cluster.AssociatedRoles[i].RoleARN != roleARN {
			kept = append(kept, cluster.AssociatedRoles[i])
		}
	}

	if len(kept) == len(cluster.AssociatedRoles) {
		return cerrors.Newf(cerrors.NotFound,
			"IAM role %q is not associated with the cluster", roleARN)
	}

	cluster.AssociatedRoles = kept
	m.clusters.Set(clusterID, cluster)

	return nil
}
