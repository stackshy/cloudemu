package redshift_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
)

// pageMaxRecords is the smallest MaxRecords Redshift honors; using it lets a
// modest fixture span more than one page.
const pageMaxRecords = 20

// multiPageCount is one more than a full page, so paging yields exactly two
// pages (a full first page plus a one-item second page).
const multiPageCount = pageMaxRecords + 1

// TestSDKRedshiftDescribeClustersPagination proves DescribeClusters pages on
// Marker/MaxRecords: the paginator walks every cluster exactly once across more
// than one page and terminates.
func TestSDKRedshiftDescribeClustersPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	want := map[string]bool{}
	for i := range multiPageCount {
		id := fmt.Sprintf("cluster-%02d", i)
		want[id] = true

		if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
			ClusterIdentifier:  aws.String(id),
			MasterUsername:     aws.String("admin"),
			MasterUserPassword: aws.String("Sup3rSecret!"),
			NodeType:           aws.String("ra3.xlplus"),
		}); err != nil {
			t.Fatalf("CreateCluster(%s): %v", id, err)
		}
	}

	pager := awsredshift.NewDescribeClustersPaginator(client, &awsredshift.DescribeClustersInput{
		MaxRecords: aws.Int32(pageMaxRecords),
	})

	seen := map[string]int{}
	pages := 0

	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("DescribeClusters page %d: %v", pages, err)
		}

		pages++
		for i := range out.Clusters {
			seen[aws.ToString(out.Clusters[i].ClusterIdentifier)]++
		}

		if pages > multiPageCount {
			t.Fatal("pagination did not terminate")
		}
	}

	assertPagedExactlyOnce(t, pages, want, seen)
}

// TestSDKRedshiftDescribeClusterSnapshotsPagination proves DescribeClusterSnapshots
// pages on Marker/MaxRecords.
func TestSDKRedshiftDescribeClusterSnapshotsPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("snap-src"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	want := map[string]bool{}
	for i := range multiPageCount {
		id := fmt.Sprintf("snap-%02d", i)
		want[id] = true

		if _, err := client.CreateClusterSnapshot(ctx, &awsredshift.CreateClusterSnapshotInput{
			SnapshotIdentifier: aws.String(id),
			ClusterIdentifier:  aws.String("snap-src"),
		}); err != nil {
			t.Fatalf("CreateClusterSnapshot(%s): %v", id, err)
		}
	}

	pager := awsredshift.NewDescribeClusterSnapshotsPaginator(client, &awsredshift.DescribeClusterSnapshotsInput{
		MaxRecords: aws.Int32(pageMaxRecords),
	})

	seen := map[string]int{}
	pages := 0

	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("DescribeClusterSnapshots page %d: %v", pages, err)
		}

		pages++
		for i := range out.Snapshots {
			seen[aws.ToString(out.Snapshots[i].SnapshotIdentifier)]++
		}

		if pages > multiPageCount {
			t.Fatal("pagination did not terminate")
		}
	}

	assertPagedExactlyOnce(t, pages, want, seen)
}

// TestSDKRedshiftDescribeClusterParameterGroupsPagination proves
// DescribeClusterParameterGroups pages on Marker/MaxRecords.
func TestSDKRedshiftDescribeClusterParameterGroupsPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	want := map[string]bool{}
	for i := range multiPageCount {
		name := fmt.Sprintf("pg-%02d", i)
		want[name] = true

		if _, err := client.CreateClusterParameterGroup(ctx, &awsredshift.CreateClusterParameterGroupInput{
			ParameterGroupName:   aws.String(name),
			ParameterGroupFamily: aws.String("redshift-1.0"),
			Description:          aws.String("pg"),
		}); err != nil {
			t.Fatalf("CreateClusterParameterGroup(%s): %v", name, err)
		}
	}

	pager := awsredshift.NewDescribeClusterParameterGroupsPaginator(client,
		&awsredshift.DescribeClusterParameterGroupsInput{MaxRecords: aws.Int32(pageMaxRecords)})

	seen := map[string]int{}
	pages := 0

	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("DescribeClusterParameterGroups page %d: %v", pages, err)
		}

		pages++
		for i := range out.ParameterGroups {
			seen[aws.ToString(out.ParameterGroups[i].ParameterGroupName)]++
		}

		if pages > multiPageCount {
			t.Fatal("pagination did not terminate")
		}
	}

	assertPagedExactlyOnce(t, pages, want, seen)
}

// TestSDKRedshiftDescribeClusterSubnetGroupsPagination proves
// DescribeClusterSubnetGroups pages on Marker/MaxRecords.
func TestSDKRedshiftDescribeClusterSubnetGroupsPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	want := map[string]bool{}
	for i := range multiPageCount {
		name := fmt.Sprintf("sg-%02d", i)
		want[name] = true

		if _, err := client.CreateClusterSubnetGroup(ctx, &awsredshift.CreateClusterSubnetGroupInput{
			ClusterSubnetGroupName: aws.String(name),
			Description:            aws.String("sg"),
			SubnetIds:              []string{"subnet-1"},
		}); err != nil {
			t.Fatalf("CreateClusterSubnetGroup(%s): %v", name, err)
		}
	}

	pager := awsredshift.NewDescribeClusterSubnetGroupsPaginator(client,
		&awsredshift.DescribeClusterSubnetGroupsInput{MaxRecords: aws.Int32(pageMaxRecords)})

	seen := map[string]int{}
	pages := 0

	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("DescribeClusterSubnetGroups page %d: %v", pages, err)
		}

		pages++
		for i := range out.ClusterSubnetGroups {
			seen[aws.ToString(out.ClusterSubnetGroups[i].ClusterSubnetGroupName)]++
		}

		if pages > multiPageCount {
			t.Fatal("pagination did not terminate")
		}
	}

	assertPagedExactlyOnce(t, pages, want, seen)
}

// TestSDKRedshiftDescribeSinglePageNoMarker proves a fixture that fits in one
// page returns no Marker for each of the four Describe operations.
func TestSDKRedshiftDescribeSinglePageNoMarker(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("solo"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := client.CreateClusterSnapshot(ctx, &awsredshift.CreateClusterSnapshotInput{
		SnapshotIdentifier: aws.String("solo-snap"),
		ClusterIdentifier:  aws.String("solo"),
	}); err != nil {
		t.Fatalf("CreateClusterSnapshot: %v", err)
	}

	if _, err := client.CreateClusterParameterGroup(ctx, &awsredshift.CreateClusterParameterGroupInput{
		ParameterGroupName:   aws.String("solo-pg"),
		ParameterGroupFamily: aws.String("redshift-1.0"),
		Description:          aws.String("pg"),
	}); err != nil {
		t.Fatalf("CreateClusterParameterGroup: %v", err)
	}

	if _, err := client.CreateClusterSubnetGroup(ctx, &awsredshift.CreateClusterSubnetGroupInput{
		ClusterSubnetGroupName: aws.String("solo-sg"),
		Description:            aws.String("sg"),
		SubnetIds:              []string{"subnet-1"},
	}); err != nil {
		t.Fatalf("CreateClusterSubnetGroup: %v", err)
	}

	clusters, err := client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}
	if aws.ToString(clusters.Marker) != "" {
		t.Errorf("DescribeClusters single page carried Marker %q", aws.ToString(clusters.Marker))
	}

	snaps, err := client.DescribeClusterSnapshots(ctx, &awsredshift.DescribeClusterSnapshotsInput{})
	if err != nil {
		t.Fatalf("DescribeClusterSnapshots: %v", err)
	}
	if aws.ToString(snaps.Marker) != "" {
		t.Errorf("DescribeClusterSnapshots single page carried Marker %q", aws.ToString(snaps.Marker))
	}

	pgs, err := client.DescribeClusterParameterGroups(ctx, &awsredshift.DescribeClusterParameterGroupsInput{})
	if err != nil {
		t.Fatalf("DescribeClusterParameterGroups: %v", err)
	}
	if aws.ToString(pgs.Marker) != "" {
		t.Errorf("DescribeClusterParameterGroups single page carried Marker %q", aws.ToString(pgs.Marker))
	}

	sgs, err := client.DescribeClusterSubnetGroups(ctx, &awsredshift.DescribeClusterSubnetGroupsInput{})
	if err != nil {
		t.Fatalf("DescribeClusterSubnetGroups: %v", err)
	}
	if aws.ToString(sgs.Marker) != "" {
		t.Errorf("DescribeClusterSubnetGroups single page carried Marker %q", aws.ToString(sgs.Marker))
	}
}

// TestSDKRedshiftDescribeInvalidMarker proves an unreadable Marker is rejected by
// each of the four Describe operations.
func TestSDKRedshiftDescribeInvalidMarker(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	bad := aws.String("!!!not-base64!!!")

	if _, err := client.DescribeClusters(ctx,
		&awsredshift.DescribeClustersInput{Marker: bad}); err == nil {
		t.Error("DescribeClusters with an invalid Marker succeeded, want an error")
	}

	if _, err := client.DescribeClusterSnapshots(ctx,
		&awsredshift.DescribeClusterSnapshotsInput{Marker: bad}); err == nil {
		t.Error("DescribeClusterSnapshots with an invalid Marker succeeded, want an error")
	}

	if _, err := client.DescribeClusterParameterGroups(ctx,
		&awsredshift.DescribeClusterParameterGroupsInput{Marker: bad}); err == nil {
		t.Error("DescribeClusterParameterGroups with an invalid Marker succeeded, want an error")
	}

	if _, err := client.DescribeClusterSubnetGroups(ctx,
		&awsredshift.DescribeClusterSubnetGroupsInput{Marker: bad}); err == nil {
		t.Error("DescribeClusterSubnetGroups with an invalid Marker succeeded, want an error")
	}
}

// TestSDKRedshiftDescribeNegativeOffsetMarker proves a well-formed Marker (valid
// base64 + valid JSON) carrying a negative offset is rejected as invalid rather
// than panicking on an out-of-range slice bound (which surfaced as a connection
// reset to the SDK).
func TestSDKRedshiftDescribeNegativeOffsetMarker(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	// {"offset":-5}: decodes cleanly as base64+JSON, but the offset is invalid.
	negative := aws.String(base64.StdEncoding.EncodeToString([]byte(`{"offset":-5}`)))

	if _, err := client.DescribeClusters(ctx,
		&awsredshift.DescribeClustersInput{Marker: negative}); err == nil {
		t.Error("DescribeClusters with a negative-offset Marker succeeded, want an error")
	}
}

// assertPagedExactlyOnce checks the walk spanned more than one page and that
// every wanted id was returned exactly once.
func assertPagedExactlyOnce(t *testing.T, pages int, want map[string]bool, seen map[string]int) {
	t.Helper()

	if pages < 2 {
		t.Errorf("walked %d pages, want more than one", pages)
	}

	for id := range want {
		if seen[id] != 1 {
			t.Errorf("id %q returned %d times, want exactly 1", id, seen[id])
		}
	}

	if len(seen) != len(want) {
		t.Errorf("saw %d distinct ids, want %d", len(seen), len(want))
	}
}
