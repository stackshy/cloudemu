package redshift_test

import (
	"context"
	"maps"
	"strconv"
	"strings"
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

	_, err = client.DescribeTags(ctx, &awsredshift.DescribeTagsInput{ResourceName: aws.String(arn)})
	requireAPIErrorCode(t, err, "ResourceNotFoundFault")
}

// TestSDKRedshiftTagGhostARN checks that tags cannot be put on a resource
// that does not exist yet, so a later create cannot pick them up.
func TestSDKRedshiftTagGhostARN(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	arn := tagARNPrefix + "cluster:ghost"

	_, err := client.CreateTags(ctx, &awsredshift.CreateTagsInput{ResourceName: aws.String(arn), Tags: sdkTags("k", "v")})
	requireAPIErrorCode(t, err, "ResourceNotFoundFault")

	_, err = client.DeleteTags(ctx, &awsredshift.DeleteTagsInput{ResourceName: aws.String(arn), TagKeys: []string{"k"}})
	requireAPIErrorCode(t, err, "ResourceNotFoundFault")

	_, err = client.DescribeTags(ctx, &awsredshift.DescribeTagsInput{ResourceName: aws.String(arn)})
	requireAPIErrorCode(t, err, "ResourceNotFoundFault")

	created, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("ghost"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	requireTags(t, "new cluster", tagMap(created.Cluster.Tags), map[string]string{})

	// Other resource types and other accounts are checked the same way.
	for _, other := range []string{
		tagARNPrefix + "snapshot:nope",
		tagARNPrefix + "parametergroup:nope",
		tagARNPrefix + "subnetgroup:nope",
		tagARNPrefix + "eventsubscription:nope",
		"arn:aws:redshift:us-east-1:999999999999:cluster:ghost",
	} {
		_, err = client.CreateTags(ctx, &awsredshift.CreateTagsInput{ResourceName: aws.String(other), Tags: sdkTags("k", "v")})
		requireAPIErrorCode(t, err, "ResourceNotFoundFault")
	}
}

func TestSDKRedshiftTagLimits(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	arn := tagARNPrefix + "cluster:limits"

	if _, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("limits"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
		Tags:               sdkTags("b", "2", "a", "1", "c", "3"),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got, err := client.DescribeClusters(ctx, &awsredshift.DescribeClustersInput{ClusterIdentifier: aws.String("limits")})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	var order []string
	for _, tag := range got.Clusters[0].Tags {
		order = append(order, aws.ToString(tag.Key))
	}

	if strings.Join(order, ",") != "a,b,c" {
		t.Fatalf("tag order = %v, want a,b,c", order)
	}

	many := make([]string, 0, 96)
	for i := range 48 {
		many = append(many, "k"+strconv.Itoa(i), "v")
	}

	_, err = client.CreateTags(ctx, &awsredshift.CreateTagsInput{ResourceName: aws.String(arn), Tags: sdkTags(many...)})
	requireAPIErrorCode(t, err, "TagLimitExceededFault")

	bad := map[string][]rstypes.Tag{
		"aws prefix": sdkTags("aws:owner", "x"),
		"empty key":  sdkTags("", "x"),
		"long key":   sdkTags(strings.Repeat("k", 129), "x"),
		"long value": sdkTags("k", strings.Repeat("v", 257)),
	}

	for name, tags := range bad {
		_, err = client.CreateTags(ctx, &awsredshift.CreateTagsInput{ResourceName: aws.String(arn), Tags: tags})
		if err == nil {
			t.Fatalf("%s: CreateTags succeeded", name)
		}

		requireAPIErrorCode(t, err, "InvalidTagFault")
	}

	_, err = client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:  aws.String("badtags"),
		MasterUsername:     aws.String("admin"),
		MasterUserPassword: aws.String("Sup3rSecret!"),
		NodeType:           aws.String("ra3.xlplus"),
		Tags:               sdkTags("aws:x", "y"),
	})
	requireAPIErrorCode(t, err, "InvalidTagFault")

	requireTags(t, "after rejected writes", describeTagMap(t, client, arn), map[string]string{"a": "1", "b": "2", "c": "3"})
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
