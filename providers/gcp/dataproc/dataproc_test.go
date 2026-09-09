package dataproc

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	dpdriver "github.com/stackshy/cloudemu/v2/services/dataproc/driver"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	return New(config.NewOptions(config.WithClock(fc), config.WithProjectID("proj")))
}

func createCfg() *dpdriver.CreateClusterConfig {
	return &dpdriver.CreateClusterConfig{
		ProjectID:   "proj",
		Region:      "us-central1",
		ClusterName: "analytics",
		Labels:      map[string]string{"env": "test"},
		Config: dpdriver.ClusterConfig{
			GceClusterConfig: &dpdriver.GceClusterConfig{
				ZoneURI:        "us-central1-a",
				SubnetworkURI:  "default",
				InternalIPOnly: true,
			},
			MasterConfig: &dpdriver.InstanceGroupConfig{
				NumInstances:   1,
				MachineTypeURI: "n1-standard-4",
				DiskConfig:     &dpdriver.DiskConfig{BootDiskSizeGb: 100, BootDiskType: "pd-ssd"},
			},
			WorkerConfig: &dpdriver.InstanceGroupConfig{
				NumInstances:   2,
				MachineTypeURI: "n1-standard-4",
			},
			SoftwareConfig: &dpdriver.SoftwareConfig{ImageVersion: "2.1-debian11"},
		},
	}
}

func TestCreateClusterRunningAndRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	c, op, err := m.CreateCluster(ctx, createCfg())
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if !op.Done {
		t.Fatalf("operation not done")
	}

	if c.Status.State != dpdriver.StateRunning {
		t.Fatalf("state = %q, want RUNNING", c.Status.State)
	}

	if c.ClusterUUID == "" {
		t.Fatalf("clusterUuid not generated")
	}

	// Deep config round-trip.
	if got := c.Config.MasterConfig.NumInstances; got != 1 {
		t.Fatalf("master num = %d, want 1", got)
	}

	if got := c.Config.WorkerConfig.NumInstances; got != 2 {
		t.Fatalf("worker num = %d, want 2", got)
	}

	if got := c.Config.WorkerConfig.MachineTypeURI; got != "n1-standard-4" {
		t.Fatalf("worker machineType = %q", got)
	}

	if got := c.Config.SoftwareConfig.ImageVersion; got != "2.1-debian11" {
		t.Fatalf("imageVersion = %q, want echoed value", got)
	}

	if !c.Config.GceClusterConfig.InternalIPOnly {
		t.Fatalf("internalIpOnly not round-tripped")
	}

	if c.Config.MasterConfig.DiskConfig.BootDiskSizeGb != 100 {
		t.Fatalf("master boot disk size not round-tripped")
	}

	if len(c.Config.MasterConfig.InstanceNames) != 1 || c.Config.MasterConfig.InstanceNames[0] != "analytics-m" {
		t.Fatalf("master instanceNames = %v", c.Config.MasterConfig.InstanceNames)
	}

	if len(c.Config.WorkerConfig.InstanceNames) != 2 {
		t.Fatalf("worker instanceNames = %v", c.Config.WorkerConfig.InstanceNames)
	}

	if c.Config.ConfigBucket == "" || c.Config.TempBucket == "" {
		t.Fatalf("staging/temp buckets not generated")
	}

	if c.Labels["env"] != "test" {
		t.Fatalf("labels not round-tripped")
	}
}

func TestCreateClusterDefaults(t *testing.T) {
	m := newMock(t)

	c, _, err := m.CreateCluster(context.Background(), &dpdriver.CreateClusterConfig{
		ProjectID:   "proj",
		Region:      "us-central1",
		ClusterName: "bare",
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if c.Config.SoftwareConfig.ImageVersion != dpdriver.DefaultImageVersion {
		t.Fatalf("default image version = %q", c.Config.SoftwareConfig.ImageVersion)
	}

	if c.Config.MasterConfig.NumInstances != defaultMasterInstances {
		t.Fatalf("default master = %d", c.Config.MasterConfig.NumInstances)
	}

	if c.Config.WorkerConfig.NumInstances != defaultWorkerInstances {
		t.Fatalf("default worker = %d", c.Config.WorkerConfig.NumInstances)
	}

	if c.Config.MasterConfig.MachineTypeURI != defaultMachineType {
		t.Fatalf("default machine type = %q", c.Config.MasterConfig.MachineTypeURI)
	}
}

func TestCreateClusterUUIDStableAcrossReads(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	c1, _, err := m.CreateCluster(ctx, createCfg())
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	c2, err := m.GetCluster(ctx, "proj", "us-central1", "analytics")
	if err != nil {
		t.Fatalf("GetCluster: %v", err)
	}

	if c1.ClusterUUID != c2.ClusterUUID {
		t.Fatalf("clusterUuid unstable: %q vs %q", c1.ClusterUUID, c2.ClusterUUID)
	}
}

func TestCreateClusterAlreadyExists(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateCluster(ctx, createCfg()); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, _, err := m.CreateCluster(ctx, createCfg())
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("want AlreadyExists, got %v", err)
	}
}

func TestGetClusterNotFound(t *testing.T) {
	m := newMock(t)

	_, err := m.GetCluster(context.Background(), "proj", "us-central1", "ghost")
	if !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestListClustersScoped(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	mustCreate(t, m, "a", "us-central1")
	mustCreate(t, m, "b", "us-central1")
	mustCreate(t, m, "c", "europe-west1")

	got, err := m.ListClusters(ctx, "proj", "us-central1")
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("want 2 clusters in us-central1, got %d", len(got))
	}

	if got[0].ClusterName != "a" || got[1].ClusterName != "b" {
		t.Fatalf("not name-ordered: %v", []string{got[0].ClusterName, got[1].ClusterName})
	}
}

func TestUpdateClusterWorkerCount(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	mustCreate(t, m, "analytics", "us-central1")

	c, op, err := m.UpdateCluster(ctx, "proj", "us-central1", "analytics", dpdriver.UpdateClusterConfig{
		FieldMask:          []string{"config.worker_config.num_instances"},
		WorkerNumInstances: 5,
	})
	if err != nil {
		t.Fatalf("UpdateCluster: %v", err)
	}

	if !op.Done {
		t.Fatalf("update op not done")
	}

	if c.Config.WorkerConfig.NumInstances != 5 {
		t.Fatalf("worker num = %d, want 5", c.Config.WorkerConfig.NumInstances)
	}

	if len(c.Config.WorkerConfig.InstanceNames) != 5 {
		t.Fatalf("worker instanceNames not resized: %v", c.Config.WorkerConfig.InstanceNames)
	}
}

func TestUpdateClusterMaskIgnoresUnmasked(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	mustCreate(t, m, "analytics", "us-central1")

	// Mask only labels — a worker count in the payload must be ignored.
	c, _, err := m.UpdateCluster(ctx, "proj", "us-central1", "analytics", dpdriver.UpdateClusterConfig{
		FieldMask:          []string{"labels"},
		Labels:             map[string]string{"team": "data"},
		WorkerNumInstances: 99,
	})
	if err != nil {
		t.Fatalf("UpdateCluster: %v", err)
	}

	if c.Config.WorkerConfig.NumInstances == 99 {
		t.Fatalf("unmasked worker count was applied")
	}

	if c.Labels["team"] != "data" {
		t.Fatalf("masked labels not applied")
	}
}

func TestDeleteCluster(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	mustCreate(t, m, "analytics", "us-central1")

	op, err := m.DeleteCluster(ctx, "proj", "us-central1", "analytics")
	if err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if !op.Done {
		t.Fatalf("delete op not done")
	}

	if _, err := m.GetCluster(ctx, "proj", "us-central1", "analytics"); !cerrors.IsNotFound(err) {
		t.Fatalf("cluster still present after delete: %v", err)
	}

	if _, err := m.DeleteCluster(ctx, "proj", "us-central1", "analytics"); !cerrors.IsNotFound(err) {
		t.Fatalf("second delete: want NotFound, got %v", err)
	}
}

func TestGetOperationRecorded(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	_, op, err := m.CreateCluster(ctx, createCfg())
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got, err := m.GetOperation(ctx, op.Name)
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}

	if !got.Done || got.Name != op.Name {
		t.Fatalf("operation mismatch: %+v", got)
	}
}

func TestGetOperationUnknownIsDone(t *testing.T) {
	m := newMock(t)

	got, err := m.GetOperation(context.Background(), "projects/proj/regions/us-central1/operations/none")
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}

	if !got.Done {
		t.Fatalf("unknown op should report done")
	}
}

func TestInstanceNamesBoundsAbsurdCount(t *testing.T) {
	// A pathological instance count must be clamped, not drive an unbounded
	// allocation. A normal count is returned in full.
	const maxInstanceGroupSize = 10000

	got := instanceNames("clus", "w", maxInstanceGroupSize+5)
	if len(got) != maxInstanceGroupSize {
		t.Fatalf("clamped names len = %d, want %d", len(got), maxInstanceGroupSize)
	}

	if n := instanceNames("clus", "w", 3); len(n) != 3 {
		t.Fatalf("normal names len = %d, want 3", len(n))
	}
}

func mustCreate(t *testing.T, m *Mock, name, region string) {
	t.Helper()

	cfg := createCfg()
	cfg.ClusterName = name
	cfg.Region = region

	if _, _, err := m.CreateCluster(context.Background(), cfg); err != nil {
		t.Fatalf("create %s/%s: %v", region, name, err)
	}
}
