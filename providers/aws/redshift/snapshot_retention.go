package redshift

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdbdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

const (
	// manualRetentionIndefinite keeps a manual snapshot until it is deleted. It
	// is the default when CreateClusterSnapshot omits the period.
	manualRetentionIndefinite = -1
	// maxManualRetentionDays is the longest manual snapshot retention AWS allows.
	maxManualRetentionDays = 3653
)

// validateManualRetention accepts -1 or 1 to 3653 days, like real Redshift.
func validateManualRetention(days int) error {
	if days == manualRetentionIndefinite || (days >= 1 && days <= maxManualRetentionDays) {
		return nil
	}

	return cerrors.Newf(cerrors.InvalidArgument,
		"invalid manual snapshot retention period %d: use -1 or a value from 1 to %d", days, maxManualRetentionDays)
}

// readSnapshotLocked returns the copy a read hands out: live tags from the
// store, and -1 for rows saved before retention was stored. The caller holds
// m.mu.
//
//nolint:gocritic // takes a value on purpose: it returns an independent copy.
func (m *Mock) readSnapshotLocked(snap rdbdriver.ClusterSnapshot) rdbdriver.ClusterSnapshot {
	snap.Tags = m.tagsLocked(snap.ARN)

	if snap.ManualSnapshotRetentionPeriod == 0 {
		snap.ManualSnapshotRetentionPeriod = manualRetentionIndefinite
	}

	return snap
}

// ModifyClusterSnapshot changes a manual snapshot's retention period. A nil
// period leaves it unchanged. A period that has already passed since the
// snapshot was taken is rejected unless force is set, as in real Redshift.
func (m *Mock) ModifyClusterSnapshot(
	_ context.Context, id string, retention *int, force bool,
) (*rdbdriver.ClusterSnapshot, error) {
	if retention != nil {
		if err := validateManualRetention(*retention); err != nil {
			return nil, err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snap, ok := m.clusterSnapshots.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "Redshift cluster snapshot %q not found", id)
	}

	if retention != nil {
		days := *retention
		if !force && days != manualRetentionIndefinite && snap.CreatedAt.AddDate(0, 0, days).Before(m.opts.Clock.Now()) {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"manual snapshot retention period %d has already passed for snapshot %q; set Force to apply it", days, id)
		}

		snap.ManualSnapshotRetentionPeriod = days
		m.clusterSnapshots.Set(id, snap)
	}

	out := m.readSnapshotLocked(snap)

	return &out, nil
}
