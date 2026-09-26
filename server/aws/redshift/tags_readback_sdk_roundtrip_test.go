package redshift_test

import (
	"context"
	"maps"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
	rstypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"
)

const tagARNPrefix = "arn:aws:redshift:us-east-1:123456789012:"

func sdkTags(tags ...string) []rstypes.Tag {
	out := make([]rstypes.Tag, 0, len(tags)/2)
	for i := 0; i+1 < len(tags); i += 2 {
		out = append(out, rstypes.Tag{Key: aws.String(tags[i]), Value: aws.String(tags[i+1])})
	}

	return out
}

func tagMap(tags []rstypes.Tag) map[string]string {
	out := map[string]string{}
	for _, t := range tags {
		out[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}

	return out
}

func describeTagMap(t *testing.T, client *awsredshift.Client, arn string) map[string]string {
	t.Helper()

	out, err := client.DescribeTags(context.Background(), &awsredshift.DescribeTagsInput{ResourceName: aws.String(arn)})
	if err != nil {
		t.Fatalf("DescribeTags(%s): %v", arn, err)
	}

	got := map[string]string{}
	for _, r := range out.TaggedResources {
		got[aws.ToString(r.Tag.Key)] = aws.ToString(r.Tag.Value)
	}

	return got
}

func requireTags(t *testing.T, what string, got, want map[string]string) {
	t.Helper()

	if !maps.Equal(got, want) {
		t.Fatalf("%s tags = %v, want %v", what, got, want)
	}
}

// retag replaces tag "env" with "prod" and adds "team" through CreateTags and
// DeleteTags, the calls Terraform makes for an in-place tag change.
func retag(t *testing.T, client *awsredshift.Client, arn string) {
	t.Helper()

	ctx := context.Background()

	if _, err := client.DeleteTags(ctx, &awsredshift.DeleteTagsInput{
		ResourceName: aws.String(arn), TagKeys: []string{"env"},
	}); err != nil {
		t.Fatalf("DeleteTags(%s): %v", arn, err)
	}

	if _, err := client.CreateTags(ctx, &awsredshift.CreateTagsInput{
		ResourceName: aws.String(arn), Tags: sdkTags("stage", "prod", "team", "data"),
	}); err != nil {
		t.Fatalf("CreateTags(%s): %v", arn, err)
	}
}

var retagged = map[string]string{"stage": "prod", "team": "data"} //nolint:gochecknoglobals // test fixture

func TestSDKRedshiftClusterTagsReadBack(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	arn := tagARNPrefix + "cluster:tagged"

	created, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("tagged"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
		Tags:               sdkTags("env", "dev"),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	requireTags(t, "CreateCluster", tagMap(created.Cluster.Tags), map[string]string{"env": "dev"})
	requireTags(t, "DescribeTags after create", describeTagMap(t, client, arn), map[string]string{"env": "dev"})

	retag(t, client, arn)

	got, err := client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{ClusterIdentifier: aws.String("tagged")})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	requireTags(t, "DescribeClusters", tagMap(got.Clusters[0].Tags), retagged)

	if _, err := client.DeleteCluster(ctx, &awsredshift.DeleteClusterInput{
		ClusterIdentifier: aws.String("tagged"), SkipFinalClusterSnapshot: aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	requireTags(t, "DescribeTags after delete", describeTagMap(t, client, arn), map[string]string{})
}

func TestSDKRedshiftSnapshotTagsReadBack(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	arn := tagARNPrefix + "snapshot:snap1"

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("src"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	snap, err := client.CreateClusterSnapshot(ctx, &awsredshift.CreateClusterSnapshotInput{
		ClusterIdentifier:  aws.String("src"),
		SnapshotIdentifier: aws.String("snap1"),
		Tags:               sdkTags("env", "dev"),
	})
	if err != nil {
		t.Fatalf("CreateClusterSnapshot: %v", err)
	}

	if aws.ToString(snap.Snapshot.SnapshotArn) != arn {
		t.Fatalf("SnapshotArn = %q, want %q", aws.ToString(snap.Snapshot.SnapshotArn), arn)
	}

	requireTags(t, "DescribeTags after create", describeTagMap(t, client, arn), map[string]string{"env": "dev"})

	retag(t, client, arn)

	got, err := client.DescribeClusterSnapshots(ctx, &awsredshift.DescribeClusterSnapshotsInput{
		SnapshotIdentifier: aws.String("snap1"),
	})
	if err != nil {
		t.Fatalf("DescribeClusterSnapshots: %v", err)
	}

	requireTags(t, "DescribeClusterSnapshots", tagMap(got.Snapshots[0].Tags), retagged)
}

func TestSDKRedshiftGroupTagsReadBack(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateClusterParameterGroup(ctx, &awsredshift.CreateClusterParameterGroupInput{
		ParameterGroupName:   aws.String("pg1"),
		ParameterGroupFamily: aws.String("redshift-1.0"),
		Description:          aws.String("pg"),
		Tags:                 sdkTags("env", "dev"),
	}); err != nil {
		t.Fatalf("CreateClusterParameterGroup: %v", err)
	}

	if _, err := client.CreateClusterSubnetGroup(ctx, &awsredshift.CreateClusterSubnetGroupInput{
		ClusterSubnetGroupName: aws.String("sg1"),
		Description:            aws.String("sg"),
		SubnetIds:              []string{"subnet-1"},
		Tags:                   sdkTags("env", "dev"),
	}); err != nil {
		t.Fatalf("CreateClusterSubnetGroup: %v", err)
	}

	pgARN := tagARNPrefix + "parametergroup:pg1"
	sgARN := tagARNPrefix + "subnetgroup:sg1"

	requireTags(t, "parameter group DescribeTags", describeTagMap(t, client, pgARN), map[string]string{"env": "dev"})
	requireTags(t, "subnet group DescribeTags", describeTagMap(t, client, sgARN), map[string]string{"env": "dev"})

	retag(t, client, pgARN)
	retag(t, client, sgARN)

	pgs, err := client.DescribeClusterParameterGroups(ctx, &awsredshift.DescribeClusterParameterGroupsInput{
		ParameterGroupName: aws.String("pg1"),
	})
	if err != nil {
		t.Fatalf("DescribeClusterParameterGroups: %v", err)
	}

	requireTags(t, "DescribeClusterParameterGroups", tagMap(pgs.ParameterGroups[0].Tags), retagged)

	sgs, err := client.DescribeClusterSubnetGroups(ctx, &awsredshift.DescribeClusterSubnetGroupsInput{
		ClusterSubnetGroupName: aws.String("sg1"),
	})
	if err != nil {
		t.Fatalf("DescribeClusterSubnetGroups: %v", err)
	}

	requireTags(t, "DescribeClusterSubnetGroups", tagMap(sgs.ClusterSubnetGroups[0].Tags), retagged)
}
