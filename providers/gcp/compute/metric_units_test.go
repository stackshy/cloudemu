package compute

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// unitRecorder records the unit of each metric put. Only PutMetricData is used.
type unitRecorder struct {
	mondriver.Monitoring
	units map[string]string
}

func (r *unitRecorder) PutMetricData(_ context.Context, data []mondriver.MetricDatum) error {
	for _, d := range data {
		r.units[d.MetricName] = d.Unit
	}

	return nil
}

// TestInstanceMetricGCPUnits checks the GCE metrics carry Cloud Monitoring
// units. They were stored as the CloudWatch-only None.
func TestInstanceMetricGCPUnits(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(clk), config.WithRegion("us-central1"), config.WithProjectID("p")))
	rec := &unitRecorder{units: map[string]string{}}
	m.SetMonitoring(rec)

	_, err := m.RunInstances(context.Background(), driver.InstanceConfig{ImageID: "img-1", InstanceType: "n1-standard-1"}, 1)
	require.NoError(t, err)

	assert.Equal(t, map[string]string{
		"instance/cpu/utilization":              "10^2.%",
		"instance/network/received_bytes_count": "By",
		"instance/network/sent_bytes_count":     "By",
		"instance/disk/read_ops_count":          "1",
		"instance/disk/write_ops_count":         "1",
	}, rec.units)
}
