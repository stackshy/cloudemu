package rds

import (
	"context"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// TestParameterGroupFamily verifies the family real RDS derives from an
// engine's version (DescribeDBEngineVersions.DBParameterGroupFamily).
func TestParameterGroupFamily(t *testing.T) {
	tests := []struct {
		engine, version, want string
	}{
		{"postgres", "15.4", "postgres15"},
		{"postgres", "16.2", "postgres16"},
		{"postgres", "9.6.24", "postgres9.6"},
		{"aurora-postgresql", "16.1", "aurora-postgresql16"},
		{"mysql", "8.0.36", "mysql8.0"},
		{"mysql", "5.7.44", "mysql5.7"},
		{"mariadb", "10.11.6", "mariadb10.11"},
		{"aurora-mysql", "8.0.mysql_aurora.3.05.2", "aurora-mysql8.0"},
		{"aurora-mysql", "5.7.mysql_aurora.2.11.2", "aurora-mysql5.7"},
		{"docdb", "5.0.0", "docdb5.0"},
		{"neptune", "1.3.1.0", "neptune1.3"},
		{"neptune", "1.1.1.0", "neptune1"},
		{"oracle-ee", "19.0.0.0.ru-2024-01.rur-2024-01.r1", "oracle-ee-19"},
		{"oracle-se2-cdb", "21.0.0.0.ru-2024-01.rur-2024-01.r1", "oracle-se2-cdb-21"},
		{"sqlserver-ee", "15.00.4365.2.v1", "sqlserver-ee-15.0"},
		{"sqlserver-web", "16.00.4095.4.v1", "sqlserver-web-16.0"},
		{"db2-ae", "11.5.9.0.sb00000000.r1", "db2-ae"},
		{"db2-se", "", "db2-se"},
		{"postgres", "", "postgres15"},
		{"unknown", "1.0", ""},
		{"postgres", "garbage", ""},
	}

	for _, tc := range tests {
		t.Run(tc.engine+"/"+tc.version, func(t *testing.T) {
			assertEqual(t, tc.want, parameterGroupFamily(tc.engine, tc.version))
		})
	}
}

// TestDescribeEngineVersionsFamilyPerVersion verifies each catalog version
// reports its own family, not one family per engine.
func TestDescribeEngineVersionsFamilyPerVersion(t *testing.T) {
	m := newTestMock()

	got, err := m.DescribeDBEngineVersions(context.Background(), "postgres", "")
	requireNoError(t, err)

	families := map[string]string{}
	for _, v := range got {
		families[v.EngineVersion] = v.DBParameterGroupFamily
	}

	assertEqual(t, "postgres15", families["15.4"])
	assertEqual(t, "postgres16", families["16.2"])
}

// TestRestoreFromSnapshotDefaultParameterGroup verifies a snapshot restore gets
// the default group of the snapshot's engine-version family.
func TestRestoreFromSnapshotDefaultParameterGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "src", Engine: "postgres", EngineVersion: "15.4"})
	requireNoError(t, err)

	_, err = m.CreateSnapshot(ctx, rdsdriver.SnapshotConfig{ID: "snap", InstanceID: "src"})
	requireNoError(t, err)

	restored, err := m.RestoreInstanceFromSnapshot(ctx, rdsdriver.RestoreInstanceInput{
		NewInstanceID: "restored", SnapshotID: "snap",
	})
	requireNoError(t, err)
	assertEqual(t, "default.postgres15", restored.DBParameterGroupName)
}

// TestCreateClusterDefaultClusterParameterGroup verifies a cluster created
// without a cluster parameter group gets the default one of its engine
// version, while an explicit group is preserved.
func TestCreateClusterDefaultClusterParameterGroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	c, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "c1", Engine: "aurora-postgresql", EngineVersion: "15.4"})
	requireNoError(t, err)
	assertEqual(t, "default.aurora-postgresql15", c.DBClusterParameterGroupName)

	c, err = m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "c2", Engine: "aurora-mysql", EngineVersion: "8.0.mysql_aurora.3.05.2"})
	requireNoError(t, err)
	assertEqual(t, "default.aurora-mysql8.0", c.DBClusterParameterGroupName)

	c, err = m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "c3", Engine: "aurora-mysql", DBClusterParameterGroupName: "custom"})
	requireNoError(t, err)
	assertEqual(t, "custom", c.DBClusterParameterGroupName)

	_, err = m.CreateClusterSnapshot(ctx, rdsdriver.ClusterSnapshotConfig{ID: "cs1", ClusterID: "c1"})
	requireNoError(t, err)

	restored, err := m.RestoreClusterFromSnapshot(ctx, rdsdriver.RestoreClusterInput{NewClusterID: "c4", SnapshotID: "cs1"})
	requireNoError(t, err)
	assertEqual(t, "default.aurora-postgresql15", restored.DBClusterParameterGroupName)
}

// TestDescribeDefaultParameterGroups verifies the default groups an instance
// or cluster is associated with are describable, by name and in the listing,
// with the family of the resolved engine version.
func TestDescribeDefaultParameterGroups(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "pg", Engine: "postgres", EngineVersion: "15.4", InstanceClass: "db.t3.micro"})
	requireNoError(t, err)

	_, err = m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "apg", Engine: "aurora-postgresql", EngineVersion: "16.2"})
	requireNoError(t, err)

	got, err := m.DescribeDBParameterGroups(ctx, []string{"default.postgres15"})
	requireNoError(t, err)
	assertEqual(t, 1, len(got))
	assertEqual(t, "postgres15", got[0].Family)
	assertEqual(t, "Default parameter group for postgres15", got[0].Description)

	all, err := m.DescribeDBParameterGroups(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 1, len(all))
	assertEqual(t, "default.postgres15", all[0].Name)

	clusterGroups, err := m.DescribeDBClusterParameterGroups(ctx, nil)
	requireNoError(t, err)
	assertEqual(t, 1, len(clusterGroups))
	assertEqual(t, "default.aurora-postgresql16", clusterGroups[0].Name)
	assertEqual(t, "aurora-postgresql16", clusterGroups[0].Family)

	_, err = m.DescribeDBParameterGroups(ctx, []string{"missing"})
	if err == nil {
		t.Fatal("want DBParameterGroupNotFound for a missing non-default group")
	}
}
