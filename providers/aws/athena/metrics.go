package athena

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// AWS/Athena query metrics, published once per completed query on the
// {QueryState, QueryType, WorkGroup} dimensions: the *Time metrics are
// Milliseconds and ProcessedBytes (DML only) is Bytes.
const (
	metricsNamespace = "AWS/Athena"

	unitBytes        = "Bytes"
	unitMilliseconds = "Milliseconds"
)

// SetMonitoring wires the CloudWatch backend that receives the AWS/Athena query
// metrics. Nil-safe: with no backend wired nothing is published.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// emitQueryMetrics publishes a completed query's metrics, only when its
// workgroup has PublishCloudWatchMetricsEnabled (the default). Real Athena
// publishes nothing for a workgroup with it turned off.
//
//nolint:gocritic // hugeParam: wg passed by value, read-only here
func (m *Mock) emitQueryMetrics(ctx context.Context, qe *driver.QueryExecution, wg driver.WorkGroup) {
	if m.monitoring == nil {
		return
	}

	if p := wg.Configuration.PublishCloudWatchMetricsEnabled; p != nil && !*p {
		return
	}

	dims := map[string]string{
		"QueryState": qe.Status.State,
		"QueryType":  qe.StatementType,
		"WorkGroup":  qe.WorkGroup,
	}
	now := m.now()

	st := &qe.Statistics
	data := []mondriver.MetricDatum{
		{MetricName: "TotalExecutionTime", Value: float64(st.TotalExecutionTimeInMillis), Unit: unitMilliseconds},
		{MetricName: "EngineExecutionTime", Value: float64(st.EngineExecutionTimeInMillis), Unit: unitMilliseconds},
		{MetricName: "QueryQueueTime", Value: float64(st.QueryQueueTimeInMillis), Unit: unitMilliseconds},
		{MetricName: "QueryPlanningTime", Value: float64(st.QueryPlanningTimeInMillis), Unit: unitMilliseconds},
		{MetricName: "ServicePreProcessingTime", Value: float64(st.ServicePreProcessingTimeInMillis), Unit: unitMilliseconds},
		{MetricName: "ServiceProcessingTime", Value: float64(st.ServiceProcessingTimeInMillis), Unit: unitMilliseconds},
	}

	// ProcessedBytes is reported for DML queries only.
	if qe.StatementType == driver.StatementTypeDML {
		data = append(data, mondriver.MetricDatum{
			MetricName: "ProcessedBytes", Value: float64(st.DataScannedInBytes), Unit: unitBytes,
		})
	}

	for i := range data {
		data[i].Namespace = metricsNamespace
		data[i].Dimensions = dims
		data[i].Timestamp = now
	}

	_ = m.monitoring.PutMetricData(ctx, data)
}
