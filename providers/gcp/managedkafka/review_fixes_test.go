package managedkafka

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	mkdriver "github.com/stackshy/cloudemu/v2/services/managedkafka/driver"
)

// TestCreateDefaultsAndOptionalFields: an unset rebalanceConfig.mode defaults to
// NO_REBALANCE and an unset kafkaVersion to 3.7.x (as the real API does), while
// tlsConfig, updateOptions and brokerCapacityConfig are stored and returned.
func TestCreateDefaultsAndOptionalFields(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	got, _, err := m.CreateCluster(ctx, cluster("d1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if got.RebalanceMode != rebalanceNone || got.KafkaVersion != defaultKafkaVersion {
		t.Fatalf("defaults: mode=%q version=%q, want %q/%q", got.RebalanceMode, got.KafkaVersion,
			rebalanceNone, defaultKafkaVersion)
	}

	unspecified := cluster("d2")
	unspecified.RebalanceMode = rebalanceUnspecified

	if got, _, err = m.CreateCluster(ctx, unspecified); err != nil || got.RebalanceMode != rebalanceNone {
		t.Fatalf("MODE_UNSPECIFIED create = %+v, %v; want NO_REBALANCE", got, err)
	}

	full := cluster("f1")
	full.KafkaVersion = "4.3.x"
	full.TLS = &mkdriver.TLSConfig{
		SSLPrincipalMappingRules: "RULE:^CN=(.*?),OU=S.*$/$1/,DEFAULT",
		CAPools:                  []string{"projects/other/locations/europe-west1/caPools/pool"},
	}
	full.AllowBrokerDownscaleOnClusterUpscale = true
	full.BrokerDiskSizeGib = 150

	if _, _, err = m.CreateCluster(ctx, full); err != nil {
		t.Fatalf("create full: %v", err)
	}

	full.TLS.CAPools[0] = "mutated-after-create"

	got, err = m.GetCluster(ctx, proj, region, "f1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.KafkaVersion != "4.3.x" || got.TLS == nil ||
		got.TLS.SSLPrincipalMappingRules != "RULE:^CN=(.*?),OU=S.*$/$1/,DEFAULT" ||
		len(got.TLS.CAPools) != 1 || got.TLS.CAPools[0] != "projects/other/locations/europe-west1/caPools/pool" ||
		!got.AllowBrokerDownscaleOnClusterUpscale || got.BrokerDiskSizeGib != 150 {
		t.Fatalf("stored optional fields = %+v tls=%+v", got, got.TLS)
	}
}

// TestOptionalFieldValidation covers the region, TLS and broker-disk rules.
func TestOptionalFieldValidation(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	pools := make([]string, maxCAPools+1)
	for i := range pools {
		pools[i] = fmt.Sprintf("projects/p/locations/l/caPools/p%d", i)
	}

	cases := map[string]func(c *mkdriver.Cluster){
		"subnet in another region": func(c *mkdriver.Cluster) {
			c.Subnets = []string{"projects/p/regions/europe-west1/subnetworks/s"}
		},
		"second subnet in another region": func(c *mkdriver.Cluster) {
			c.Subnets = []string{subnet, "projects/p/regions/us-east1/subnetworks/s"}
		},
		"broker disk under 100": func(c *mkdriver.Cluster) { c.BrokerDiskSizeGib = 99 },
		"bad ca pool":           func(c *mkdriver.Cluster) { c.TLS = &mkdriver.TLSConfig{CAPools: []string{"pool"}} },
		"11 ca pools":           func(c *mkdriver.Cluster) { c.TLS = &mkdriver.TLSConfig{CAPools: pools} },
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

	// A subnet in another PROJECT but the same region is allowed.
	c := cluster("xproj")
	c.Subnets = []string{"projects/host-project/regions/" + region + "/subnetworks/shared"}

	if _, _, err := m.CreateCluster(ctx, c); err != nil {
		t.Fatalf("cross-project same-region subnet: %v", err)
	}
}

// TestMaskOptionalFields: kafkaVersion is updatable (the API marks it an
// optional input, not immutable), and the tlsConfig, updateOptions and
// brokerCapacityConfig paths apply.
func TestMaskOptionalFields(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	mustCreate(t, m, "c1")

	want := cluster("c1")
	want.KafkaVersion = "4.3.x"
	want.TLS = &mkdriver.TLSConfig{SSLPrincipalMappingRules: "DEFAULT", CAPools: []string{"projects/a/locations/b/caPools/c"}}
	want.AllowBrokerDownscaleOnClusterUpscale = true
	want.BrokerDiskSizeGib = 200

	steps := []struct {
		mask  []string
		check func(c *mkdriver.Cluster) bool
	}{
		{[]string{"kafka_version"}, func(c *mkdriver.Cluster) bool { return c.KafkaVersion == "4.3.x" && c.TLS == nil }},
		{[]string{"tlsConfig.sslPrincipalMappingRules"}, func(c *mkdriver.Cluster) bool {
			return c.TLS != nil && c.TLS.SSLPrincipalMappingRules == "DEFAULT" && len(c.TLS.CAPools) == 0
		}},
		{[]string{"tls_config.trust_config.cas_configs"}, func(c *mkdriver.Cluster) bool {
			return len(c.TLS.CAPools) == 1 && c.TLS.SSLPrincipalMappingRules == "DEFAULT"
		}},
		{[]string{"updateOptions"}, func(c *mkdriver.Cluster) bool { return c.AllowBrokerDownscaleOnClusterUpscale }},
		{[]string{"brokerCapacityConfig.diskSizeGib"}, func(c *mkdriver.Cluster) bool { return c.BrokerDiskSizeGib == 200 }},
	}

	for _, s := range steps {
		got, _, err := m.UpdateCluster(ctx, want, s.mask)
		if err != nil || !s.check(got) {
			t.Fatalf("mask %v = %+v tls=%+v, %v", s.mask, got, got.TLS, err)
		}
	}

	// Clearing tlsConfig with an empty block, and resetting the version with "*",
	// which re-applies the 3.7.x default.
	clear := cluster("c1")

	got, _, err := m.UpdateCluster(ctx, clear, []string{"tlsConfig", "tlsConfig.trustConfig", "kafkaVersion"})
	if err != nil || got.TLS == nil || len(got.TLS.CAPools) != 0 || got.KafkaVersion != defaultKafkaVersion {
		t.Fatalf("clear = %+v tls=%+v, %v", got, got.TLS, err)
	}

	bad := cluster("c1")
	bad.BrokerDiskSizeGib = 10

	if _, _, err := m.UpdateCluster(ctx, bad, []string{"brokerCapacityConfig"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("broker disk 10 via mask: want INVALID_ARGUMENT, got %v", err)
	}
}

// TestGetOperationUnknownIsNotFound: the real API 404s an operation name it
// never issued; a created operation carries its metadata.
func TestGetOperationUnknownIsNotFound(t *testing.T) {
	m, clock := newMock(t)
	ctx := context.Background()

	if _, err := m.GetOperation(ctx, "projects/p/locations/us-central1/operations/nope"); !cerrors.IsNotFound(err) {
		t.Fatalf("unknown op: want NOT_FOUND, got %v", err)
	}

	_, op, err := m.CreateCluster(ctx, cluster("c1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := m.GetOperation(ctx, op.Name)
	if err != nil || !got.Done || got.Type != opCreate || got.APIVersion != apiVersion ||
		got.TargetName != clusterName(proj, region, "c1") || !got.CreateTime.Equal(clock.Now()) ||
		!got.EndTime.Equal(clock.Now()) {
		t.Fatalf("created op = %+v, %v", got, err)
	}
}

// TestOperationStoreIsBounded: the store keeps at most maxOperations, evicting
// the oldest first.
func TestOperationStoreIsBounded(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	mustCreate(t, m, "c1")

	first := ""

	for i := range maxOperations + 5 {
		c := cluster("c1")
		c.Labels = map[string]string{"i": fmt.Sprint(i)}

		_, op, err := m.UpdateCluster(ctx, c, []string{"labels"})
		if err != nil {
			t.Fatalf("update %d: %v", i, err)
		}

		if i == 0 {
			first = op.Name
		}
	}

	if n := m.operations.Len(); n != maxOperations {
		t.Fatalf("operation store = %d, want %d", n, maxOperations)
	}

	if _, err := m.GetOperation(ctx, first); !cerrors.IsNotFound(err) {
		t.Fatalf("oldest op should be evicted, got %v", err)
	}

	if opSeqOf("no-marker") != 0 || opSeqOf("x/operations/operation-zz-u") != 0 {
		t.Fatal("unparseable op names must sort first")
	}
}

// TestAsyncSettleCreatingThenActive: under --async-settle a new cluster reports
// CREATING for the settle window, then ACTIVE; without it, ACTIVE at once.
func TestAsyncSettleCreatingThenActive(t *testing.T) {
	clock := config.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithProjectID(proj), config.WithClock(clock), config.WithAsyncSettle()))
	ctx := context.Background()

	created, _, err := m.CreateCluster(ctx, cluster("s1"))
	if err != nil || created.State != stateCreating {
		t.Fatalf("create under async settle = %+v, %v; want CREATING", created, err)
	}

	if got, _ := m.GetCluster(ctx, proj, region, "s1"); got.State != stateCreating {
		t.Fatalf("get in window = %q, want CREATING", got.State)
	}

	if all, _ := m.ListClusters(ctx, proj, region); len(all) != 1 || all[0].State != stateCreating {
		t.Fatalf("list in window = %+v", all)
	}

	clock.Advance(settle.DefaultClusterSettle)

	if got, _ := m.GetCluster(ctx, proj, region, "s1"); got.State != stateActive {
		t.Fatalf("get after window = %q, want ACTIVE", got.State)
	}

	if _, err := m.DeleteCluster(ctx, proj, region, "s1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	sync, _ := newMock(t)
	if got, _, _ := sync.CreateCluster(ctx, cluster("s2")); got.State != stateActive {
		t.Fatalf("default create = %q, want ACTIVE", got.State)
	}
}

// TestTopicSurface covers list, get, update masks and delete, including the
// NOT_FOUND and INVALID_ARGUMENT paths.
func TestTopicSurface(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	if _, err := m.ListTopics(ctx, proj, region, "c1"); !cerrors.IsNotFound(err) {
		t.Fatalf("list under missing cluster: %v", err)
	}

	if err := m.DeleteTopic(ctx, proj, region, "c1", "t"); !cerrors.IsNotFound(err) {
		t.Fatalf("delete under missing cluster: %v", err)
	}

	if _, err := m.UpdateTopic(ctx, &mkdriver.Topic{Project: proj, Location: region, ClusterID: "c1", ID: "t"},
		[]string{"configs"}); !cerrors.IsNotFound(err) {
		t.Fatalf("update under missing cluster: %v", err)
	}

	if _, err := m.GetTopic(ctx, proj, region, "c1", "t"); !cerrors.IsNotFound(err) {
		t.Fatalf("get under missing cluster: %v", err)
	}

	mustCreate(t, m, "c1")
	mustCreate(t, m, "c2")

	mk := func(cluster, id string) *mkdriver.Topic {
		return &mkdriver.Topic{Project: proj, Location: region, ClusterID: cluster, ID: id, PartitionCount: 2, ReplicationFactor: 3}
	}

	for _, tp := range []*mkdriver.Topic{mk("c1", "b"), mk("c1", "a"), mk("c2", "z")} {
		if _, err := m.CreateTopic(ctx, tp); err != nil {
			t.Fatalf("create %s: %v", tp.ID, err)
		}
	}

	if _, err := m.CreateTopic(ctx, mk("c1", "a")); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate topic: %v", err)
	}

	for name, mutate := range map[string]func(*mkdriver.Topic){
		"zero partitions": func(tp *mkdriver.Topic) { tp.PartitionCount = 0 },
		"zero rf":         func(tp *mkdriver.Topic) { tp.ReplicationFactor = 0 },
	} {
		tp := mk("c1", "v")
		mutate(tp)

		if _, err := m.CreateTopic(ctx, tp); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("%s: %v", name, err)
		}
	}

	list, err := m.ListTopics(ctx, proj, region, "c1")
	if err != nil || len(list) != 2 || list[0].ID != "a" || list[1].ID != "b" {
		t.Fatalf("list = %+v, %v", list, err)
	}

	if _, err := m.GetTopic(ctx, proj, region, "c1", "nope"); !cerrors.IsNotFound(err) {
		t.Fatalf("get missing topic: %v", err)
	}

	withCfg := mk("c1", "a")
	withCfg.Configs = map[string]string{"cleanup.policy": "compact"}

	got, err := m.UpdateTopic(ctx, withCfg, []string{"configs"})
	if err != nil || got.Configs["cleanup.policy"] != "compact" {
		t.Fatalf("configs update = %+v, %v", got, err)
	}

	all := mk("c1", "a")
	all.PartitionCount = 5

	if got, err = m.UpdateTopic(ctx, all, []string{"*"}); err != nil || got.PartitionCount != 5 || got.Configs != nil {
		t.Fatalf("* update = %+v, %v", got, err)
	}

	for _, bad := range [][]string{nil, {"replication_factor"}, {"name"}, {"nope"}} {
		if _, err := m.UpdateTopic(ctx, mk("c1", "a"), bad); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("topic mask %v: %v", bad, err)
		}
	}

	if _, err := m.UpdateTopic(ctx, mk("c1", "ghost"), []string{"configs"}); !cerrors.IsNotFound(err) {
		t.Fatalf("update missing topic: %v", err)
	}

	zero := mk("c1", "b")
	zero.PartitionCount = 0

	if _, err := m.UpdateTopic(ctx, zero, []string{"partitionCount"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("shrink to zero: %v", err)
	}

	if err := m.DeleteTopic(ctx, proj, region, "c1", "a"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if err := m.DeleteTopic(ctx, proj, region, "c1", "a"); !cerrors.IsNotFound(err) {
		t.Fatalf("delete twice: %v", err)
	}

	if _, err := m.DeleteCluster(ctx, proj, region, "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("delete missing cluster: %v", err)
	}

	if _, err := m.GetCluster(ctx, proj, region, "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("get missing cluster: %v", err)
	}

	if _, _, err := m.CreateCluster(ctx, cluster("c1")); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate cluster: %v", err)
	}
}

// TestRestoreRejectsCorruptSnapshots covers the snapshot error paths.
func TestRestoreRejectsCorruptSnapshots(t *testing.T) {
	m, _ := newMock(t)
	ctx := context.Background()

	if err := m.Restore(ctx, []byte(`not json`)); err == nil {
		t.Fatal("restore of non-JSON must fail")
	}

	if err := m.Restore(ctx, []byte(`{"clusters":"not-a-map"}`)); err == nil {
		t.Fatal("restore of a corrupt store must fail")
	}

	if err := m.Restore(ctx, []byte(`{}`)); err != nil {
		t.Fatalf("restore of an empty snapshot: %v", err)
	}
}
