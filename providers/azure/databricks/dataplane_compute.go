package databricks

import (
	"context"

	"github.com/stackshy/cloudemu/databricks/driver"
	"github.com/stackshy/cloudemu/errors"
)

// ResizeCluster changes a cluster's worker count or autoscale bounds.
func (m *Mock) ResizeCluster(_ context.Context, id string, numWorkers, autoscaleMin, autoscaleMax int32) error {
	cluster, ok := m.clusters.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "cluster %q not found", id)
	}

	updated := *cluster
	updated.NumWorkers = numWorkers
	updated.AutoscaleMin = autoscaleMin
	updated.AutoscaleMax = autoscaleMax
	m.clusters.Set(id, &updated)

	return nil
}

// PinCluster pins a cluster.
func (m *Mock) PinCluster(ctx context.Context, id string) error {
	return m.setClusterPinned(ctx, id, true)
}

// UnpinCluster unpins a cluster.
func (m *Mock) UnpinCluster(ctx context.Context, id string) error {
	return m.setClusterPinned(ctx, id, false)
}

func (m *Mock) setClusterPinned(_ context.Context, id string, pinned bool) error {
	cluster, ok := m.clusters.Get(id)
	if !ok {
		return errors.Newf(errors.NotFound, "cluster %q not found", id)
	}

	updated := *cluster
	updated.Pinned = pinned
	m.clusters.Set(id, &updated)

	return nil
}

// ListNodeTypes returns the available node-type catalog.
func (*Mock) ListNodeTypes(_ context.Context) ([]driver.NodeType, error) {
	return []driver.NodeType{
		{NodeTypeID: "Standard_DS3_v2", Description: "Standard_DS3_v2", NumCores: 4, MemoryMB: 14336},
		{NodeTypeID: "Standard_DS4_v2", Description: "Standard_DS4_v2", NumCores: 8, MemoryMB: 28672},
		{NodeTypeID: "Standard_DS5_v2", Description: "Standard_DS5_v2", NumCores: 16, MemoryMB: 57344},
	}, nil
}

// ListSparkVersions returns the available runtime versions.
func (*Mock) ListSparkVersions(_ context.Context) ([]driver.SparkVersion, error) {
	return []driver.SparkVersion{
		{Key: "13.3.x-scala2.12", Name: "13.3 LTS (includes Apache Spark 3.4.1, Scala 2.12)"},
		{Key: "14.3.x-scala2.12", Name: "14.3 LTS (includes Apache Spark 3.5.0, Scala 2.12)"},
	}, nil
}

// ListZones returns the availability zones and the default.
func (*Mock) ListZones(_ context.Context) (zones []string, defaultZone string, err error) {
	zones = []string{"eastus-1", "eastus-2", "eastus-3"}

	return zones, zones[0], nil
}
