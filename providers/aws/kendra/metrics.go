package kendra

import (
	"context"
	"time"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// AWS/Kendra metrics: DocumentsIndexed, DocumentsFailedToIndex and
// IndexQueryCount are published on the IndexId dimension; the
// DocumentsSubmitted* metrics add DataSourceId.
const (
	metricsNamespace = "AWS/Kendra"
	unitCount        = "Count"
)

// SetMonitoring wires the CloudWatch backend that receives the AWS/Kendra
// metrics. Nil-safe: with no backend wired nothing is published.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

func (m *Mock) publish(dims map[string]string, at time.Time, data ...mondriver.MetricDatum) {
	if m.monitoring == nil || len(data) == 0 {
		return
	}

	for i := range data {
		data[i].Namespace = metricsNamespace
		data[i].Dimensions = dims
		data[i].Timestamp = at
		data[i].Unit = unitCount
	}

	_ = m.monitoring.PutMetricData(context.Background(), data)
}

// recordIndexedMetrics publishes the documents a BatchPutDocument indexed and
// the ones that failed.
func (m *Mock) recordIndexedMetrics(indexID string, indexed, failed int) {
	dims := map[string]string{"IndexId": indexID}
	at := m.now()

	if indexed > 0 {
		m.publish(dims, at, mondriver.MetricDatum{MetricName: "DocumentsIndexed", Value: float64(indexed)})
	}

	if failed > 0 {
		m.publish(dims, at, mondriver.MetricDatum{MetricName: "DocumentsFailedToIndex", Value: float64(failed)})
	}
}

// recordQueryMetrics publishes one IndexQueryCount for a Query or Retrieve.
func (m *Mock) recordQueryMetrics(indexID string) {
	m.publish(map[string]string{"IndexId": indexID}, m.now(),
		mondriver.MetricDatum{MetricName: "IndexQueryCount", Value: 1})
}

// recordDeletionMetric publishes the documents a data source's sync job
// submitted for deletion.
func (m *Mock) recordDeletionMetric(indexID, dataSourceID string, deleted int) {
	if deleted == 0 {
		return
	}

	m.publish(map[string]string{"IndexId": indexID, "DataSourceId": dataSourceID}, m.now(),
		mondriver.MetricDatum{MetricName: "DocumentsSubmittedForDeletion", Value: float64(deleted)})
}
