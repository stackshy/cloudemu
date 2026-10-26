package kafka

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// AWS/Kafka CloudWatch metrics for provisioned clusters
// (https://docs.aws.amazon.com/msk/latest/developerguide/metrics-details.html).
//
// Real MSK pushes its metrics on a schedule, not per request: "The metrics that
// you configure for your MSK Provisioned clusters are automatically collected
// and pushed to CloudWatch at 1 minute intervals." Tick does the same on the
// shared serve scheduler, and only for clusters that are ACTIVE (every DEFAULT
// metric is documented as visible "After the cluster gets to the ACTIVE
// state"). The dimension names contain spaces ("Cluster Name", "Broker ID").
//
// Only DEFAULT-level metrics whose values cloudemu actually knows are
// published (DEFAULT metrics are published at every monitoring level):
//
//   - Cluster Name: ActiveControllerCount (1: exactly one active controller),
//     GlobalTopicCount and GlobalPartitionCount (from the cluster's stored
//     topics; partitions exclude replicas, as documented) and
//     OfflinePartitionsCount (0: no broker is ever unavailable).
//   - Cluster Name, Broker ID (brokers 1..NumberOfBrokerNodes, the IDs
//     ListNodes reports): UnderReplicatedPartitions and
//     UnderMinIsrPartitionCount (0: replicas never fall behind, since no
//     broker runs).
//
// The CPU, memory, disk, network, connection and throughput metrics describe
// broker hosts and client traffic that cloudemu does not run, and per-broker
// PartitionCount/LeaderCount need a partition assignment it does not model,
// so none of them is published. Serverless clusters publish a different
// metric set and are skipped.
const (
	metricsNamespace = "AWS/Kafka"

	dimClusterName = "Cluster Name"
	dimBrokerID    = "Broker ID"

	unitCount = "Count"

	// metricsInterval is how often the AWS/Kafka metrics are published.
	metricsInterval = time.Minute
)

// SetMonitoring wires the CloudWatch backend that receives the AWS/Kafka
// metrics. With no backend wired (the default) nothing is published.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// Tick is the MSK work of the shared serve scheduler (config.Tickable). At now
// it removes every cluster whose DELETING window has ended, so a finished
// delete is dropped (and persisted) even when nothing reads the cluster, and
// publishes the AWS/Kafka metrics that are due for every ACTIVE provisioned
// cluster. It reports whether it removed or published anything. Every state
// check uses now, not the options clock. Library mode never calls it, so tests
// drive it with a FakeClock. Each cluster is sampled under its own lock and
// everything is published after all locks are released.
func (m *Mock) Tick(now time.Time) bool {
	arns := m.clusters.Keys()
	sort.Strings(arns)

	reaped := false

	var data []mondriver.MetricDatum

	for _, arn := range arns {
		cd, ok := m.clusters.Get(arn)
		if !ok {
			continue
		}

		if m.reapIfGoneAt(arn, cd, now) {
			reaped = true

			continue
		}

		if m.monitoring != nil {
			data = append(data, m.dueMetrics(cd, now)...)
		}
	}

	if len(data) > 0 {
		_ = m.monitoring.PutMetricData(context.Background(), data)
	}

	return reaped || len(data) > 0
}

// dueMetrics samples cd's metrics if they are due at now and records the sample
// time. A cluster that is not ACTIVE, or is serverless, reports nothing.
// Callers must not hold cd.mu.
func (*Mock) dueMetrics(cd *clusterData, now time.Time) []mondriver.MetricDatum {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	if cd.cluster.ClusterType != driver.ClusterTypeProvisioned ||
		clusterStateAt(cd, now) != driver.ClusterStateActive {
		return nil
	}

	if !cd.metricsSampleAt.IsZero() && now.Sub(cd.metricsSampleAt) < metricsInterval {
		return nil
	}

	cd.metricsSampleAt = now

	name := cd.cluster.ClusterName

	var partitions int64
	for _, t := range cd.topics {
		partitions += int64(t.NumberOfPartitions)
	}

	datum := func(metric string, value float64, dims map[string]string) mondriver.MetricDatum {
		return mondriver.MetricDatum{
			Namespace: metricsNamespace, MetricName: metric, Value: value,
			Unit: unitCount, Dimensions: dims, Timestamp: now,
		}
	}

	clusterDims := map[string]string{dimClusterName: name}

	data := []mondriver.MetricDatum{
		datum("ActiveControllerCount", 1, clusterDims),
		datum("GlobalTopicCount", float64(len(cd.topics)), clusterDims),
		datum("GlobalPartitionCount", float64(partitions), clusterDims),
		datum("OfflinePartitionsCount", 0, clusterDims),
	}

	for i := 1; i <= int(cd.cluster.NumberOfBrokerNodes); i++ {
		dims := map[string]string{dimClusterName: name, dimBrokerID: strconv.Itoa(i)}

		data = append(data,
			datum("UnderReplicatedPartitions", 0, dims),
			datum("UnderMinIsrPartitionCount", 0, dims),
		)
	}

	return data
}
