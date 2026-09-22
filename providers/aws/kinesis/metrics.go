package kinesis

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Stream-level AWS/Kinesis metrics, published on the StreamName dimension with
// the units the Kinesis Data Streams CloudWatch reference documents.
const (
	metricsNamespace = "AWS/Kinesis"

	unitBytes        = "Bytes"
	unitCount        = "Count"
	unitMilliseconds = "Milliseconds"
)

// SetMonitoring wires the CloudWatch backend that receives the stream-level
// AWS/Kinesis metrics. Nil-safe: with no backend wired nothing is published.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// putRecordsOutcome summarizes one PutRecords call for its metrics.
type putRecordsOutcome struct {
	total  int
	failed int
	bytes  int // bytes of the records that were stored
}

func (m *Mock) publish(ctx context.Context, stream string, data []mondriver.MetricDatum) {
	if m.monitoring == nil {
		return
	}

	now := m.now()

	for i := range data {
		data[i].Namespace = metricsNamespace
		data[i].Dimensions = map[string]string{"StreamName": stream}
		data[i].Timestamp = now
	}

	_ = m.monitoring.PutMetricData(ctx, data)
}

func (m *Mock) sinceMillis(start time.Time) float64 {
	return float64(m.now().Sub(start)) / float64(time.Millisecond)
}

// emitPutRecordMetrics publishes the metrics of one successful PutRecord.
func (m *Mock) emitPutRecordMetrics(ctx context.Context, stream string, size int, start time.Time) {
	m.publish(ctx, stream, []mondriver.MetricDatum{
		{MetricName: "IncomingBytes", Value: float64(size), Unit: unitBytes},
		{MetricName: "IncomingRecords", Value: 1, Unit: unitCount},
		{MetricName: "PutRecord.Bytes", Value: float64(size), Unit: unitBytes},
		{MetricName: "PutRecord.Success", Value: 1, Unit: unitCount},
		{MetricName: "PutRecord.Latency", Value: m.sinceMillis(start), Unit: unitMilliseconds},
	})
}

// emitPutRecordsMetrics publishes the metrics of one accepted PutRecords call.
// PutRecords.Success is 1 when at least one record succeeded, as in real Kinesis.
func (m *Mock) emitPutRecordsMetrics(ctx context.Context, stream string, o putRecordsOutcome, start time.Time) {
	succeeded := o.total - o.failed

	var success float64
	if succeeded > 0 {
		success = 1
	}

	m.publish(ctx, stream, []mondriver.MetricDatum{
		{MetricName: "IncomingBytes", Value: float64(o.bytes), Unit: unitBytes},
		{MetricName: "IncomingRecords", Value: float64(succeeded), Unit: unitCount},
		{MetricName: "PutRecords.Bytes", Value: float64(o.bytes), Unit: unitBytes},
		{MetricName: "PutRecords.Success", Value: success, Unit: unitCount},
		{MetricName: "PutRecords.TotalRecords", Value: float64(o.total), Unit: unitCount},
		{MetricName: "PutRecords.SuccessfulRecords", Value: float64(succeeded), Unit: unitCount},
		{MetricName: "PutRecords.FailedRecords", Value: float64(o.failed), Unit: unitCount},
		{MetricName: "PutRecords.Latency", Value: m.sinceMillis(start), Unit: unitMilliseconds},
	})
}

// emitGetRecordsMetrics publishes the metrics of one successful GetRecords.
// GetRecords.IteratorAgeMilliseconds is the age of the last record returned
// (now minus its arrival time), zero when the call returned nothing.
func (m *Mock) emitGetRecordsMetrics(ctx context.Context, stream string, recs []driver.Record, start time.Time) {
	var size int
	for i := range recs {
		size += len(recs[i].Data)
	}

	var age float64
	if n := len(recs); n > 0 {
		age = max(0, float64(m.now().Sub(recs[n-1].ApproximateArrivalTimestamp))/float64(time.Millisecond))
	}

	m.publish(ctx, stream, []mondriver.MetricDatum{
		{MetricName: "GetRecords.Bytes", Value: float64(size), Unit: unitBytes},
		{MetricName: "GetRecords.Records", Value: float64(len(recs)), Unit: unitCount},
		{MetricName: "GetRecords.Success", Value: 1, Unit: unitCount},
		{MetricName: "GetRecords.IteratorAgeMilliseconds", Value: age, Unit: unitMilliseconds},
		{MetricName: "GetRecords.Latency", Value: m.sinceMillis(start), Unit: unitMilliseconds},
	})
}
