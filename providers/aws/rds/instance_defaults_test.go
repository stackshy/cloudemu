package rds

import (
	"context"
	"testing"

	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// TestCreateInstanceDefaultParameterGroup verifies real RDS's behavior of
// assigning the account-default "default.<family>" DB parameter group when the
// caller supplies none, while an explicit group is preserved.
func TestCreateInstanceDefaultParameterGroup(t *testing.T) {
	tests := []struct {
		name       string
		engine     string
		version    string
		paramGroup string
		want       string
	}{
		{name: "mysql default", engine: "mysql", want: "default.mysql8.0"},
		// The default postgres version is 15.4, so the family is postgres15,
		// not the family of the newest catalog version.
		{name: "postgres default version", engine: "postgres", want: "default.postgres15"},
		{name: "postgres 15.4", engine: "postgres", version: "15.4", want: "default.postgres15"},
		{name: "postgres 16.2", engine: "postgres", version: "16.2", want: "default.postgres16"},
		{name: "aurora-postgresql 15.4", engine: "aurora-postgresql", version: "15.4", want: "default.aurora-postgresql15"},
		{name: "aurora-mysql default", engine: "aurora-mysql", want: "default.aurora-mysql8.0"},
		{name: "mariadb 10.11.6", engine: "mariadb", version: "10.11.6", want: "default.mariadb10.11"},
		{name: "explicit preserved", engine: "mysql", paramGroup: "custom-pg", want: "custom-pg"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			inst, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{
				ID:                   "db1",
				Engine:               tc.engine,
				EngineVersion:        tc.version,
				DBParameterGroupName: tc.paramGroup,
			})
			requireNoError(t, err)
			assertEqual(t, tc.want, inst.DBParameterGroupName)

			got, err := m.DescribeInstances(context.Background(), []string{"db1"})
			requireNoError(t, err)
			assertEqual(t, tc.want, got[0].DBParameterGroupName)
		})
	}
}

// TestCreateInstanceDefaultAvailabilityZone verifies real RDS's behavior of
// placing a single-AZ instance in an Availability Zone (reported on Describe),
// while a Multi-AZ instance's primary AZ is RDS-chosen and left unset and an
// explicit AZ is preserved.
func TestCreateInstanceDefaultAvailabilityZone(t *testing.T) {
	tests := []struct {
		name    string
		multiAZ bool
		az      string
		want    string
	}{
		{name: "single-AZ defaults to first zone", want: "us-east-1a"},
		{name: "explicit AZ preserved", az: "us-east-1c", want: "us-east-1c"},
		{name: "multi-AZ leaves primary unset", multiAZ: true, want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			inst, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{
				ID:               "db1",
				Engine:           "mysql",
				MultiAZ:          tc.multiAZ,
				AvailabilityZone: tc.az,
			})
			requireNoError(t, err)
			assertEqual(t, tc.want, inst.AvailabilityZone)
		})
	}
}
