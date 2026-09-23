package blobstorage

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/monitor"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// TestMetricAzureUnits checks the storage metrics carry their Azure Monitor
// units. They were stored as the CloudWatch-only None.
func TestMetricAzureUnits(t *testing.T) {
	ctx := context.Background()
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk))

	mon := monitor.New(opts)
	m := New(opts)
	m.SetMonitoring(mon)

	require.NoError(t, m.CreateBucket(ctx, "c1"))
	require.NoError(t, m.PutObject(ctx, "c1", "f.txt", []byte("hello"), "text/plain", nil))

	for name, want := range map[string]string{"Ingress": "Bytes", "Transactions": "Count"} {
		res, err := mon.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace: "Microsoft.Storage/storageAccounts", MetricName: name, Stat: "Sum", Period: 60,
			Dimensions: map[string]string{"containerName": "c1"},
			StartTime:  clk.Now().Add(-time.Minute), EndTime: clk.Now().Add(time.Minute),
		})
		require.NoError(t, err)
		assert.Equal(t, want, res.Unit, name)
	}
}
