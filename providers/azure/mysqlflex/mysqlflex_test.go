package mysqlflex

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("eastus"),
		config.WithAccountID("sub-1"),
	)

	return New(opts)
}

func TestCreateInstance(t *testing.T) {
	tests := []struct {
		name      string
		cfg       rdsdriver.InstanceConfig
		expectErr bool
	}{
		{
			name: "default tier and storage",
			cfg:  rdsdriver.InstanceConfig{ID: "orders"},
		},
		{
			name: "explicit sku and storage",
			cfg: rdsdriver.InstanceConfig{
				ID:               "analytics",
				InstanceClass:    "Standard_D2ds_v4",
				AllocatedStorage: 200,
				StorageType:      "Premium_LRS",
				EngineVersion:    "8.0.21",
			},
		},
		{
			name:      "missing identifier",
			cfg:       rdsdriver.InstanceConfig{},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			inst, err := m.CreateInstance(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, tc.cfg.ID, inst.ID)
			assertEqual(t, "available", inst.State)
			assertEqual(t, defaultEngine, inst.Engine)
			assertEqual(t, defaultPort, inst.Port)
			assertNotEmpty(t, inst.ARN)

			if !strings.HasSuffix(inst.Endpoint, endpointSuffix) {
				t.Errorf("expected endpoint suffix %q, got %q", endpointSuffix, inst.Endpoint)
			}
		})
	}
}

func TestDuplicateCreate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "dup"})
	requireNoError(t, err)

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "dup"}); err == nil {
		t.Fatal("expected AlreadyExists on duplicate create")
	}
}

func TestInstanceLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1"})
	requireNoError(t, err)

	requireNoError(t, m.StopInstance(ctx, "db1"))

	insts, err := m.DescribeInstances(ctx, []string{"db1"})
	requireNoError(t, err)
	assertEqual(t, "stopped", insts[0].State)

	// Idempotent stop.
	requireNoError(t, m.StopInstance(ctx, "db1"))

	requireNoError(t, m.StartInstance(ctx, "db1"))
	requireNoError(t, m.RebootInstance(ctx, "db1"))

	requireNoError(t, m.DeleteInstance(ctx, "db1"))

	if _, err := m.DescribeInstances(ctx, []string{"db1"}); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestRebootRequiresAvailable(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1"})
	requireNoError(t, err)

	requireNoError(t, m.StopInstance(ctx, "db1"))

	if err := m.RebootInstance(ctx, "db1"); err == nil {
		t.Fatal("expected reboot to fail on stopped instance")
	}
}

func TestModifyInstance(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db1"})
	requireNoError(t, err)

	updated, err := m.ModifyInstance(ctx, "db1", rdsdriver.ModifyInstanceInput{
		InstanceClass:    "Standard_D4ds_v4",
		AllocatedStorage: 500,
		EngineVersion:    "8.0.21",
		Tags:             map[string]string{"env": "prod"},
	})
	requireNoError(t, err)

	assertEqual(t, "Standard_D4ds_v4", updated.InstanceClass)
	assertEqual(t, 500, updated.AllocatedStorage)
	assertEqual(t, "8.0.21", updated.EngineVersion)
	assertEqual(t, "prod", updated.Tags["env"])
}

func TestSnapshotAndRestore(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID:               "src",
		AllocatedStorage: 100,
		EngineVersion:    "8.0.21",
	})
	requireNoError(t, err)

	snap, err := m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{ID: "snap1", InstanceID: "src"})
	requireNoError(t, err)

	assertEqual(t, "available", snap.State)
	assertEqual(t, 100, snap.AllocatedStorage)

	restored, err := m.RestoreInstanceFromSnapshot(ctx, rdsdriver.RestoreInstanceInput{
		NewInstanceID: "restored",
		SnapshotID:    "snap1",
	})
	requireNoError(t, err)
	assertEqual(t, "restored", restored.ID)
	assertEqual(t, 100, restored.AllocatedStorage)
	assertEqual(t, defaultPort, restored.Port)

	requireNoError(t, m.DeleteSnapshot(ctx, "snap1"))
}

func TestClusterOpsUnsupported(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "c"}); err == nil {
		t.Fatal("CreateCluster should be unsupported on MySQL Flex")
	}

	clusters, err := m.DescribeClusters(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 0, len(clusters))

	if err := m.StartCluster(ctx, "c"); err == nil {
		t.Fatal("StartCluster should be unsupported on MySQL Flex")
	}

	if err := m.StopCluster(ctx, "c"); err == nil {
		t.Fatal("StopCluster should be unsupported on MySQL Flex")
	}

	if err := m.DeleteCluster(ctx, "c"); err == nil {
		t.Fatal("DeleteCluster should be unsupported on MySQL Flex")
	}

	if _, err := m.ModifyCluster(ctx, "c", rdsdriver.ModifyInstanceInput{}); err == nil {
		t.Fatal("ModifyCluster should be unsupported on MySQL Flex")
	}

	if _, err := m.CreateClusterSnapshot(ctx, rdsdriver.ClusterSnapshotConfig{ID: "s", ClusterID: "c"}); err == nil {
		t.Fatal("CreateClusterSnapshot should be unsupported on MySQL Flex")
	}

	csnaps, err := m.DescribeClusterSnapshots(ctx, nil, "")
	requireNoError(t, err)
	assertEqual(t, 0, len(csnaps))

	if err := m.DeleteClusterSnapshot(ctx, "s"); err == nil {
		t.Fatal("DeleteClusterSnapshot should be unsupported on MySQL Flex")
	}

	if _, err := m.RestoreClusterFromSnapshot(ctx, rdsdriver.RestoreClusterInput{}); err == nil {
		t.Fatal("RestoreClusterFromSnapshot should be unsupported on MySQL Flex")
	}
}

func TestDescribeInstancesUnknown(t *testing.T) {
	m := newTestMock()

	if _, err := m.DescribeInstances(context.Background(), []string{"nope"}); err == nil {
		t.Fatal("expected NotFound on unknown id")
	}
}

// Hand-rolled helpers per CLAUDE.md (provider tests don't use testify).

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()

	switch {
	case expectErr && err == nil:
		t.Fatal("expected error, got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

func assertNotEmpty(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		t.Error("expected non-empty string")
	}
}

func TestSubResourcesRequireServer(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "ghost", Name: "db"}); err == nil {
		t.Error("CreateDatabase on missing server: expected error")
	}

	if _, err := m.CreateFirewallRule(ctx, rdsdriver.FirewallRuleConfig{Server: "ghost", Name: "r"}); err == nil {
		t.Error("CreateFirewallRule on missing server: expected error")
	}

	if _, err := m.SetConfiguration(ctx, rdsdriver.ConfigurationConfig{Server: "ghost", Name: "max_connections", Value: "100"}); err == nil {
		t.Error("SetConfiguration on missing server: expected error")
	}
}

func TestDatabaseLifecycleAndCascade(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv", Name: "app"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv", Name: "app"}); err == nil {
		t.Error("duplicate database: expected AlreadyExists")
	}

	if _, err := m.CreateFirewallRule(ctx, rdsdriver.FirewallRuleConfig{Server: "srv", Name: "r", StartIPAddress: "10.0.0.1", EndIPAddress: "10.0.0.9"}); err != nil {
		t.Fatalf("CreateFirewallRule: %v", err)
	}

	if _, err := m.SetConfiguration(ctx, rdsdriver.ConfigurationConfig{Server: "srv", Name: "max_connections", Value: "100"}); err != nil {
		t.Fatalf("SetConfiguration: %v", err)
	}

	// Deleting the server cascades to its children.
	if err := m.DeleteInstance(ctx, "srv"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if _, err := m.ListDatabases(ctx, "srv"); err == nil {
		t.Error("ListDatabases after server delete: expected server NotFound")
	}
}

func TestSetConfigurationValidatesParameter(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Unknown parameter names and empty values are both rejected (real Azure
	// 404s an unknown server parameter).
	if _, err := m.SetConfiguration(ctx, rdsdriver.ConfigurationConfig{Server: "srv", Name: "not_a_real_param", Value: "1"}); err == nil {
		t.Error("SetConfiguration with unknown parameter: expected NotFound")
	}

	if _, err := m.SetConfiguration(ctx, rdsdriver.ConfigurationConfig{Server: "srv", Name: "max_connections", Value: ""}); err == nil {
		t.Error("SetConfiguration with empty value: expected InvalidArgument")
	}

	if _, err := m.SetConfiguration(ctx, rdsdriver.ConfigurationConfig{Server: "srv", Name: "max_connections", Value: "200"}); err != nil {
		t.Errorf("SetConfiguration with known parameter: %v", err)
	}

	// A known-but-unset parameter returns its catalog default, not NotFound.
	def, err := m.GetConfiguration(ctx, "srv", "wait_timeout")
	if err != nil {
		t.Fatalf("GetConfiguration for unset known param: %v", err)
	}

	if def.Source != "system-default" || def.Value == "" {
		t.Errorf("expected catalog default for wait_timeout, got %+v", def)
	}

	// An unknown parameter still 404s.
	if _, err := m.GetConfiguration(ctx, "srv", "not_a_real_param"); err == nil {
		t.Error("GetConfiguration for unknown param: expected NotFound")
	}
}

func TestFailoverRequiresRunning(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := m.FailoverInstance(ctx, "srv"); err != nil {
		t.Fatalf("FailoverInstance on running server: %v", err)
	}

	if err := m.StopInstance(ctx, "srv"); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}

	if err := m.FailoverInstance(ctx, "srv"); err == nil {
		t.Error("FailoverInstance on stopped server: expected FailedPrecondition")
	}

	if err := m.FailoverInstance(ctx, "ghost"); err == nil {
		t.Error("FailoverInstance on missing server: expected NotFound")
	}
}

func TestSubResourceCRUDCoverage(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Databases: get + delete.
	if _, err := m.CreateDatabase(ctx, rdsdriver.DatabaseConfig{Server: "srv", Name: "app"}); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	if got, err := m.GetDatabase(ctx, "srv", "app"); err != nil || got.Name != "app" {
		t.Fatalf("GetDatabase: %+v %v", got, err)
	}

	if err := m.DeleteDatabase(ctx, "srv", "app"); err != nil {
		t.Fatalf("DeleteDatabase: %v", err)
	}

	if err := m.DeleteDatabase(ctx, "srv", "app"); err == nil {
		t.Error("DeleteDatabase again: expected NotFound")
	}

	// Firewall rules: get, list, delete.
	if _, err := m.CreateFirewallRule(ctx, rdsdriver.FirewallRuleConfig{Server: "srv", Name: "r", StartIPAddress: "10.0.0.1", EndIPAddress: "10.0.0.9"}); err != nil {
		t.Fatalf("CreateFirewallRule: %v", err)
	}

	if got, err := m.GetFirewallRule(ctx, "srv", "r"); err != nil || got.EndIPAddress != "10.0.0.9" {
		t.Fatalf("GetFirewallRule: %+v %v", got, err)
	}

	if rs, err := m.ListFirewallRules(ctx, "srv"); err != nil || len(rs) != 1 {
		t.Fatalf("ListFirewallRules: %d %v", len(rs), err)
	}

	if err := m.DeleteFirewallRule(ctx, "srv", "r"); err != nil {
		t.Fatalf("DeleteFirewallRule: %v", err)
	}

	if err := m.DeleteFirewallRule(ctx, "srv", "r"); err == nil {
		t.Error("DeleteFirewallRule again: expected NotFound")
	}

	// Configurations: set (known param), get, list.
	if _, err := m.SetConfiguration(ctx, rdsdriver.ConfigurationConfig{Server: "srv", Name: "max_connections", Value: "100"}); err != nil {
		t.Fatalf("SetConfiguration: %v", err)
	}

	if got, err := m.GetConfiguration(ctx, "srv", "max_connections"); err != nil || got.Value != "100" {
		t.Fatalf("GetConfiguration: %+v %v", got, err)
	}

	// List returns the full catalog (with the override applied), not just the
	// single written parameter.
	if cs, err := m.ListConfigurations(ctx, "srv"); err != nil || len(cs) < 2 {
		t.Fatalf("ListConfigurations: %d %v", len(cs), err)
	}
}

func TestBatchSetConfigurationsIsAtomic(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "srv"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// A batch with a good entry followed by an unknown-parameter entry must
	// apply nothing (the good entry keeps its catalog default).
	_, err := m.BatchSetConfigurations(ctx, "srv", []rdsdriver.ConfigurationConfig{
		{Name: "max_connections", Value: "500"},
		{Name: "not_a_real_param", Value: "x"},
	})
	if err == nil {
		t.Fatal("BatchSetConfigurations with a bad entry: expected error")
	}

	got, err := m.GetConfiguration(ctx, "srv", "max_connections")
	if err != nil {
		t.Fatalf("GetConfiguration: %v", err)
	}

	if got.Source != "system-default" {
		t.Errorf("batch was not atomic: max_connections was persisted as %q (source %q)", got.Value, got.Source)
	}

	// A fully-valid batch applies all entries.
	if _, err := m.BatchSetConfigurations(ctx, "srv", []rdsdriver.ConfigurationConfig{
		{Name: "max_connections", Value: "500"},
		{Name: "slow_query_log", Value: "ON"},
	}); err != nil {
		t.Fatalf("BatchSetConfigurations (valid): %v", err)
	}

	mc, _ := m.GetConfiguration(ctx, "srv", "max_connections")
	sl, _ := m.GetConfiguration(ctx, "srv", "slow_query_log")
	if mc.Value != "500" || sl.Value != "ON" {
		t.Errorf("valid batch not applied: max_connections=%q slow_query_log=%q", mc.Value, sl.Value)
	}
}
