package managedkafka

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	mkdriver "github.com/stackshy/cloudemu/v2/services/managedkafka/driver"
)

const (
	proj   = "p"
	region = "us-central1"
	subnet = "projects/p/regions/us-central1/subnetworks/s"
)

func newMock(t *testing.T) (*Mock, *config.FakeClock) {
	t.Helper()

	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	return New(config.NewOptions(config.WithProjectID(proj), config.WithClock(clock))), clock
}

func cluster(id string) *mkdriver.Cluster {
	return &mkdriver.Cluster{
		Project: proj, Location: region, ID: id,
		VcpuCount: 3, MemoryBytes: 3 * gib, Subnets: []string{subnet},
		KmsKey: "k1", Labels: map[string]string{"a": "b"},
	}
}

func mustCreate(t *testing.T, m *Mock, id string) {
	t.Helper()

	if _, _, err := m.CreateCluster(context.Background(), cluster(id)); err != nil {
		t.Fatalf("CreateCluster(%s): %v", id, err)
	}
}

func TestClusterCRUDAndClone(t *testing.T) {
	m, clock := newMock(t)
	ctx := context.Background()

	c, op, err := m.CreateCluster(ctx, cluster("c1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if c.State != stateActive || !op.Done || op.Type != opCreate || c.CreateTime != clock.Now() {
		t.Fatalf("created = %+v op = %+v", c, op)
	}

	// Mutating a returned value must not alias the store.
	c.Labels["a"] = "mutated"
	c.Subnets[0] = "x"

	got, err := m.GetCluster(ctx, proj, region, "c1")
	if err != nil || got.Labels["a"] != "b" || got.Subnets[0] != subnet {
		t.Fatalf("store aliased: %+v %v", got, err)
	}

	if _, _, err = m.CreateCluster(ctx, cluster("c1")); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate: %v", err)
	}

	if list, _ := m.ListClusters(ctx, proj, region); len(list) != 1 {
		t.Fatalf("list = %d", len(list))
	}

	if _, err = m.DeleteCluster(ctx, proj, region, "c1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err = m.GetCluster(ctx, proj, region, "c1"); !cerrors.IsNotFound(err) {
		t.Fatalf("get after delete: %v", err)
	}
}

func TestClusterValidation(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	cases := map[string]func(c *mkdriver.Cluster){
		"vcpu 2":           func(c *mkdriver.Cluster) { c.VcpuCount = 2; c.MemoryBytes = 2 * gib },
		"mem under":        func(c *mkdriver.Cluster) { c.MemoryBytes = 3*gib - 1 },
		"mem over":         func(c *mkdriver.Cluster) { c.MemoryBytes = 24*gib + 1 },
		"vcpu overflow":    func(c *mkdriver.Cluster) { c.VcpuCount = 1 << 62 },
		"no subnets":       func(c *mkdriver.Cluster) { c.Subnets = nil },
		"11 subnets":       func(c *mkdriver.Cluster) { c.Subnets = make([]string, maxNetworkConfigs+1) },
		"bad subnet shape": func(c *mkdriver.Cluster) { c.Subnets = []string{"projects/p/zones/z/subnetworks/s"} },
		"bad id":           func(c *mkdriver.Cluster) { c.ID = "9starts-with-digit" },
		"empty id":         func(c *mkdriver.Cluster) { c.ID = "" },
		"bad mode":         func(c *mkdriver.Cluster) { c.RebalanceMode = "REBALANCE_SOMETIMES" },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := cluster("v")
			mutate(c)

			if _, _, err := m.CreateCluster(ctx, c); !cerrors.IsInvalidArgument(err) {
				t.Fatalf("want INVALID_ARGUMENT, got %v", err)
			}
		})
	}
}

func TestClusterMask(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	mustCreate(t, m, "c1")

	desired := &mkdriver.Cluster{
		Project: proj, Location: region, ID: "c1",
		VcpuCount: 4, MemoryBytes: 32 * gib, Subnets: []string{subnet, subnet + "2"},
		RebalanceMode: rebalanceNone, Labels: map[string]string{"x": "y"},
	}

	got, op, err := m.UpdateCluster(ctx, desired, []string{"capacity_config", "gcp_config.access_config.network_configs"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if op.Type != opUpdate || got.VcpuCount != 4 || got.MemoryBytes != 32*gib || len(got.Subnets) != 2 ||
		got.RebalanceMode != rebalanceNone || got.Labels["a"] != "b" || got.KmsKey != "k1" {
		t.Fatalf("masked update = %+v", got)
	}

	desired.KmsKey = "k1"

	all, _, err := m.UpdateCluster(ctx, desired, []string{"*"})
	if err != nil || all.RebalanceMode != rebalanceNone || all.Labels["x"] != "y" {
		t.Fatalf("* update = %+v %v", all, err)
	}

	for _, bad := range [][]string{nil, {"nope"}, {"state"}, {"gcpConfig.kmsKey"}} {
		if _, _, err := m.UpdateCluster(ctx, desired, bad); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("mask %v: want INVALID_ARGUMENT, got %v", bad, err)
		}
	}

	desired.KmsKey = "other"
	if _, _, err := m.UpdateCluster(ctx, desired, []string{"gcpConfig"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("kmsKey change via gcpConfig: %v", err)
	}

	if _, _, err := m.UpdateCluster(ctx, &mkdriver.Cluster{Project: proj, Location: region, ID: "ghost"},
		[]string{"labels"}); !cerrors.IsNotFound(err) {
		t.Fatalf("update missing: %v", err)
	}
}

func TestTopicsAndCascade(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	topic := func(id string, parts int32) *mkdriver.Topic {
		return &mkdriver.Topic{
			Project: proj, Location: region, ClusterID: "c1", ID: id,
			PartitionCount: parts, ReplicationFactor: 3, Configs: map[string]string{"k": "v"},
		}
	}

	if _, err := m.CreateTopic(ctx, topic("t1", 1)); !cerrors.IsNotFound(err) {
		t.Fatalf("topic under missing cluster: %v", err)
	}

	mustCreate(t, m, "c1")

	if _, err := m.CreateTopic(ctx, topic("t1", 1)); err != nil {
		t.Fatalf("create topic: %v", err)
	}

	for _, id := range []string{"", "bad/id", ".."} {
		if _, err := m.CreateTopic(ctx, topic(id, 1)); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("topic id %q: %v", id, err)
		}
	}

	if _, err := m.UpdateTopic(ctx, topic("t1", 4), []string{"partition_count"}); err != nil {
		t.Fatalf("grow: %v", err)
	}

	if _, err := m.UpdateTopic(ctx, topic("t1", 2), []string{"partitionCount"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("shrink: %v", err)
	}

	rf := topic("t1", 4)
	rf.ReplicationFactor = 1

	if _, err := m.UpdateTopic(ctx, rf, []string{"*"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("replicationFactor change via *: %v", err)
	}

	if _, err := m.DeleteCluster(ctx, proj, region, "c1"); err != nil {
		t.Fatalf("delete cluster: %v", err)
	}

	if m.topics.Len() != 0 {
		t.Fatalf("topics survived cluster delete: %d", m.topics.Len())
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	mustCreate(t, m, "c1")

	if _, err := m.CreateTopic(ctx, &mkdriver.Topic{
		Project: proj, Location: region, ClusterID: "c1", ID: "t", PartitionCount: 1, ReplicationFactor: 1,
	}); err != nil {
		t.Fatalf("topic: %v", err)
	}

	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored, _ := newMock(t)
	if err := restored.Restore(ctx, snap); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if _, err := restored.GetTopic(ctx, proj, region, "c1", "t"); err != nil {
		t.Fatalf("topic after restore: %v", err)
	}

	if restored.opSeq.Load() != m.opSeq.Load() {
		t.Fatalf("opSeq = %d, want %d", restored.opSeq.Load(), m.opSeq.Load())
	}
}
