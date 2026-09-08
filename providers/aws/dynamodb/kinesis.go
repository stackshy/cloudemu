package dynamodb

import (
	"context"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

var _ driver.KinesisStreamer = (*Mock)(nil)

// EnableKinesisStreamingDestination attaches streamArn to the table (or
// re-activates an existing destination) and lands it ACTIVE immediately.
func (m *Mock) EnableKinesisStreamingDestination(_ context.Context,
	table, streamArn, precision string) (driver.KinesisDestination, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	td, exists := m.tables[table]
	if !exists {
		return driver.KinesisDestination{}, cerrors.Newf(cerrors.NotFound, "Table not found: %s", table)
	}

	if td.kinesisDests == nil {
		td.kinesisDests = make(map[string]driver.KinesisDestination)
	}

	dest := driver.KinesisDestination{StreamArn: streamArn, Status: driver.KinesisStatusActive, Precision: precision}
	td.kinesisDests[streamArn] = dest

	return dest, nil
}

// DisableKinesisStreamingDestination marks the table's streamArn destination
// DISABLED.
func (m *Mock) DisableKinesisStreamingDestination(_ context.Context,
	table, streamArn string) (driver.KinesisDestination, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	dest, err := m.kinesisDest(table, streamArn)
	if err != nil {
		return driver.KinesisDestination{}, err
	}

	dest.Status = driver.KinesisStatusDisabled
	m.tables[table].kinesisDests[streamArn] = dest

	return dest, nil
}

// UpdateKinesisStreamingDestination changes the precision of the table's
// streamArn destination.
func (m *Mock) UpdateKinesisStreamingDestination(_ context.Context,
	table, streamArn, precision string) (driver.KinesisDestination, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	dest, err := m.kinesisDest(table, streamArn)
	if err != nil {
		return driver.KinesisDestination{}, err
	}

	dest.Precision = precision
	m.tables[table].kinesisDests[streamArn] = dest

	return dest, nil
}

// DescribeKinesisStreamingDestination returns every destination attached to the
// table, ordered by StreamArn.
func (m *Mock) DescribeKinesisStreamingDestination(_ context.Context, table string) ([]driver.KinesisDestination, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	td, exists := m.tables[table]
	if !exists {
		return nil, cerrors.Newf(cerrors.NotFound, "Table not found: %s", table)
	}

	out := make([]driver.KinesisDestination, 0, len(td.kinesisDests))
	for _, d := range td.kinesisDests {
		out = append(out, d)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].StreamArn < out[j].StreamArn })

	return out, nil
}

// kinesisDest returns a copy of the table's streamArn destination under the
// caller-held lock, or a not-found error. A missing table is TableNotFound; a
// missing destination is also reported as not-found so callers map it to the
// same ResourceNotFoundException DynamoDB returns.
func (m *Mock) kinesisDest(table, streamArn string) (driver.KinesisDestination, error) {
	td, exists := m.tables[table]
	if !exists {
		return driver.KinesisDestination{}, cerrors.Newf(cerrors.NotFound, "Table not found: %s", table)
	}

	dest, ok := td.kinesisDests[streamArn]
	if !ok {
		return driver.KinesisDestination{}, cerrors.Newf(cerrors.NotFound,
			"Kinesis streaming destination not found for table %s: %s", table, streamArn)
	}

	return dest, nil
}
