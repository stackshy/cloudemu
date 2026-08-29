package monitor

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordAndListActivityLog verifies events are recorded newest-first and
// filtered by resource group.
func TestRecordAndListActivityLog(t *testing.T) {
	m, _ := newTestMock()

	m.RecordActivityLogEvent(&driver.ActivityLogEvent{
		OperationName: "Microsoft.Network/virtualNetworks/write", ResourceGroup: "rg-a",
	})
	m.RecordActivityLogEvent(&driver.ActivityLogEvent{
		OperationName: "Microsoft.Compute/virtualMachines/write", ResourceGroup: "rg-b",
	})

	all := m.ListActivityLogEvents(driver.ActivityLogQuery{})
	require.Len(t, all, 2)

	// Newest first: the compute event was recorded last.
	assert.Equal(t, "Microsoft.Compute/virtualMachines/write", all[0].OperationName)
	// Recorder stamps id, correlation id, timestamp, and defaults.
	assert.NotEmpty(t, all[0].EventID)
	assert.NotEmpty(t, all[0].CorrelationID)
	assert.Equal(t, "Succeeded", all[0].Status)
	assert.False(t, all[0].EventTimestamp.IsZero())

	filtered := m.ListActivityLogEvents(driver.ActivityLogQuery{ResourceGroup: "rg-a"})
	require.Len(t, filtered, 1)
	assert.Equal(t, "rg-a", filtered[0].ResourceGroup)
}

// TestActivityLogNilEventIgnored verifies a nil event is a safe no-op.
func TestActivityLogNilEventIgnored(t *testing.T) {
	m, _ := newTestMock()
	m.RecordActivityLogEvent(nil)
	assert.Empty(t, m.ListActivityLogEvents(driver.ActivityLogQuery{}))
}
