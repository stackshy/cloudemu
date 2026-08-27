package cloudwatchlogs

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetricFilterFieldsRoundTrip locks the metric-filter round-trip fix:
// DefaultValue, Unit, and Dimensions set on PutMetricFilter survive to
// DescribeMetricFilters (previously dropped).
func TestMetricFilterFieldsRoundTrip(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "g"})
	require.NoError(t, err)

	def := 0.0
	require.NoError(t, m.PutMetricFilter(ctx, &driver.MetricFilterConfig{
		Name:            "f",
		LogGroup:        "g",
		FilterPattern:   "ERROR",
		MetricName:      "ErrCount",
		MetricNamespace: "MyApp",
		MetricValue:     "1",
		DefaultValue:    &def,
		Unit:            "Count",
		Dimensions:      map[string]string{"Service": "api"},
	}))

	filters, err := m.DescribeMetricFilters(ctx, "g")
	require.NoError(t, err)
	require.Len(t, filters, 1)

	require.NotNil(t, filters[0].DefaultValue)
	assert.InDelta(t, 0.0, *filters[0].DefaultValue, 0)
	assert.Equal(t, "Count", filters[0].Unit)
	assert.Equal(t, "api", filters[0].Dimensions["Service"])
}
