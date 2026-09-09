package mongocluster_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/mongocluster"
)

func newMock() *mongocluster.Mock {
	return mongocluster.New(config.NewOptions())
}

func iptr64(v int64) *int64 { return &v }
func iptr32(v int32) *int32 { return &v }
func sptr(v string) *string { return &v }

func standardInput() *mongocluster.ClusterInput {
	return &mongocluster.ClusterInput{
		Tags:                  map[string]string{"env": "dev"},
		AdministratorUserName: sptr("mongoAdmin"),
		AdministratorPassword: sptr("Sup3rSecret!"),
		ServerVersion:         sptr("7.0"),
		Compute:               &mongocluster.Compute{Tier: "M30"},
		Storage:               &mongocluster.Storage{SizeGb: iptr64(128)},
		Sharding:              &mongocluster.Sharding{ShardCount: iptr32(1)},
		HighAvailability:      &mongocluster.HighAvailability{TargetMode: "SameZone"},
	}
}

func createCluster(t *testing.T, m *mongocluster.Mock) mongocluster.Cluster {
	t.Helper()

	c, isNew, err := m.CreateOrUpdateCluster(context.Background(), "sub", "rg", "mongo1", "West US 2", standardInput())
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

	if c.ClusterStatus != "Ready" {
		t.Errorf("clusterStatus = %q, want Ready", c.ClusterStatus)
	}

	want := "mongodb+srv://<user>:<password>@mongo1.mongocluster.cosmos.azure.com/" +
		"?tls=true&authMechanism=SCRAM-SHA-256&retrywrites=false&maxIdleTimeMS=120000"
	if c.ConnectionString != want {
		t.Errorf("connectionString = %q, want %q", c.ConnectionString, want)
	}

	if c.AdministratorUserName != "mongoAdmin" {
		t.Errorf("administratorUserName = %q, want mongoAdmin", c.AdministratorUserName)
	}

	if c.ServerVersion != "7.0" {
		t.Errorf("serverVersion = %q, want 7.0", c.ServerVersion)
	}

	if c.Compute == nil || c.Compute.Tier != "M30" {
		t.Errorf("compute = %+v, want tier M30", c.Compute)
	}

	if c.Storage == nil || c.Storage.SizeGb == nil || *c.Storage.SizeGb != 128 {
		t.Errorf("storage = %+v, want sizeGb 128", c.Storage)
	}

	if c.Sharding == nil || c.Sharding.ShardCount == nil || *c.Sharding.ShardCount != 1 {
		t.Errorf("sharding = %+v, want shardCount 1", c.Sharding)
	}

	if c.HighAvailability == nil || c.HighAvailability.TargetMode != "SameZone" {
		t.Errorf("highAvailability = %+v, want SameZone", c.HighAvailability)
	}
}

func TestCreateClusterDefaults(t *testing.T) {
	m := newMock()

	c, _, err := m.CreateOrUpdateCluster(context.Background(), "sub", "rg", "bare", "eastus", &mongocluster.ClusterInput{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if c.ServerVersion != "7.0" {
		t.Errorf("serverVersion default = %q, want 7.0", c.ServerVersion)
	}

	if c.CreateMode != "Default" {
		t.Errorf("createMode default = %q, want Default", c.CreateMode)
	}

	if c.PublicNetworkAccess != "Enabled" {
		t.Errorf("publicNetworkAccess default = %q, want Enabled", c.PublicNetworkAccess)
	}

	if c.HighAvailability == nil || c.HighAvailability.TargetMode != "Disabled" {
		t.Errorf("highAvailability default = %+v, want Disabled", c.HighAvailability)
	}
}

func TestComputedFieldsStableAcrossReads(t *testing.T) {
	m := newMock()
	createCluster(t, m)

	first, err := m.GetCluster(context.Background(), "sub", "rg", "mongo1")
	if err != nil {
		t.Fatalf("get1: %v", err)
	}

	second, err := m.GetCluster(context.Background(), "sub", "rg", "mongo1")
	if err != nil {
		t.Fatalf("get2: %v", err)
	}

	if first.ConnectionString != second.ConnectionString ||
		first.ProvisioningState != second.ProvisioningState ||
		first.ClusterStatus != second.ClusterStatus {
		t.Errorf("computed fields drifted: %+v vs %+v", first, second)
	}
}

func TestConnectionStringStableAcrossPatch(t *testing.T) {
	m := newMock()
	created := createCluster(t, m)

	// A tags/storage-only PATCH must not disturb the minted connection string.
	patched, _, err := m.CreateOrUpdateCluster(context.Background(), "sub", "rg", "mongo1", "West US 2",
		&mongocluster.ClusterInput{
			Tags:    map[string]string{"env": "prod"},
			Storage: &mongocluster.Storage{SizeGb: iptr64(256)},
		})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}

	if patched.ConnectionString != created.ConnectionString {
		t.Errorf("connectionString drifted on patch: %q vs %q", patched.ConnectionString, created.ConnectionString)
	}

	if patched.Storage == nil || *patched.Storage.SizeGb != 256 {
		t.Errorf("storage not updated: %+v", patched.Storage)
	}

	// Merge semantics: the untouched serverVersion and admin user must survive.
	if patched.ServerVersion != "7.0" || patched.AdministratorUserName != "mongoAdmin" {
		t.Errorf("patch did not merge: version=%q user=%q", patched.ServerVersion, patched.AdministratorUserName)
	}

	if patched.Tags["env"] != "prod" {
		t.Errorf("tags not replaced: %+v", patched.Tags)
	}
}

func TestLocationImmutableOnUpdate(t *testing.T) {
	m := newMock()
	createCluster(t, m)

	// CreateOrUpdate preserves the stored location; the handler passes the existing
	// location on PATCH, so the driver never changes it.
	updated, _, err := m.CreateOrUpdateCluster(context.Background(), "sub", "rg", "mongo1", "West US 2",
		&mongocluster.ClusterInput{ServerVersion: sptr("8.0")})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if updated.Location != "West US 2" {
		t.Errorf("location = %q, want West US 2", updated.Location)
	}

	if updated.ServerVersion != "8.0" {
		t.Errorf("serverVersion = %q, want 8.0", updated.ServerVersion)
	}
}

func TestListConnectionStringsStable(t *testing.T) {
	m := newMock()
	createCluster(t, m)

	first, err := m.ListConnectionStrings(context.Background(), "sub", "rg", "mongo1")
	if err != nil {
		t.Fatalf("list1: %v", err)
	}

	second, err := m.ListConnectionStrings(context.Background(), "sub", "rg", "mongo1")
	if err != nil {
		t.Fatalf("list2: %v", err)
	}

	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("want 1 connection string each, got %d and %d", len(first), len(second))
	}

	if first[0].ConnectionString != second[0].ConnectionString {
		t.Errorf("connection string drifted: %q vs %q", first[0].ConnectionString, second[0].ConnectionString)
	}

	if first[0].Name != "default" {
		t.Errorf("name = %q, want default", first[0].Name)
	}
}

func TestListConnectionStringsMissing(t *testing.T) {
	m := newMock()

	_, err := m.ListConnectionStrings(context.Background(), "sub", "rg", "nope")
	if !cerrors.IsNotFound(err) {
		t.Errorf("want NotFound, got %v", err)
	}
}

func TestGetMissing(t *testing.T) {
	m := newMock()

	_, err := m.GetCluster(context.Background(), "sub", "rg", "nope")
	if !cerrors.IsNotFound(err) {
		t.Errorf("want NotFound, got %v", err)
	}
}

func TestDeleteIdempotent(t *testing.T) {
	m := newMock()
	createCluster(t, m)

	existed, err := m.DeleteCluster(context.Background(), "sub", "rg", "mongo1")
	if err != nil || !existed {
		t.Fatalf("first delete: existed=%v err=%v", existed, err)
	}

	existed, err = m.DeleteCluster(context.Background(), "sub", "rg", "mongo1")
	if err != nil || existed {
		t.Fatalf("second delete: existed=%v err=%v", existed, err)
	}
}

func TestListByResourceGroupAndSubscription(t *testing.T) {
	m := newMock()
	createCluster(t, m)

	if _, _, err := m.CreateOrUpdateCluster(context.Background(), "sub", "rg2", "mongo2", "eastus",
		&mongocluster.ClusterInput{}); err != nil {
		t.Fatalf("create mongo2: %v", err)
	}

	byRG, err := m.ListClustersByResourceGroup(context.Background(), "sub", "rg")
	if err != nil || len(byRG) != 1 || byRG[0].Name != "mongo1" {
		t.Fatalf("list by rg = %+v err=%v", byRG, err)
	}

	bySub, err := m.ListClustersBySubscription(context.Background(), "sub")
	if err != nil || len(bySub) != 2 {
		t.Fatalf("list by sub = %+v err=%v", bySub, err)
	}
}

func TestPurgeResourceGroup(t *testing.T) {
	m := newMock()
	createCluster(t, m)

	if err := m.PurgeResourceGroup(context.Background(), "sub", "rg"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if _, err := m.GetCluster(context.Background(), "sub", "rg", "mongo1"); !cerrors.IsNotFound(err) {
		t.Errorf("cluster survived purge: %v", err)
	}
}

func TestPasswordNotStoredOnCluster(t *testing.T) {
	m := newMock()
	c := createCluster(t, m)

	// The Cluster value carries no administrator-password field at all; the write-
	// only secret must never surface on the stored/returned resource.
	if c.ConnectionString == "" {
		t.Fatal("connection string missing")
	}

	// Deterministic connection string must not embed the real secret.
	if strings.Contains(c.ConnectionString, "Sup3rSecret") {
		t.Errorf("connection string leaked the password: %q", c.ConnectionString)
	}
}
