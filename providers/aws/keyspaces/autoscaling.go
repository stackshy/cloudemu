package keyspaces

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

// GetTableAutoScalingSettings returns the table with its stored auto-scaling
// specification. It errors if the table is not in provisioned throughput mode
// (auto scaling applies only to PROVISIONED tables), matching AWS.
func (m *Mock) GetTableAutoScalingSettings(_ context.Context, keyspace, table string) (*ksdriver.Table, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.getTableLocked(keyspace, table)
	if err != nil {
		return nil, err
	}

	if t.CapacitySpecification.ThroughputMode != ksdriver.ThroughputProvisioned {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"auto scaling settings are only available for PROVISIONED tables; %q is %s",
			table, t.CapacitySpecification.ThroughputMode)
	}

	out := cloneTable(&t)

	return &out, nil
}
