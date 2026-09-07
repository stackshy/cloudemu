package redisenterprise_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/redisenterprise"
)

func newMock() *redisenterprise.Mock {
	return redisenterprise.New(config.NewOptions())
}

func iptr(v int) *int       { return &v }
func sptr(v string) *string { return &v }

func standardCluster() *redisenterprise.ClusterInput {
	return &redisenterprise.ClusterInput{
		Tags:  map[string]string{"env": "dev"},
		Sku:   &redisenterprise.Sku{Name: "Enterprise_E10", Capacity: iptr(2)},
		Zones: []string{"1", "2", "3"},
	}
}

func createCluster(t *testing.T, m *redisenterprise.Mock) redisenterprise.Cluster {
	t.Helper()

	c, isNew, err := m.CreateOrUpdateCluster(context.Background(), "sub", "rg", "cache1", "West US", standardCluster())
	if err != nil || !isNew {
		t.Fatalf("create cluster: err=%v isNew=%v", err, isNew)
	}

	return c
}

func TestCreateClusterComputedFields(t *testing.T) {
	m := newMock()
	c := createCluster(t, m)

	if c.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", c.ProvisioningState)
	}

	if c.ResourceState != "Running" {
		t.Errorf("resourceState = %q, want Running", c.ResourceState)
	}

	if c.HostName != "cache1.westus.redisenterprise.cache.azure.net" {
		t.Errorf("hostName = %q, want cache1.westus.redisenterprise.cache.azure.net", c.HostName)
	}

	if c.RedisVersion == "" {
		t.Errorf("redisVersion is empty, want a version")
	}

	if c.MinimumTLSVersion != "1.2" {
		t.Errorf("minimumTlsVersion = %q, want 1.2", c.MinimumTLSVersion)
	}

	if c.Sku == nil || c.Sku.Name != "Enterprise_E10" || c.Sku.Capacity == nil || *c.Sku.Capacity != 2 {
		t.Errorf("sku = %+v, want Enterprise_E10 capacity 2", c.Sku)
	}

	if len(c.Zones) != 3 {
		t.Errorf("zones = %v, want 3", c.Zones)
	}
}

func TestClusterHostnameStableAcrossReads(t *testing.T) {
	m := newMock()
	createCluster(t, m)

	first, err := m.GetCluster(context.Background(), "sub", "rg", "cache1")
	if err != nil {
		t.Fatalf("get1: %v", err)
	}

	second, err := m.GetCluster(context.Background(), "sub", "rg", "cache1")
	if err != nil {
		t.Fatalf("get2: %v", err)
	}

	if first.HostName != second.HostName || first.ProvisioningState != second.ProvisioningState ||
		first.ResourceState != second.ResourceState || first.RedisVersion != second.RedisVersion {
		t.Errorf("computed cluster fields drifted: %+v vs %+v", first, second)
	}
}

func TestClusterPatchMergesAndLocationImmutable(t *testing.T) {
	m := newMock()
	createCluster(t, m)

	// PATCH-style: only tags supplied; sku and zones preserved.
	patch := &redisenterprise.ClusterInput{Tags: map[string]string{"env": "prod"}}

	c, isNew, err := m.CreateOrUpdateCluster(context.Background(), "sub", "rg", "cache1", "East US", patch)
	if err != nil || isNew {
		t.Fatalf("update: err=%v isNew=%v", err, isNew)
	}

	if c.Location != "West US" {
		t.Errorf("location = %q, want West US (immutable)", c.Location)
	}

	if c.Sku == nil || c.Sku.Name != "Enterprise_E10" {
		t.Errorf("sku after patch = %+v, want preserved", c.Sku)
	}

	if len(c.Zones) != 3 {
		t.Errorf("zones after patch = %v, want preserved", c.Zones)
	}

	if c.Tags["env"] != "prod" {
		t.Errorf("tags after patch = %v, want env=prod", c.Tags)
	}

	if c.HostName != "cache1.westus.redisenterprise.cache.azure.net" {
		t.Errorf("hostName drifted after patch: %q", c.HostName)
	}
}

func TestCreateClusterRequiresSku(t *testing.T) {
	m := newMock()

	_, _, err := m.CreateOrUpdateCluster(context.Background(), "sub", "rg", "cache1", "West US",
		&redisenterprise.ClusterInput{})
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("missing sku: err=%v, want InvalidArgument", err)
	}
}

func TestSystemAssignedIdentityMintsIDs(t *testing.T) {
	m := newMock()

	in := standardCluster()
	in.Identity = &redisenterprise.Identity{Type: "SystemAssigned"}

	c, _, err := m.CreateOrUpdateCluster(context.Background(), "sub", "rg", "cache1", "West US", in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if c.Identity == nil || c.Identity.PrincipalID == "" || c.Identity.TenantID == "" {
		t.Errorf("identity ids not minted: %+v", c.Identity)
	}
}

func standardDatabase() *redisenterprise.DatabaseInput {
	return &redisenterprise.DatabaseInput{
		ClientProtocol:   sptr("Encrypted"),
		ClusteringPolicy: sptr("EnterpriseCluster"),
		Modules: []redisenterprise.Module{
			{Name: "RediSearch", Args: "PARTITIONS 4"},
		},
	}
}

func createDatabase(t *testing.T, m *redisenterprise.Mock) redisenterprise.Database {
	t.Helper()

	d, isNew, err := m.CreateOrUpdateDatabase(
		context.Background(), "sub", "rg", "cache1", "default", standardDatabase())
	if err != nil || !isNew {
		t.Fatalf("create database: err=%v isNew=%v", err, isNew)
	}

	return d
}

func TestDatabaseCreateComputedFieldsAndDefaults(t *testing.T) {
	m := newMock()
	createCluster(t, m)

	d := createDatabase(t, m)

	if d.ProvisioningState != "Succeeded" || d.ResourceState != "Running" {
		t.Errorf("states = %q/%q, want Succeeded/Running", d.ProvisioningState, d.ResourceState)
	}

	if d.PrimaryKey == "" || d.SecondaryKey == "" || d.PrimaryKey == d.SecondaryKey {
		t.Errorf("keys not minted distinctly: %q / %q", d.PrimaryKey, d.SecondaryKey)
	}

	if d.Port == nil || *d.Port != 10000 {
		t.Errorf("port = %v, want default 10000", d.Port)
	}

	if d.ClientProtocol != "Encrypted" || d.ClusteringPolicy != "EnterpriseCluster" {
		t.Errorf("protocol/policy = %q/%q", d.ClientProtocol, d.ClusteringPolicy)
	}

	if d.EvictionPolicy != "VolatileLRU" {
		t.Errorf("evictionPolicy = %q, want default VolatileLRU", d.EvictionPolicy)
	}

	if len(d.Modules) != 1 || d.Modules[0].Name != "RediSearch" || d.Modules[0].Version == "" {
		t.Errorf("modules = %+v, want RediSearch with a version", d.Modules)
	}

	if d.Modules[0].Args != "PARTITIONS 4" {
		t.Errorf("module args = %q, want round-tripped verbatim", d.Modules[0].Args)
	}
}

func TestDatabaseKeysStableAcrossReads(t *testing.T) {
	m := newMock()
	createCluster(t, m)
	created := createDatabase(t, m)

	got, err := m.GetDatabase(context.Background(), "sub", "rg", "cache1", "default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.PrimaryKey != created.PrimaryKey || got.SecondaryKey != created.SecondaryKey {
		t.Errorf("keys drifted: created %q/%q vs get %q/%q",
			created.PrimaryKey, created.SecondaryKey, got.PrimaryKey, got.SecondaryKey)
	}

	// PATCH must preserve keys.
	patched, _, err := m.CreateOrUpdateDatabase(context.Background(), "sub", "rg", "cache1", "default",
		&redisenterprise.DatabaseInput{EvictionPolicy: sptr("NoEviction")})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	if patched.PrimaryKey != created.PrimaryKey {
		t.Errorf("key changed on patch: %q vs %q", patched.PrimaryKey, created.PrimaryKey)
	}

	if patched.EvictionPolicy != "NoEviction" {
		t.Errorf("evictionPolicy after patch = %q, want NoEviction", patched.EvictionPolicy)
	}

	// PATCH preserved the clustering policy set at create.
	if patched.ClusteringPolicy != "EnterpriseCluster" {
		t.Errorf("clusteringPolicy after patch = %q, want preserved", patched.ClusteringPolicy)
	}
}

func TestDatabaseCreateRequiresParentCluster(t *testing.T) {
	m := newMock()

	_, _, err := m.CreateOrUpdateDatabase(context.Background(), "sub", "rg", "ghost", "default", standardDatabase())
	if !cerrors.IsNotFound(err) {
		t.Errorf("missing parent: err=%v, want NotFound", err)
	}
}

func TestDeleteClusterCascadesDatabases(t *testing.T) {
	m := newMock()
	createCluster(t, m)
	createDatabase(t, m)

	existed, err := m.DeleteCluster(context.Background(), "sub", "rg", "cache1")
	if err != nil || !existed {
		t.Fatalf("delete cluster: err=%v existed=%v", err, existed)
	}

	if _, err := m.GetDatabase(context.Background(), "sub", "rg", "cache1", "default"); !cerrors.IsNotFound(err) {
		t.Errorf("database survived cluster delete: err=%v", err)
	}
}

func TestDatabaseARMID(t *testing.T) {
	m := newMock()
	createCluster(t, m)
	d := createDatabase(t, m)

	want := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Cache/redisEnterprise/cache1/databases/default"
	if d.ARMID() != want {
		t.Errorf("database ARMID = %q, want %q", d.ARMID(), want)
	}
}

func TestListClustersAndDatabases(t *testing.T) {
	m := newMock()
	createCluster(t, m)
	createDatabase(t, m)

	if _, _, err := m.CreateOrUpdateDatabase(
		context.Background(), "sub", "rg", "cache1", "db2", standardDatabase()); err != nil {
		t.Fatalf("create db2: %v", err)
	}

	clusters, err := m.ListClustersByResourceGroup(context.Background(), "sub", "rg")
	if err != nil || len(clusters) != 1 {
		t.Fatalf("list clusters: err=%v n=%d", err, len(clusters))
	}

	dbs, err := m.ListDatabasesByCluster(context.Background(), "sub", "rg", "cache1")
	if err != nil || len(dbs) != 2 {
		t.Fatalf("list databases: err=%v n=%d", err, len(dbs))
	}

	subList, err := m.ListClustersBySubscription(context.Background(), "sub")
	if err != nil || len(subList) != 1 {
		t.Fatalf("list by sub: err=%v n=%d", err, len(subList))
	}
}

func TestPurgeResourceGroupCascades(t *testing.T) {
	m := newMock()
	createCluster(t, m)
	createDatabase(t, m)

	if err := m.PurgeResourceGroup(context.Background(), "sub", "rg"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	clusters, _ := m.ListClustersByResourceGroup(context.Background(), "sub", "rg")
	if len(clusters) != 0 {
		t.Errorf("clusters after purge = %d, want 0", len(clusters))
	}

	dbs, _ := m.ListDatabasesByCluster(context.Background(), "sub", "rg", "cache1")
	if len(dbs) != 0 {
		t.Errorf("databases after purge = %d, want 0", len(dbs))
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := newMock()
	createCluster(t, m)
	created := createDatabase(t, m)

	data, err := m.Snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored := newMock()
	if err := restored.Restore(context.Background(), data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	c, err := restored.GetCluster(context.Background(), "sub", "rg", "cache1")
	if err != nil {
		t.Fatalf("get cluster after restore: %v", err)
	}

	if c.HostName != "cache1.westus.redisenterprise.cache.azure.net" {
		t.Errorf("restored cluster lost hostName: %q", c.HostName)
	}

	d, err := restored.GetDatabase(context.Background(), "sub", "rg", "cache1", "default")
	if err != nil {
		t.Fatalf("get database after restore: %v", err)
	}

	if d.PrimaryKey != created.PrimaryKey {
		t.Errorf("restored database lost key: %q vs %q", d.PrimaryKey, created.PrimaryKey)
	}
}
