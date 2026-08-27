package opensearch_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// TestSnapshotRoundTripOpenSearch proves a snapshot/restore round-trip preserves
// the promoted domain store (whose *domainData carries unexported status/config/
// tags) and the fully-exported package store under their original identities.
func TestSnapshotRoundTripOpenSearch(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	if _, err := src.CreateDomain(ctx, driver.CreateDomainInput{
		DomainName:    "my-domain",
		EngineVersion: "OpenSearch_2.11",
		ClusterConfig: driver.ClusterConfig{InstanceType: "t3.small.search", InstanceCount: 3},
		Tags:          map[string]string{"env": "prod"},
	}); err != nil {
		t.Fatalf("create domain: %v", err)
	}

	if _, err := src.CreatePackage(ctx, driver.CreatePackageInput{
		PackageName: "pkg-1", PackageType: "TXT-DICTIONARY", S3BucketName: "b", S3Key: "k",
	}); err != nil {
		t.Fatalf("create package: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	desc, err := dst.DescribeDomain(ctx, "my-domain")
	if err != nil {
		t.Fatalf("describe domain: %v", err)
	}

	if desc.EngineVersion != "OpenSearch_2.11" || desc.ClusterConfig.InstanceCount != 3 {
		t.Fatalf("domain config not preserved after restore: %+v", desc)
	}

	pkgs, _, err := dst.DescribePackages(ctx, driver.Page{})
	if err != nil || len(pkgs) != 1 || pkgs[0].PackageName != "pkg-1" {
		t.Fatalf("restored packages = %+v, err %v", pkgs, err)
	}
}
