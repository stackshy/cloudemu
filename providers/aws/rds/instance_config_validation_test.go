package rds

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestCreateInstanceRejectsInvalidEngine(t *testing.T) {
	m := newTestMock()

	_, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{
		ID:            "db1",
		Engine:        "not-a-real-engine",
		InstanceClass: "db.t3.micro",
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for bad engine, got %v", err)
	}

	// The rejected instance must not have been reserved.
	if insts, _ := m.DescribeInstances(context.Background(), nil); len(insts) != 0 {
		t.Fatalf("no instance should be created on a validation failure, got %d", len(insts))
	}
}

func TestCreateInstanceRejectsInvalidClass(t *testing.T) {
	m := newTestMock()

	_, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{
		ID:            "db1",
		Engine:        "mysql",
		InstanceClass: "db.bogus.xlarge",
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for bad instance class, got %v", err)
	}
}

func TestCreateInstanceAcceptsKnownEnginesAndClasses(t *testing.T) {
	cases := []struct {
		engine string
		class  string
	}{
		{"mysql", "db.t3.micro"},
		{"postgres", "db.r5.large"},
		{"aurora-mysql", "db.r6g.2xlarge"},
		{"aurora-postgresql", "db.serverless"},
		{"mariadb", ""}, // empty class defaults later
		{"sqlserver-ex", "db.m5.xlarge"},
		{"docdb", "db.r5.large"},
		{"neptune", "db.r5.large"},
	}

	for _, tc := range cases {
		m := newTestMock()

		_, err := m.CreateInstance(context.Background(), rdsdriver.InstanceConfig{
			ID:            "db1",
			Engine:        tc.engine,
			InstanceClass: tc.class,
		})
		if err != nil {
			t.Fatalf("engine=%q class=%q should be accepted: %v", tc.engine, tc.class, err)
		}
	}
}
