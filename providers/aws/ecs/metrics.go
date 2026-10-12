package ecs

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// ECS publishes two metric namespaces. AWS/ECS carries the reservation and
// live-task metrics the service always emits; ECS/ContainerInsights carries the
// cluster and service counts and exists only for clusters with Container
// Insights enabled (cluster setting containerInsights = enabled or enhanced, or
// the account setting when the cluster sets none).
//
// The emulator runs no workload, so it never publishes CPU/memory utilization or
// the other usage-driven series: only metrics derived from state it really holds
// (task and service counts, reserved container-instance capacity).
const (
	metricsNamespaceECS      = "AWS/ECS"
	metricsNamespaceInsights = "ECS/ContainerInsights"
	metricUnitCount          = "Count"
	metricUnitPercent        = "Percent"
	percentOf                = 100.0
	dimClusterName           = "ClusterName"

	settingContainerInsights = "containerInsights"
	insightsEnabled          = "enabled"
	insightsEnhanced         = "enhanced"
)

// SetMonitoring wires the CloudWatch backend that receives the AWS/ECS and
// ECS/ContainerInsights metrics. Safe to leave unset: no metrics are published.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) { m.monitoring = mon }

// containerInsightsOn reports whether Container Insights is enabled for the
// cluster: its own containerInsights setting wins, else the account setting.
func (m *Mock) containerInsightsOn(c *driver.Cluster) bool {
	value := ""

	if c != nil {
		for _, s := range c.Settings {
			if s.Name == settingContainerInsights {
				value = s.Value
			}
		}
	}

	if value == "" {
		if s, ok := m.settings.Get(settingContainerInsights); ok {
			value = s.Value
		}
	}

	return value == insightsEnabled || value == insightsEnhanced
}

// metricBatch accumulates the datums of one publish.
type metricBatch struct {
	data []mondriver.MetricDatum
	now  time.Time
}

func (b *metricBatch) add(ns, name, unit string, value float64, dims map[string]string) {
	b.data = append(b.data, mondriver.MetricDatum{
		Namespace: ns, MetricName: name, Value: value, Unit: unit, Timestamp: b.now, Dimensions: dims,
	})
}

// publishClusterMetrics publishes the metrics of one cluster's current state. It
// is called after every mutation, outside every emulator lock, and does nothing
// for an unknown or deleted cluster.
func (m *Mock) publishClusterMetrics(cluster string) {
	if m.monitoring == nil {
		return
	}

	c, stored := m.clusters.Get(cluster)
	if (stored && c.Status != statusActive) || (!stored && cluster != defaultCluster) {
		return
	}

	batch := &metricBatch{now: m.opts.Clock.Now()}
	insights := m.containerInsightsOn(c)

	m.addClusterMetrics(batch, cluster, insights)

	for _, svc := range m.services.SortedValues() {
		if clusterNameFromARN(svc.ClusterARN) == cluster && svc.Status == statusActive {
			m.addServiceMetrics(batch, cluster, svc, insights)
		}
	}

	if len(batch.data) > 0 {
		_ = m.monitoring.PutMetricData(context.Background(), batch.data)
	}
}

// addClusterMetrics adds the cluster-level series: the reservation of its
// container instances and, with Container Insights, the instance, service and
// task counts.
func (m *Mock) addClusterMetrics(b *metricBatch, cluster string, insights bool) {
	dims := map[string]string{dimClusterName: cluster}

	if cpuPct, memPct, ok := m.reservationPercent(cluster); ok {
		b.add(metricsNamespaceECS, "CPUReservation", metricUnitPercent, cpuPct, dims)
		b.add(metricsNamespaceECS, "MemoryReservation", metricUnitPercent, memPct, dims)
	}

	if !insights {
		return
	}

	services, running, _, instances := m.clusterCounts(cluster)

	b.add(metricsNamespaceInsights, "ContainerInstanceCount", metricUnitCount, float64(instances), dims)
	b.add(metricsNamespaceInsights, "ServiceCount", metricUnitCount, float64(services), dims)
	b.add(metricsNamespaceInsights, "TaskCount", metricUnitCount, float64(running), dims)
}

// addServiceMetrics adds one service's series: LiveTaskCount while it has
// running tasks and, with Container Insights, its task, deployment and task-set
// counts.
func (m *Mock) addServiceMetrics(b *metricBatch, cluster string, svc *driver.Service, insights bool) {
	running, pending := m.liveServiceTaskCounts(cluster, serviceGroup(svc.Name))
	dims := map[string]string{dimClusterName: cluster, "ServiceName": svc.Name}

	if running > 0 {
		b.add(metricsNamespaceECS, "LiveTaskCount", metricUnitCount, float64(running), dims)
	}

	if !insights {
		return
	}

	b.add(metricsNamespaceInsights, "DesiredTaskCount", metricUnitCount, float64(svc.DesiredCount), dims)
	b.add(metricsNamespaceInsights, "RunningTaskCount", metricUnitCount, float64(running), dims)
	b.add(metricsNamespaceInsights, "PendingTaskCount", metricUnitCount, float64(pending), dims)
	b.add(metricsNamespaceInsights, "DeploymentCount", metricUnitCount, float64(len(svc.Deployments)), dims)
	b.add(metricsNamespaceInsights, "TaskSetCount", metricUnitCount, float64(len(m.taskSetsOf(svc))), dims)
}

// reservationPercent returns the share of the cluster's registered CPU and
// memory that running tasks have reserved, and whether the cluster has any
// ACTIVE container instance at all (without one there is nothing to reserve).
func (m *Mock) reservationPercent(cluster string) (cpuPct, memPct float64, ok bool) {
	var regCPU, remCPU, regMem, remMem int

	for _, ci := range m.instances.All() {
		if instanceClusterName(ci.ARN) != cluster || ci.Status != statusActive {
			continue
		}

		ok = true
		regCPU += ci.RegisteredCPU
		remCPU += ci.RemainingCPU
		regMem += ci.RegisteredMemory
		remMem += ci.RemainingMemory
	}

	if !ok {
		return 0, 0, false
	}

	return reservedPercent(regCPU, remCPU), reservedPercent(regMem, remMem), true
}

func reservedPercent(registered, remaining int) float64 {
	if registered <= 0 {
		return 0
	}

	return float64(registered-remaining) / float64(registered) * percentOf
}
