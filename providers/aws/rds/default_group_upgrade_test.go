package rds

import (
	"context"
	"errors"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// TestModifyInstanceEngineUpgradeMovesDefaultParamGroup: a major version
// upgrade moves an instance on its default group to the new family's default
// group; a minor upgrade, a deferred upgrade and an explicit new-family group
// on the same request are left as the caller expects.
func TestModifyInstanceEngineUpgradeMovesDefaultParamGroup(t *testing.T) {
	ctx := context.Background()
	m, _ := newEventTestMock()

	create := func(id, group string) {
		t.Helper()

		_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
			ID: id, Engine: "postgres", EngineVersion: "15.4", DBParameterGroupName: group,
		})
		requireNoError(t, err)
	}

	modify := func(id string, in rdsdriver.ModifyInstanceInput) *rdsdriver.Instance {
		t.Helper()

		out, err := m.ModifyInstance(ctx, id, in)
		requireNoError(t, err)

		return out
	}

	create("major", "")
	got := modify("major", rdsdriver.ModifyInstanceInput{EngineVersion: "16.2", ApplyImmediately: true})
	assertEqual(t, "16.2", got.EngineVersion)
	assertEqual(t, "default.postgres16", got.DBParameterGroupName)

	described, err := m.DescribeInstances(ctx, []string{"major"})
	requireNoError(t, err)
	assertEqual(t, "default.postgres16", described[0].DBParameterGroupName)

	create("minor", "")
	got = modify("minor", rdsdriver.ModifyInstanceInput{EngineVersion: "15.5", ApplyImmediately: true})
	assertEqual(t, "default.postgres15", got.DBParameterGroupName)

	create("deferred", "")
	got = modify("deferred", rdsdriver.ModifyInstanceInput{EngineVersion: "16.2"})
	assertEqual(t, "15.4", got.EngineVersion)
	assertEqual(t, "default.postgres15", got.DBParameterGroupName)

	_, err = m.CreateDBParameterGroup(ctx, rdsdriver.ParameterGroupConfig{Name: "my16", Family: "postgres16"})
	requireNoError(t, err)

	create("explicit", "")
	got = modify("explicit", rdsdriver.ModifyInstanceInput{
		EngineVersion: "16.2", ApplyImmediately: true, DBParameterGroupName: "my16",
	})
	assertEqual(t, "16.2", got.EngineVersion)
	assertEqual(t, "my16", got.DBParameterGroupName)
}

// TestModifyInstanceRejectsParamGroupFamilyMismatch: RDS rejects, with
// InvalidParameterCombination, a major upgrade that would leave the instance
// on a custom group of the old family, and any request that puts a group of
// another family on the instance. A custom group survives a minor upgrade.
func TestModifyInstanceRejectsParamGroupFamilyMismatch(t *testing.T) {
	ctx := context.Background()
	m, _ := newEventTestMock()

	for _, g := range []rdsdriver.ParameterGroupConfig{
		{Name: "my15", Family: "postgres15"}, {Name: "my16", Family: "postgres16"},
	} {
		_, err := m.CreateDBParameterGroup(ctx, g)
		requireNoError(t, err)
	}

	_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "db1", Engine: "postgres", EngineVersion: "15.4", DBParameterGroupName: "my15",
	})
	requireNoError(t, err)

	_, err = m.ModifyInstance(ctx, "db1", rdsdriver.ModifyInstanceInput{EngineVersion: "16.2", ApplyImmediately: true})
	requireCode(t, err, cerrors.IsInvalidArgument)
	assertEqual(t, "The current DB instance parameter group my15 is custom. You must explicitly specify a new DB "+
		"instance parameter group, either default or custom, for the engine version upgrade.", errMsg(err))

	_, err = m.ModifyInstance(ctx, "db1", rdsdriver.ModifyInstanceInput{
		EngineVersion: "17.2", ApplyImmediately: true, DBParameterGroupName: "my16",
	})
	requireCode(t, err, cerrors.IsInvalidArgument)
	assertEqual(t, "The parameter group my16 with DBParameterGroupFamily postgres16 can't be used for this "+
		"instance. Use a parameter group with DBParameterGroupFamily postgres17.", errMsg(err))

	_, err = m.ModifyInstance(ctx, "db1", rdsdriver.ModifyInstanceInput{DBParameterGroupName: "default.postgres16"})
	requireCode(t, err, cerrors.IsInvalidArgument)

	// A rejected request changes nothing.
	described, err := m.DescribeInstances(ctx, []string{"db1"})
	requireNoError(t, err)
	assertEqual(t, "15.4", described[0].EngineVersion)
	assertEqual(t, "my15", described[0].DBParameterGroupName)

	got, err := m.ModifyInstance(ctx, "db1", rdsdriver.ModifyInstanceInput{EngineVersion: "15.5", ApplyImmediately: true})
	requireNoError(t, err)
	assertEqual(t, "my15", got.DBParameterGroupName)

	got, err = m.ModifyInstance(ctx, "db1", rdsdriver.ModifyInstanceInput{
		EngineVersion: "16.2", ApplyImmediately: true, DBParameterGroupName: "my16",
	})
	requireNoError(t, err)
	assertEqual(t, "16.2", got.EngineVersion)
	assertEqual(t, "my16", got.DBParameterGroupName)
}

// TestModifyClusterMajorUpgradeMovesMembers: an Aurora major upgrade upgrades
// every member instance; members on the default instance group move to the new
// family's default, or to DBInstanceParameterGroupName when it is given. A
// member on a custom old-family group needs DBInstanceParameterGroupName, which
// is itself valid only on a major upgrade.
func TestModifyClusterMajorUpgradeMovesMembers(t *testing.T) {
	ctx := context.Background()
	m, _ := newEventTestMock()

	newCluster := func(id string, groups ...string) {
		t.Helper()

		_, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: id, Engine: "aurora-postgresql", EngineVersion: "15.4"})
		requireNoError(t, err)

		for i, g := range groups {
			_, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
				ID: id + "m" + string(rune('1'+i)), Engine: "aurora-postgresql", ClusterID: id,
				DBParameterGroupName: g,
			})
			requireNoError(t, err)
		}
	}

	member := func(id string) rdsdriver.Instance {
		t.Helper()

		got, err := m.DescribeInstances(ctx, []string{id})
		requireNoError(t, err)

		return got[0]
	}

	newCluster("c1", "", "")
	assertEqual(t, "default.aurora-postgresql15", member("c1m1").DBParameterGroupName)

	_, err := m.ModifyCluster(ctx, "c1", rdsdriver.ModifyInstanceInput{EngineVersion: "16.2", ApplyImmediately: true})
	requireNoError(t, err)

	for _, id := range []string{"c1m1", "c1m2"} {
		assertEqual(t, "16.2", member(id).EngineVersion)
		assertEqual(t, "default.aurora-postgresql16", member(id).DBParameterGroupName)
	}

	// A minor upgrade moves the version only.
	_, err = m.ModifyCluster(ctx, "c1", rdsdriver.ModifyInstanceInput{EngineVersion: "16.3"})
	requireNoError(t, err)
	assertEqual(t, "16.3", member("c1m1").EngineVersion)
	assertEqual(t, "default.aurora-postgresql16", member("c1m1").DBParameterGroupName)

	_, err = m.ModifyCluster(ctx, "c1", rdsdriver.ModifyInstanceInput{
		EngineVersion: "16.4", DBInstanceParameterGroupName: "default.aurora-postgresql16",
	})
	requireCode(t, err, cerrors.IsInvalidArgument)

	for _, g := range []rdsdriver.ParameterGroupConfig{
		{Name: "apg15", Family: "aurora-postgresql15"}, {Name: "apg16", Family: "aurora-postgresql16"},
	} {
		_, err = m.CreateDBParameterGroup(ctx, g)
		requireNoError(t, err)
	}

	newCluster("c2", "apg15")

	_, err = m.ModifyCluster(ctx, "c2", rdsdriver.ModifyInstanceInput{EngineVersion: "16.2", ApplyImmediately: true})
	requireCode(t, err, cerrors.IsInvalidArgument)
	assertEqual(t, "15.4", member("c2m1").EngineVersion)

	clusters, err := m.DescribeClusters(ctx, []string{"c2"})
	requireNoError(t, err)
	assertEqual(t, "15.4", clusters[0].EngineVersion)

	_, err = m.ModifyCluster(ctx, "c2", rdsdriver.ModifyInstanceInput{
		EngineVersion: "16.2", DBInstanceParameterGroupName: "apg15",
	})
	requireCode(t, err, cerrors.IsInvalidArgument)

	_, err = m.ModifyCluster(ctx, "c2", rdsdriver.ModifyInstanceInput{
		EngineVersion: "16.2", DBInstanceParameterGroupName: "apg16",
	})
	requireNoError(t, err)
	assertEqual(t, "16.2", member("c2m1").EngineVersion)
	assertEqual(t, "apg16", member("c2m1").DBParameterGroupName)
}

// TestUnknownDefaultFamilyIsNotFound: only "default.<family>" names of a real
// family exist. "default.bogus" is DBParameterGroupNotFound on describe,
// modify and tagging, not a synthesized or immutable default group.
func TestUnknownDefaultFamilyIsNotFound(t *testing.T) {
	ctx := context.Background()
	m, _ := newEventTestMock()
	params := []rdsdriver.Parameter{{Name: "max_connections", Value: "10"}}

	for _, name := range []string{"default.bogus", "default.postgres16.2", "default.postgres-16", "default."} {
		_, err := m.ModifyDBParameterGroup(ctx, name, params)
		requireCode(t, err, cerrors.IsNotFound)

		_, err = m.DescribeDBParameterGroups(ctx, []string{name})
		requireCode(t, err, cerrors.IsNotFound)

		_, err = m.ListTagsForResource(ctx, parameterGroupARN("us-east-1", "123456789012", name))
		requireCode(t, err, cerrors.IsNotFound)
	}

	_, err := m.ModifyDBClusterParameterGroup(ctx, "default.bogus-family", params)
	requireCode(t, err, cerrors.IsNotFound)

	for _, family := range []string{
		"postgres16", "postgres9.6", "mysql8.0", "mariadb10.11", "aurora-mysql8.0", "aurora-postgresql15",
		"docdb5.0", "neptune1", "neptune1.3", "oracle-ee-19", "oracle-se2-cdb-21", "sqlserver-ee-15.0", "db2-se",
	} {
		if !isKnownFamily(family) {
			t.Errorf("isKnownFamily(%q) = false, want true", family)
		}
	}
}

// errMsg returns the human message of a cloudemu error.
func errMsg(err error) string {
	var e *cerrors.Error
	if errors.As(err, &e) {
		return e.Message
	}

	return err.Error()
}

// TestModifyClusterEngineUpgradeMovesDefaultClusterParamGroup mirrors the
// instance case for DBClusterParameterGroupName.
func TestModifyClusterEngineUpgradeMovesDefaultClusterParamGroup(t *testing.T) {
	ctx := context.Background()
	m, _ := newEventTestMock()

	_, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{
		ID: "c1", Engine: "aurora-postgresql", EngineVersion: "15.4",
	})
	requireNoError(t, err)

	got, err := m.ModifyCluster(ctx, "c1", rdsdriver.ModifyInstanceInput{EngineVersion: "16.2", ApplyImmediately: true})
	requireNoError(t, err)
	assertEqual(t, "default.aurora-postgresql16", got.DBClusterParameterGroupName)

	got, err = m.ModifyCluster(ctx, "c1", rdsdriver.ModifyInstanceInput{
		EngineVersion: "16.3", DBClusterParameterGroupName: "mine",
	})
	requireNoError(t, err)
	assertEqual(t, "mine", got.DBClusterParameterGroupName)
}

// TestCreateClusterResolvesDefaultEngineVersion: a cluster created without an
// EngineVersion reports the resolved default version, consistent with its
// version-derived default cluster parameter group.
func TestCreateClusterResolvesDefaultEngineVersion(t *testing.T) {
	ctx := context.Background()
	m, _ := newEventTestMock()

	got, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "c1", Engine: "aurora-mysql"})
	requireNoError(t, err)

	want := defaultEngineVersion("aurora-mysql")
	if want == "" {
		t.Fatal("no catalog default version for aurora-mysql")
	}

	assertEqual(t, want, got.EngineVersion)
	assertEqual(t, "default."+parameterGroupFamily("aurora-mysql", want), got.DBClusterParameterGroupName)

	described, err := m.DescribeClusters(ctx, []string{"c1"})
	requireNoError(t, err)
	assertEqual(t, want, described[0].EngineVersion)
}

// TestModifyDefaultParameterGroupRejected: a listed default group cannot be
// modified or reset, and says so (InvalidArgument), instead of NotFound.
func TestModifyDefaultParameterGroupRejected(t *testing.T) {
	ctx := context.Background()
	m, _ := newEventTestMock()
	params := []rdsdriver.Parameter{{Name: "max_connections", Value: "10"}}

	_, err := m.ModifyDBParameterGroup(ctx, "default.postgres15", params)
	requireCode(t, err, cerrors.IsInvalidArgument)

	_, err = m.ResetDBParameterGroup(ctx, "default.postgres15", nil, true)
	requireCode(t, err, cerrors.IsInvalidArgument)

	_, err = m.ModifyDBClusterParameterGroup(ctx, "default.aurora-postgresql15", params)
	requireCode(t, err, cerrors.IsInvalidArgument)

	_, err = m.ResetDBClusterParameterGroup(ctx, "default.aurora-postgresql15", nil, true)
	requireCode(t, err, cerrors.IsInvalidArgument)

	_, err = m.ModifyDBParameterGroup(ctx, "nope", params)
	requireCode(t, err, cerrors.IsNotFound)

	_, err = m.ModifyDBClusterParameterGroup(ctx, "nope", params)
	requireCode(t, err, cerrors.IsNotFound)
}

func requireCode(t *testing.T, err error, is func(error) bool) {
	t.Helper()

	if err == nil || !is(err) {
		t.Fatalf("unexpected error %v", err)
	}
}
