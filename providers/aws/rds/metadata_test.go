package rds

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestDescribeEngineVersionsAndOrderable(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	all, err := m.DescribeDBEngineVersions(ctx, "", "")
	if err != nil || len(all) == 0 {
		t.Fatalf("DescribeDBEngineVersions(all): got %d, err %v", len(all), err)
	}

	mysql, err := m.DescribeDBEngineVersions(ctx, "mysql", "")
	if err != nil || len(mysql) == 0 {
		t.Fatalf("DescribeDBEngineVersions(mysql): got %d, err %v", len(mysql), err)
	}

	for _, v := range mysql {
		if v.Engine != "mysql" {
			t.Fatalf("engine filter leaked %q", v.Engine)
		}

		if v.DBParameterGroupFamily == "" {
			t.Error("expected a parameter-group family")
		}
	}

	opts, err := m.DescribeOrderableDBInstanceOptions(ctx, "postgres", "16.2")
	if err != nil || len(opts) == 0 {
		t.Fatalf("DescribeOrderableDBInstanceOptions: got %d, err %v", len(opts), err)
	}

	if opts[0].DBInstanceClass == "" || opts[0].StorageType == "" {
		t.Fatalf("orderable option incomplete: %+v", opts[0])
	}

	if _, err := m.DescribeOrderableDBInstanceOptions(ctx, "", ""); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing engine: want InvalidArgument, got %v", err)
	}
}

func TestResourceTagging(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	inst, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "db", Engine: "mysql", Tags: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Tags set at creation are listable.
	tags, err := m.ListTagsForResource(ctx, inst.ARN)
	if err != nil || tags["env"] != "prod" {
		t.Fatalf("ListTagsForResource: %v tags=%v", err, tags)
	}

	// Add merges.
	if err := m.AddTagsToResource(ctx, inst.ARN, map[string]string{"team": "data"}); err != nil {
		t.Fatalf("AddTagsToResource: %v", err)
	}

	tags, _ = m.ListTagsForResource(ctx, inst.ARN)
	if tags["env"] != "prod" || tags["team"] != "data" {
		t.Fatalf("after add: %v", tags)
	}

	// Remove drops a key.
	if err := m.RemoveTagsFromResource(ctx, inst.ARN, []string{"env"}); err != nil {
		t.Fatalf("RemoveTagsFromResource: %v", err)
	}

	tags, _ = m.ListTagsForResource(ctx, inst.ARN)
	if _, ok := tags["env"]; ok || tags["team"] != "data" {
		t.Fatalf("after remove: %v", tags)
	}

	// Unknown ARN is NotFound.
	if _, err := m.ListTagsForResource(ctx, "arn:aws:rds:us-east-1:123456789012:db:ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("unknown resource: want NotFound, got %v", err)
	}
}
