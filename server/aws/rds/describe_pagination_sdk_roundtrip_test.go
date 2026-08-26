package rds_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
)

// The Marker/MaxRecords contract is identical across the RDS Describe list
// APIs, so the tests below share one page count and one over-a-single-page
// item count: 25 items paged 20 at a time yield exactly two pages (20 + 5).
const (
	paginationPageSize   = 20
	paginationItemCount  = 25
	paginationTotalPages = 2
)

// pageWalk drives a Marker/MaxRecords describe closure to exhaustion, returning
// the identifiers seen and the number of pages fetched. It fails if any
// identifier repeats across pages — the invariant offset pagination must hold.
func pageWalk(t *testing.T,
	describe func(marker *string) (ids []string, next *string, err error),
) (seen map[string]bool, pages int) {
	t.Helper()

	seen = map[string]bool{}

	var marker *string

	for {
		ids, next, err := describe(marker)
		if err != nil {
			t.Fatalf("describe page %d: %v", pages+1, err)
		}

		pages++

		for _, id := range ids {
			if seen[id] {
				t.Fatalf("identifier %q appeared on two pages", id)
			}

			seen[id] = true
		}

		if aws.ToString(next) == "" {
			return seen, pages
		}

		marker = next
	}
}

func TestSDKRDSDescribeDBClustersPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	for i := range paginationItemCount {
		id := fmt.Sprintf("cl-%02d", i)
		if _, err := client.CreateDBCluster(ctx, &awsrds.CreateDBClusterInput{
			DBClusterIdentifier: aws.String(id),
			Engine:              aws.String("aurora-mysql"),
		}); err != nil {
			t.Fatalf("CreateDBCluster(%s): %v", id, err)
		}
	}

	seen, pages := pageWalk(t, func(marker *string) ([]string, *string, error) {
		out, err := client.DescribeDBClusters(ctx, &awsrds.DescribeDBClustersInput{
			MaxRecords: aws.Int32(paginationPageSize), Marker: marker,
		})
		if err != nil {
			return nil, nil, err
		}

		ids := make([]string, 0, len(out.DBClusters))
		for _, c := range out.DBClusters {
			ids = append(ids, aws.ToString(c.DBClusterIdentifier))
		}

		return ids, out.Marker, nil
	})

	if len(seen) != paginationItemCount || pages != paginationTotalPages {
		t.Fatalf("walked %d clusters over %d pages, want %d over %d",
			len(seen), pages, paginationItemCount, paginationTotalPages)
	}

	single, err := client.DescribeDBClusters(ctx, &awsrds.DescribeDBClustersInput{})
	if err != nil {
		t.Fatalf("DescribeDBClusters single page: %v", err)
	}

	if aws.ToString(single.Marker) != "" {
		t.Fatal("single page returned a Marker; want none")
	}

	if _, err := client.DescribeDBClusters(ctx, &awsrds.DescribeDBClustersInput{
		Marker: aws.String("not-a-valid-marker"),
	}); err == nil {
		t.Fatal("invalid Marker accepted; want an error")
	}
}

// TestSDKRDSDescribeNegativeOffsetMarker proves a well-formed Marker (valid
// base64 + valid JSON) carrying a negative offset is rejected as
// InvalidParameterValue rather than panicking on an out-of-range slice bound
// (which surfaced as a connection reset to the SDK).
func TestSDKRDSDescribeNegativeOffsetMarker(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	// {"offset":-5}: decodes cleanly as base64+JSON, but the offset is invalid.
	negative := aws.String(base64.StdEncoding.EncodeToString([]byte(`{"offset":-5}`)))

	if _, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		Marker: negative,
	}); err == nil {
		t.Fatal("DescribeDBInstances with a negative-offset Marker succeeded, want an error")
	}
}

func TestSDKRDSDescribeDBSnapshotsPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("snap-src"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	for i := range paginationItemCount {
		id := fmt.Sprintf("snap-%02d", i)
		if _, err := client.CreateDBSnapshot(ctx, &awsrds.CreateDBSnapshotInput{
			DBSnapshotIdentifier: aws.String(id),
			DBInstanceIdentifier: aws.String("snap-src"),
		}); err != nil {
			t.Fatalf("CreateDBSnapshot(%s): %v", id, err)
		}
	}

	seen, pages := pageWalk(t, func(marker *string) ([]string, *string, error) {
		out, err := client.DescribeDBSnapshots(ctx, &awsrds.DescribeDBSnapshotsInput{
			MaxRecords: aws.Int32(paginationPageSize), Marker: marker,
		})
		if err != nil {
			return nil, nil, err
		}

		ids := make([]string, 0, len(out.DBSnapshots))
		for _, s := range out.DBSnapshots {
			ids = append(ids, aws.ToString(s.DBSnapshotIdentifier))
		}

		return ids, out.Marker, nil
	})

	if len(seen) != paginationItemCount || pages != paginationTotalPages {
		t.Fatalf("walked %d snapshots over %d pages, want %d over %d",
			len(seen), pages, paginationItemCount, paginationTotalPages)
	}

	single, err := client.DescribeDBSnapshots(ctx, &awsrds.DescribeDBSnapshotsInput{})
	if err != nil {
		t.Fatalf("DescribeDBSnapshots single page: %v", err)
	}

	if aws.ToString(single.Marker) != "" {
		t.Fatal("single page returned a Marker; want none")
	}

	if _, err := client.DescribeDBSnapshots(ctx, &awsrds.DescribeDBSnapshotsInput{
		Marker: aws.String("not-a-valid-marker"),
	}); err == nil {
		t.Fatal("invalid Marker accepted; want an error")
	}
}

func TestSDKRDSDescribeDBSubnetGroupsPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	for i := range paginationItemCount {
		id := fmt.Sprintf("subnet-grp-%02d", i)
		if _, err := client.CreateDBSubnetGroup(ctx, &awsrds.CreateDBSubnetGroupInput{
			DBSubnetGroupName:        aws.String(id),
			DBSubnetGroupDescription: aws.String("test"),
			SubnetIds:                []string{"subnet-1", "subnet-2"},
		}); err != nil {
			t.Fatalf("CreateDBSubnetGroup(%s): %v", id, err)
		}
	}

	seen, pages := pageWalk(t, func(marker *string) ([]string, *string, error) {
		out, err := client.DescribeDBSubnetGroups(ctx, &awsrds.DescribeDBSubnetGroupsInput{
			MaxRecords: aws.Int32(paginationPageSize), Marker: marker,
		})
		if err != nil {
			return nil, nil, err
		}

		ids := make([]string, 0, len(out.DBSubnetGroups))
		for _, g := range out.DBSubnetGroups {
			ids = append(ids, aws.ToString(g.DBSubnetGroupName))
		}

		return ids, out.Marker, nil
	})

	if len(seen) != paginationItemCount || pages != paginationTotalPages {
		t.Fatalf("walked %d subnet groups over %d pages, want %d over %d",
			len(seen), pages, paginationItemCount, paginationTotalPages)
	}

	single, err := client.DescribeDBSubnetGroups(ctx, &awsrds.DescribeDBSubnetGroupsInput{})
	if err != nil {
		t.Fatalf("DescribeDBSubnetGroups single page: %v", err)
	}

	if aws.ToString(single.Marker) != "" {
		t.Fatal("single page returned a Marker; want none")
	}

	if _, err := client.DescribeDBSubnetGroups(ctx, &awsrds.DescribeDBSubnetGroupsInput{
		Marker: aws.String("not-a-valid-marker"),
	}); err == nil {
		t.Fatal("invalid Marker accepted; want an error")
	}
}

func TestSDKRDSDescribeDBParameterGroupsPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	for i := range paginationItemCount {
		id := fmt.Sprintf("param-grp-%02d", i)
		if _, err := client.CreateDBParameterGroup(ctx, &awsrds.CreateDBParameterGroupInput{
			DBParameterGroupName:   aws.String(id),
			DBParameterGroupFamily: aws.String("mysql8.0"),
			Description:            aws.String("test"),
		}); err != nil {
			t.Fatalf("CreateDBParameterGroup(%s): %v", id, err)
		}
	}

	seen, pages := pageWalk(t, func(marker *string) ([]string, *string, error) {
		out, err := client.DescribeDBParameterGroups(ctx, &awsrds.DescribeDBParameterGroupsInput{
			MaxRecords: aws.Int32(paginationPageSize), Marker: marker,
		})
		if err != nil {
			return nil, nil, err
		}

		ids := make([]string, 0, len(out.DBParameterGroups))
		for _, g := range out.DBParameterGroups {
			ids = append(ids, aws.ToString(g.DBParameterGroupName))
		}

		return ids, out.Marker, nil
	})

	if len(seen) != paginationItemCount || pages != paginationTotalPages {
		t.Fatalf("walked %d parameter groups over %d pages, want %d over %d",
			len(seen), pages, paginationItemCount, paginationTotalPages)
	}

	single, err := client.DescribeDBParameterGroups(ctx, &awsrds.DescribeDBParameterGroupsInput{})
	if err != nil {
		t.Fatalf("DescribeDBParameterGroups single page: %v", err)
	}

	if aws.ToString(single.Marker) != "" {
		t.Fatal("single page returned a Marker; want none")
	}

	if _, err := client.DescribeDBParameterGroups(ctx, &awsrds.DescribeDBParameterGroupsInput{
		Marker: aws.String("not-a-valid-marker"),
	}); err == nil {
		t.Fatal("invalid Marker accepted; want an error")
	}
}

func TestSDKRDSDescribeDBClusterParameterGroupsPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	for i := range paginationItemCount {
		id := fmt.Sprintf("cluster-param-grp-%02d", i)
		if _, err := client.CreateDBClusterParameterGroup(ctx, &awsrds.CreateDBClusterParameterGroupInput{
			DBClusterParameterGroupName: aws.String(id),
			DBParameterGroupFamily:      aws.String("aurora-mysql8.0"),
			Description:                 aws.String("test"),
		}); err != nil {
			t.Fatalf("CreateDBClusterParameterGroup(%s): %v", id, err)
		}
	}

	seen, pages := pageWalk(t, func(marker *string) ([]string, *string, error) {
		out, err := client.DescribeDBClusterParameterGroups(ctx, &awsrds.DescribeDBClusterParameterGroupsInput{
			MaxRecords: aws.Int32(paginationPageSize), Marker: marker,
		})
		if err != nil {
			return nil, nil, err
		}

		ids := make([]string, 0, len(out.DBClusterParameterGroups))
		for _, g := range out.DBClusterParameterGroups {
			ids = append(ids, aws.ToString(g.DBClusterParameterGroupName))
		}

		return ids, out.Marker, nil
	})

	if len(seen) != paginationItemCount || pages != paginationTotalPages {
		t.Fatalf("walked %d cluster parameter groups over %d pages, want %d over %d",
			len(seen), pages, paginationItemCount, paginationTotalPages)
	}

	single, err := client.DescribeDBClusterParameterGroups(ctx, &awsrds.DescribeDBClusterParameterGroupsInput{})
	if err != nil {
		t.Fatalf("DescribeDBClusterParameterGroups single page: %v", err)
	}

	if aws.ToString(single.Marker) != "" {
		t.Fatal("single page returned a Marker; want none")
	}

	if _, err := client.DescribeDBClusterParameterGroups(ctx, &awsrds.DescribeDBClusterParameterGroupsInput{
		Marker: aws.String("not-a-valid-marker"),
	}); err == nil {
		t.Fatal("invalid Marker accepted; want an error")
	}
}
