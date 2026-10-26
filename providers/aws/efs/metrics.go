package efs

import (
	"context"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/services/efs/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// AWS/EFS CloudWatch metrics
// (https://docs.aws.amazon.com/efs/latest/ug/efs-metrics.html).
//
// Real EFS pushes its metrics on a schedule, not per request: "Amazon EFS metric
// data is automatically sent to CloudWatch at 1-minute periods, unless noted"
// (https://docs.aws.amazon.com/efs/latest/ug/monitoring-cloudwatch.html), and
// StorageBytes "is emitted to CloudWatch every 15 minutes". Tick does the same
// on the shared serve scheduler.
//
// Only metrics whose values cloudemu actually knows are published:
//
//   - StorageBytes (Bytes) on FileSystemId + StorageClass, for the Total,
//     Standard and IA classes, from the file system's stored SizeInBytes. The
//     Archive, IASizeOverhead and ArchiveSizeOverhead classes are not modeled.
//   - PermittedThroughput (Bytes/Second) on FileSystemId, for provisioned
//     file systems only: with no stored data it is exactly the provisioned
//     amount. For bursting and elastic file systems it depends on burst credits
//     and AWS-side limits cloudemu does not model, so it is not published.
//
// The I/O metrics (TotalIOBytes, DataReadIOBytes, MeteredIOBytes, ...),
// ClientConnections, PercentIOLimit and BurstCreditBalance describe NFS traffic
// and credit accounting that cloudemu does not serve, and TimeSinceLastSync
// needs real replication, so none of them is published.
const (
	metricsNamespace = "AWS/EFS"

	dimFileSystemID = "FileSystemId"
	dimStorageClass = "StorageClass"

	metricStorageBytes        = "StorageBytes"
	metricPermittedThroughput = "PermittedThroughput"

	unitBytes          = "Bytes"
	unitBytesPerSecond = "Bytes/Second"

	bytesPerMiB = 1 << 20

	// throughputInterval is how often the 1-minute metrics are published.
	throughputInterval = time.Minute
	// storageInterval is how often StorageBytes is published.
	storageInterval = 15 * time.Minute
)

// SetMonitoring wires the CloudWatch backend that receives the AWS/EFS metrics.
// Nil-safe: with no backend wired nothing is published.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// Tick finishes the lifecycle transitions whose windows have elapsed (a
// "deleting" file system or mount target is removed) and publishes the AWS/EFS
// metrics that are due at now for every available file system. It reports
// whether it changed state: a reap, or published metrics (the datapoints land
// in CloudWatch's persisted state and the sample times in EFS's). It has the
// config.Tickable signature of the shared serve scheduler; library mode never
// calls it, so tests drive it with a FakeClock. Each file system is sampled
// under its own lock and everything is published after all locks are released.
func (m *Mock) Tick(now time.Time) bool {
	var (
		data    []mondriver.MetricDatum
		changed bool
	)

	ids := m.fileSystems.Keys()
	sort.Strings(ids)

	for _, id := range ids {
		fd, ok, reaped := m.settleFS(id)
		changed = changed || reaped

		if !ok || m.monitoring == nil {
			continue
		}

		data = append(data, m.dueMetrics(fd, now)...)
	}

	if len(data) > 0 {
		_ = m.monitoring.PutMetricData(context.Background(), data)

		changed = true
	}

	return changed
}

// dueMetrics samples fd's metrics that are due at now and records the sample
// times. A file system that is still creating or already deleting reports
// nothing. Callers must not hold fd.mu.
func (m *Mock) dueMetrics(fd *fsData, now time.Time) []mondriver.MetricDatum {
	fd.mu.Lock()
	defer fd.mu.Unlock()

	if m.fsStateLocked(fd) != driver.StateAvailable {
		return nil
	}

	id := fd.fs.FileSystemID

	var data []mondriver.MetricDatum

	if due(fd.storageSampleAt, now, storageInterval) {
		fd.storageSampleAt = now

		for _, c := range []struct {
			class string
			value int64
		}{
			{"Total", fd.fs.SizeInBytes.Value},
			{"Standard", fd.fs.SizeInBytes.ValueInStandard},
			{"IA", fd.fs.SizeInBytes.ValueInIA},
		} {
			data = append(data, mondriver.MetricDatum{
				Namespace: metricsNamespace, MetricName: metricStorageBytes,
				Value: float64(c.value), Unit: unitBytes, Timestamp: now,
				Dimensions: map[string]string{dimFileSystemID: id, dimStorageClass: c.class},
			})
		}
	}

	if fd.fs.ThroughputMode == driver.ThroughputProvisioned && due(fd.throughputSampleAt, now, throughputInterval) {
		fd.throughputSampleAt = now

		data = append(data, mondriver.MetricDatum{
			Namespace: metricsNamespace, MetricName: metricPermittedThroughput,
			Value: fd.fs.ProvisionedThroughputInMibps * bytesPerMiB, Unit: unitBytesPerSecond, Timestamp: now,
			Dimensions: map[string]string{dimFileSystemID: id},
		})
	}

	return data
}

// due reports whether a metric last sampled at last is due again at now.
func due(last, now time.Time, interval time.Duration) bool {
	return last.IsZero() || now.Sub(last) >= interval
}
