package memorydb_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsmemorydb "github.com/aws/aws-sdk-go-v2/service/memorydb"
	mdbtypes "github.com/aws/aws-sdk-go-v2/service/memorydb/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2/config"
	mdbprovider "github.com/stackshy/cloudemu/v2/providers/aws/memorydb"
	mdbserver "github.com/stackshy/cloudemu/v2/server/aws/memorydb"
)

func newSDKClient(t *testing.T) *awsmemorydb.Client {
	t.Helper()

	opts := config.NewOptions(config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))
	srv := httptest.NewServer(mdbserver.New(mdbprovider.New(opts)))
	t.Cleanup(srv.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsmemorydb.NewFromConfig(cfg, func(o *awsmemorydb.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
	})
}

func TestSDKClusterLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
		ClusterName:         aws.String("orders"),
		NodeType:            aws.String("db.r6g.large"),
		ACLName:             aws.String("open-access"),
		NumShards:           aws.Int32(2),
		NumReplicasPerShard: aws.Int32(1),
		Tags: []mdbtypes.Tag{
			{Key: aws.String("env"), Value: aws.String("prod")},
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if aws.ToString(out.Cluster.Name) != "orders" {
		t.Fatalf("name = %q, want orders", aws.ToString(out.Cluster.Name))
	}

	if aws.ToInt32(out.Cluster.NumberOfShards) != 2 {
		t.Fatalf("shards = %d, want 2", aws.ToInt32(out.Cluster.NumberOfShards))
	}

	if out.Cluster.ClusterEndpoint == nil || aws.ToString(out.Cluster.ClusterEndpoint.Address) == "" {
		t.Fatalf("expected a cluster endpoint, got %+v", out.Cluster.ClusterEndpoint)
	}

	got, err := client.DescribeClusters(ctx, &awsmemorydb.DescribeClustersInput{
		ClusterName:      aws.String("orders"),
		ShowShardDetails: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if len(got.Clusters) != 1 || len(got.Clusters[0].Shards) != 2 {
		t.Fatalf("expected 1 cluster with 2 shards, got %+v", got.Clusters)
	}

	upd, err := client.UpdateCluster(ctx, &awsmemorydb.UpdateClusterInput{
		ClusterName:        aws.String("orders"),
		ShardConfiguration: &mdbtypes.ShardConfigurationRequest{ShardCount: 3},
	})
	if err != nil {
		t.Fatalf("UpdateCluster: %v", err)
	}

	if aws.ToInt32(upd.Cluster.NumberOfShards) != 3 {
		t.Fatalf("after update shards = %d, want 3", aws.ToInt32(upd.Cluster.NumberOfShards))
	}

	del, err := client.DeleteCluster(ctx, &awsmemorydb.DeleteClusterInput{
		ClusterName: aws.String("orders"),
	})
	if err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if aws.ToString(del.Cluster.Status) != "deleting" {
		t.Fatalf("delete status = %q, want deleting", aws.ToString(del.Cluster.Status))
	}

	_, err = client.DescribeClusters(ctx, &awsmemorydb.DescribeClustersInput{
		ClusterName: aws.String("orders"),
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ClusterNotFoundFault" {
		t.Fatalf("Describe after delete: got %v, want ClusterNotFoundFault", err)
	}
}

func TestSDKACLsAndUsers(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &awsmemorydb.CreateUserInput{
		UserName:     aws.String("app"),
		AccessString: aws.String("on ~* +@all"),
		AuthenticationMode: &mdbtypes.AuthenticationMode{
			Type:      mdbtypes.InputAuthenticationTypePassword,
			Passwords: []string{"averylongpasswordvalue1234"},
		},
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.CreateACL(ctx, &awsmemorydb.CreateACLInput{
		ACLName:   aws.String("app-acl"),
		UserNames: []string{"app"},
	}); err != nil {
		t.Fatalf("CreateACL: %v", err)
	}

	got, err := client.DescribeACLs(ctx, &awsmemorydb.DescribeACLsInput{ACLName: aws.String("app-acl")})
	if err != nil {
		t.Fatalf("DescribeACLs: %v", err)
	}

	if len(got.ACLs) != 1 || len(got.ACLs[0].UserNames) != 1 {
		t.Fatalf("expected acl with 1 user, got %+v", got.ACLs)
	}

	// Deleting a user still attached to an ACL must fail.
	_, err = client.DeleteUser(ctx, &awsmemorydb.DeleteUserInput{UserName: aws.String("app")})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("DeleteUser in-use: got %v, want InvalidParameterValueException", err)
	}
}

func TestSDKParameterAndSubnetGroups(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateParameterGroup(ctx, &awsmemorydb.CreateParameterGroupInput{
		ParameterGroupName: aws.String("pg1"),
		Family:             aws.String("memorydb_redis7"),
		Description:        aws.String("custom"),
	}); err != nil {
		t.Fatalf("CreateParameterGroup: %v", err)
	}

	if _, err := client.UpdateParameterGroup(ctx, &awsmemorydb.UpdateParameterGroupInput{
		ParameterGroupName: aws.String("pg1"),
		ParameterNameValues: []mdbtypes.ParameterNameValue{
			{ParameterName: aws.String("maxmemory-policy"), ParameterValue: aws.String("allkeys-lru")},
		},
	}); err != nil {
		t.Fatalf("UpdateParameterGroup: %v", err)
	}

	params, err := client.DescribeParameters(ctx, &awsmemorydb.DescribeParametersInput{
		ParameterGroupName: aws.String("pg1"),
	})
	if err != nil {
		t.Fatalf("DescribeParameters: %v", err)
	}

	if len(params.Parameters) == 0 {
		t.Fatal("expected parameters, got none")
	}

	if _, err := client.CreateSubnetGroup(ctx, &awsmemorydb.CreateSubnetGroupInput{
		SubnetGroupName: aws.String("sg1"),
		SubnetIds:       []string{"subnet-a", "subnet-b"},
	}); err != nil {
		t.Fatalf("CreateSubnetGroup: %v", err)
	}

	sgs, err := client.DescribeSubnetGroups(ctx, &awsmemorydb.DescribeSubnetGroupsInput{
		SubnetGroupName: aws.String("sg1"),
	})
	if err != nil {
		t.Fatalf("DescribeSubnetGroups: %v", err)
	}

	if len(sgs.SubnetGroups) != 1 || len(sgs.SubnetGroups[0].Subnets) != 2 {
		t.Fatalf("expected subnet group with 2 subnets, got %+v", sgs.SubnetGroups)
	}
}

func TestSDKSnapshotAndRestore(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
		ClusterName: aws.String("src"),
		NodeType:    aws.String("db.r6g.large"),
		ACLName:     aws.String("open-access"),
		NumShards:   aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := client.CreateSnapshot(ctx, &awsmemorydb.CreateSnapshotInput{
		ClusterName:  aws.String("src"),
		SnapshotName: aws.String("snap1"),
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	snaps, err := client.DescribeSnapshots(ctx, &awsmemorydb.DescribeSnapshotsInput{
		SnapshotName: aws.String("snap1"),
	})
	if err != nil {
		t.Fatalf("DescribeSnapshots: %v", err)
	}

	if len(snaps.Snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps.Snapshots))
	}

	// Restore into a new cluster from the snapshot.
	restored, err := client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
		ClusterName:  aws.String("restored"),
		NodeType:     aws.String("db.r6g.large"),
		ACLName:      aws.String("open-access"),
		SnapshotName: aws.String("snap1"),
	})
	if err != nil {
		t.Fatalf("CreateCluster from snapshot: %v", err)
	}

	if aws.ToString(restored.Cluster.Name) != "restored" {
		t.Fatalf("restored name = %q", aws.ToString(restored.Cluster.Name))
	}
}

func TestSDKMultiRegionAndReservedNodes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	mrc, err := client.CreateMultiRegionCluster(ctx, &awsmemorydb.CreateMultiRegionClusterInput{
		MultiRegionClusterNameSuffix: aws.String("global"),
		NodeType:                     aws.String("db.r6g.large"),
		NumShards:                    aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("CreateMultiRegionCluster: %v", err)
	}

	if aws.ToString(mrc.MultiRegionCluster.MultiRegionClusterName) == "" {
		t.Fatal("expected a multi-region cluster name")
	}

	offers, err := client.DescribeReservedNodesOfferings(ctx, &awsmemorydb.DescribeReservedNodesOfferingsInput{})
	if err != nil {
		t.Fatalf("DescribeReservedNodesOfferings: %v", err)
	}

	if len(offers.ReservedNodesOfferings) == 0 {
		t.Fatal("expected at least one reserved-nodes offering")
	}

	if _, err := client.PurchaseReservedNodesOffering(ctx, &awsmemorydb.PurchaseReservedNodesOfferingInput{
		ReservedNodesOfferingId: offers.ReservedNodesOfferings[0].ReservedNodesOfferingId,
		ReservationId:           aws.String("res1"),
	}); err != nil {
		t.Fatalf("PurchaseReservedNodesOffering: %v", err)
	}

	nodes, err := client.DescribeReservedNodes(ctx, &awsmemorydb.DescribeReservedNodesInput{})
	if err != nil {
		t.Fatalf("DescribeReservedNodes: %v", err)
	}

	if len(nodes.ReservedNodes) == 0 {
		t.Fatal("expected a purchased reserved node")
	}
}

func TestSDKFailoverAndNodeTypeUpdates(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
		ClusterName:         aws.String("ha"),
		NodeType:            aws.String("db.r6g.large"),
		ACLName:             aws.String("open-access"),
		NumShards:           aws.Int32(1),
		NumReplicasPerShard: aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := client.FailoverShard(ctx, &awsmemorydb.FailoverShardInput{
		ClusterName: aws.String("ha"),
		ShardName:   aws.String("0001"),
	}); err != nil {
		t.Fatalf("FailoverShard: %v", err)
	}

	up, err := client.ListAllowedNodeTypeUpdates(ctx, &awsmemorydb.ListAllowedNodeTypeUpdatesInput{
		ClusterName: aws.String("ha"),
	})
	if err != nil {
		t.Fatalf("ListAllowedNodeTypeUpdates: %v", err)
	}

	if len(up.ScaleUpNodeTypes) == 0 && len(up.ScaleDownNodeTypes) == 0 {
		t.Fatal("expected some allowed node-type updates")
	}
}

func TestSDKACLUpdateDeleteAndUserUpdate(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &awsmemorydb.CreateUserInput{
		UserName:     aws.String("u1"),
		AccessString: aws.String("on ~* +@read"),
		AuthenticationMode: &mdbtypes.AuthenticationMode{
			Type:      mdbtypes.InputAuthenticationTypePassword,
			Passwords: []string{"averylongpasswordvalue1234"},
		},
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.UpdateUser(ctx, &awsmemorydb.UpdateUserInput{
		UserName:     aws.String("u1"),
		AccessString: aws.String("on ~* +@all"),
	}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	users, err := client.DescribeUsers(ctx, &awsmemorydb.DescribeUsersInput{})
	if err != nil {
		t.Fatalf("DescribeUsers: %v", err)
	}

	if len(users.Users) == 0 {
		t.Fatal("expected at least one user")
	}

	if _, err := client.CreateACL(ctx, &awsmemorydb.CreateACLInput{ACLName: aws.String("acl2")}); err != nil {
		t.Fatalf("CreateACL: %v", err)
	}

	upd, err := client.UpdateACL(ctx, &awsmemorydb.UpdateACLInput{
		ACLName:        aws.String("acl2"),
		UserNamesToAdd: []string{"u1"},
	})
	if err != nil {
		t.Fatalf("UpdateACL: %v", err)
	}

	if len(upd.ACL.UserNames) != 1 {
		t.Fatalf("expected 1 user on acl2, got %d", len(upd.ACL.UserNames))
	}

	if _, err := client.DeleteACL(ctx, &awsmemorydb.DeleteACLInput{ACLName: aws.String("acl2")}); err != nil {
		t.Fatalf("DeleteACL: %v", err)
	}
}

func TestSDKParameterAndSubnetGroupUpdateDelete(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateParameterGroup(ctx, &awsmemorydb.CreateParameterGroupInput{
		ParameterGroupName: aws.String("pg2"),
		Family:             aws.String("memorydb_redis7"),
	}); err != nil {
		t.Fatalf("CreateParameterGroup: %v", err)
	}

	if _, err := client.ResetParameterGroup(ctx, &awsmemorydb.ResetParameterGroupInput{
		ParameterGroupName: aws.String("pg2"),
		AllParameters:      true,
	}); err != nil {
		t.Fatalf("ResetParameterGroup: %v", err)
	}

	groups, err := client.DescribeParameterGroups(ctx, &awsmemorydb.DescribeParameterGroupsInput{})
	if err != nil {
		t.Fatalf("DescribeParameterGroups: %v", err)
	}

	if len(groups.ParameterGroups) == 0 {
		t.Fatal("expected parameter groups")
	}

	if _, err := client.DeleteParameterGroup(ctx, &awsmemorydb.DeleteParameterGroupInput{
		ParameterGroupName: aws.String("pg2"),
	}); err != nil {
		t.Fatalf("DeleteParameterGroup: %v", err)
	}

	if _, err := client.CreateSubnetGroup(ctx, &awsmemorydb.CreateSubnetGroupInput{
		SubnetGroupName: aws.String("sg2"),
		SubnetIds:       []string{"subnet-a"},
	}); err != nil {
		t.Fatalf("CreateSubnetGroup: %v", err)
	}

	if _, err := client.UpdateSubnetGroup(ctx, &awsmemorydb.UpdateSubnetGroupInput{
		SubnetGroupName: aws.String("sg2"),
		SubnetIds:       []string{"subnet-a", "subnet-c"},
	}); err != nil {
		t.Fatalf("UpdateSubnetGroup: %v", err)
	}

	if _, err := client.DeleteSubnetGroup(ctx, &awsmemorydb.DeleteSubnetGroupInput{
		SubnetGroupName: aws.String("sg2"),
	}); err != nil {
		t.Fatalf("DeleteSubnetGroup: %v", err)
	}
}

func TestSDKSnapshotCopyDeleteTagsCatalogs(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
		ClusterName: aws.String("tagged"),
		NodeType:    aws.String("db.r6g.large"),
		ACLName:     aws.String("open-access"),
		NumShards:   aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	arn := aws.ToString(created.Cluster.ARN)

	if _, err := client.CreateSnapshot(ctx, &awsmemorydb.CreateSnapshotInput{
		ClusterName:  aws.String("tagged"),
		SnapshotName: aws.String("s1"),
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	if _, err := client.CopySnapshot(ctx, &awsmemorydb.CopySnapshotInput{
		SourceSnapshotName: aws.String("s1"),
		TargetSnapshotName: aws.String("s1-copy"),
	}); err != nil {
		t.Fatalf("CopySnapshot: %v", err)
	}

	if _, err := client.DeleteSnapshot(ctx, &awsmemorydb.DeleteSnapshotInput{
		SnapshotName: aws.String("s1-copy"),
	}); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}

	if _, err := client.TagResource(ctx, &awsmemorydb.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        []mdbtypes.Tag{{Key: aws.String("team"), Value: aws.String("data")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := client.ListTags(ctx, &awsmemorydb.ListTagsInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}

	if len(tags.TagList) == 0 {
		t.Fatal("expected tags on the cluster")
	}

	if _, err := client.UntagResource(ctx, &awsmemorydb.UntagResourceInput{
		ResourceArn: aws.String(arn),
		TagKeys:     []string{"team"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	vers, err := client.DescribeEngineVersions(ctx, &awsmemorydb.DescribeEngineVersionsInput{})
	if err != nil {
		t.Fatalf("DescribeEngineVersions: %v", err)
	}

	if len(vers.EngineVersions) == 0 {
		t.Fatal("expected engine versions")
	}

	if _, err := client.DescribeEvents(ctx, &awsmemorydb.DescribeEventsInput{}); err != nil {
		t.Fatalf("DescribeEvents: %v", err)
	}
}

func TestSDKMultiRegionFullSurface(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateMultiRegionCluster(ctx, &awsmemorydb.CreateMultiRegionClusterInput{
		MultiRegionClusterNameSuffix: aws.String("mr"),
		NodeType:                     aws.String("db.r6g.large"),
		NumShards:                    aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("CreateMultiRegionCluster: %v", err)
	}

	name := aws.ToString(created.MultiRegionCluster.MultiRegionClusterName)

	if _, err := client.UpdateMultiRegionCluster(ctx, &awsmemorydb.UpdateMultiRegionClusterInput{
		MultiRegionClusterName: aws.String(name),
		NodeType:               aws.String("db.r6g.xlarge"),
	}); err != nil {
		t.Fatalf("UpdateMultiRegionCluster: %v", err)
	}

	list, err := client.DescribeMultiRegionClusters(ctx, &awsmemorydb.DescribeMultiRegionClustersInput{})
	if err != nil {
		t.Fatalf("DescribeMultiRegionClusters: %v", err)
	}

	if len(list.MultiRegionClusters) == 0 {
		t.Fatal("expected a multi-region cluster")
	}

	if _, err := client.ListAllowedMultiRegionClusterUpdates(ctx,
		&awsmemorydb.ListAllowedMultiRegionClusterUpdatesInput{MultiRegionClusterName: aws.String(name)}); err != nil {
		t.Fatalf("ListAllowedMultiRegionClusterUpdates: %v", err)
	}

	if _, err := client.DescribeMultiRegionParameterGroups(ctx,
		&awsmemorydb.DescribeMultiRegionParameterGroupsInput{}); err != nil {
		t.Fatalf("DescribeMultiRegionParameterGroups: %v", err)
	}

	if _, err := client.DeleteMultiRegionCluster(ctx, &awsmemorydb.DeleteMultiRegionClusterInput{
		MultiRegionClusterName: aws.String(name),
	}); err != nil {
		t.Fatalf("DeleteMultiRegionCluster: %v", err)
	}
}

func TestHandlerMatches(t *testing.T) {
	opts := config.NewOptions(config.WithRegion("us-east-1"), config.WithAccountID("123456789012"))
	h := mdbserver.New(mdbprovider.New(opts))

	yes := httptest.NewRequest(http.MethodPost, "/", nil)
	yes.Header.Set("X-Amz-Target", "AmazonMemoryDB.CreateCluster")

	if !h.Matches(yes) {
		t.Fatal("expected Matches to claim an AmazonMemoryDB target")
	}

	no := httptest.NewRequest(http.MethodPost, "/", nil)
	no.Header.Set("X-Amz-Target", "AmazonElastiCache.CreateCacheCluster")

	if h.Matches(no) {
		t.Fatal("expected Matches to reject a non-MemoryDB target")
	}
}

func TestSDKErrorFaults(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
		ClusterName: aws.String("dup"),
		NodeType:    aws.String("db.r6g.large"),
		ACLName:     aws.String("open-access"),
		NumShards:   aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	// Duplicate name -> ClusterAlreadyExistsFault (writeErr AlreadyExists).
	_, err := client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
		ClusterName: aws.String("dup"),
		NodeType:    aws.String("db.r6g.large"),
		ACLName:     aws.String("open-access"),
		NumShards:   aws.Int32(1),
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ClusterAlreadyExistsFault" {
		t.Fatalf("duplicate create: got %v, want ClusterAlreadyExistsFault", err)
	}

	// A dangling sibling reference surfaces the specific SDK-modeled fault, not a
	// generic InvalidParameterValueException, so typed errors.As matches work.
	_, err = client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
		ClusterName: aws.String("bad-acl"),
		NodeType:    aws.String("db.r6g.large"),
		ACLName:     aws.String("no-such-acl"),
		NumShards:   aws.Int32(1),
	})

	var aclNF *mdbtypes.ACLNotFoundFault
	if !errors.As(err, &aclNF) {
		t.Fatalf("bad ACL ref: got %v, want ACLNotFoundFault", err)
	}

	_, err = client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
		ClusterName:     aws.String("bad-subnet"),
		NodeType:        aws.String("db.r6g.large"),
		ACLName:         aws.String("open-access"),
		SubnetGroupName: aws.String("no-such-subnet-group"),
		NumShards:       aws.Int32(1),
	})

	var subnetNF *mdbtypes.SubnetGroupNotFoundFault
	if !errors.As(err, &subnetNF) {
		t.Fatalf("bad subnet group ref: got %v, want SubnetGroupNotFoundFault", err)
	}

	_, err = client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
		ClusterName:        aws.String("bad-pg"),
		NodeType:           aws.String("db.r6g.large"),
		ACLName:            aws.String("open-access"),
		ParameterGroupName: aws.String("no-such-parameter-group"),
		NumShards:          aws.Int32(1),
	})

	var pgNF *mdbtypes.ParameterGroupNotFoundFault
	if !errors.As(err, &pgNF) {
		t.Fatalf("bad parameter group ref: got %v, want ParameterGroupNotFoundFault", err)
	}
}

func TestSDKClusterCreateDefaults(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	// Omitting TLSEnabled and NumReplicasPerShard must match AWS defaults:
	// TLS on, 1 replica/shard (2 nodes) and MultiAZ. A custom Port propagates to
	// every node endpoint, not just the cluster endpoint.
	out, err := client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
		ClusterName: aws.String("defaults"),
		NodeType:    aws.String("db.r6g.large"),
		ACLName:     aws.String("open-access"),
		NumShards:   aws.Int32(2),
		Port:        aws.Int32(6380),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	c := out.Cluster
	if !aws.ToBool(c.TLSEnabled) {
		t.Errorf("TLSEnabled default = false, want true")
	}

	if c.AvailabilityMode != mdbtypes.AZStatusMultiAZ {
		t.Errorf("AvailabilityMode = %q, want multiaz", c.AvailabilityMode)
	}

	if len(c.Shards) != 2 || aws.ToInt32(c.Shards[0].NumberOfNodes) != 2 || len(c.Shards[0].Nodes) != 2 {
		t.Fatalf("topology = %d shards / %d nodes, want 2 shards / 2 nodes/shard",
			len(c.Shards), aws.ToInt32(c.Shards[0].NumberOfNodes))
	}

	if p := c.ClusterEndpoint.Port; p != 6380 {
		t.Errorf("ClusterEndpoint.Port = %d, want 6380", p)
	}

	if p := c.Shards[0].Nodes[0].Endpoint.Port; p != 6380 {
		t.Errorf("node Endpoint.Port = %d, want 6380 (custom port must reach nodes)", p)
	}

	// An explicit TLSEnabled=false and NumReplicasPerShard=0 must be preserved,
	// yielding a single node per shard and SingleAZ.
	out, err = client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
		ClusterName:         aws.String("explicit"),
		NodeType:            aws.String("db.r6g.large"),
		ACLName:             aws.String("open-access"),
		NumShards:           aws.Int32(2),
		NumReplicasPerShard: aws.Int32(0),
		TLSEnabled:          aws.Bool(false),
	})
	if err != nil {
		t.Fatalf("CreateCluster explicit: %v", err)
	}

	c = out.Cluster
	if aws.ToBool(c.TLSEnabled) {
		t.Errorf("explicit TLSEnabled=false not preserved")
	}

	if c.AvailabilityMode != mdbtypes.AZStatusSingleAZ {
		t.Errorf("AvailabilityMode = %q, want singleaz", c.AvailabilityMode)
	}

	if aws.ToInt32(c.Shards[0].NumberOfNodes) != 1 || len(c.Shards[0].Nodes) != 1 {
		t.Errorf("explicit 0 replicas: nodes/shard = %d, want 1", aws.ToInt32(c.Shards[0].NumberOfNodes))
	}
}

func TestSDKReservedNodeDuplicateFault(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	offers, err := client.DescribeReservedNodesOfferings(ctx, &awsmemorydb.DescribeReservedNodesOfferingsInput{})
	if err != nil {
		t.Fatalf("DescribeReservedNodesOfferings: %v", err)
	}

	in := &awsmemorydb.PurchaseReservedNodesOfferingInput{
		ReservedNodesOfferingId: offers.ReservedNodesOfferings[0].ReservedNodesOfferingId,
		ReservationId:           aws.String("dup-res"),
	}

	if _, err := client.PurchaseReservedNodesOffering(ctx, in); err != nil {
		t.Fatalf("first purchase: %v", err)
	}

	// Second purchase with the same ReservationId must surface the SDK-modeled
	// ReservedNodeAlreadyExistsFault (not the unmodeled *Offering* variant).
	_, err = client.PurchaseReservedNodesOffering(ctx, in)

	var dup *mdbtypes.ReservedNodeAlreadyExistsFault
	if !errors.As(err, &dup) {
		t.Fatalf("duplicate purchase: got %v, want ReservedNodeAlreadyExistsFault", err)
	}
}

func TestSDKPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	for _, n := range []string{"c1", "c2", "c3"} {
		if _, err := client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
			ClusterName: aws.String(n), NodeType: aws.String("db.r6g.large"),
			ACLName: aws.String("open-access"), NumShards: aws.Int32(1),
		}); err != nil {
			t.Fatalf("CreateCluster %s: %v", n, err)
		}
	}

	first, err := client.DescribeClusters(ctx, &awsmemorydb.DescribeClustersInput{MaxResults: aws.Int32(2)})
	if err != nil {
		t.Fatalf("DescribeClusters page 1: %v", err)
	}

	if len(first.Clusters) != 2 || first.NextToken == nil {
		t.Fatalf("page 1: got %d clusters, token=%v; want 2 + token", len(first.Clusters), first.NextToken)
	}

	second, err := client.DescribeClusters(ctx, &awsmemorydb.DescribeClustersInput{
		MaxResults: aws.Int32(2), NextToken: first.NextToken,
	})
	if err != nil {
		t.Fatalf("DescribeClusters page 2: %v", err)
	}

	if len(second.Clusters) != 1 || second.NextToken != nil {
		t.Fatalf("page 2: got %d clusters, token=%v; want 1 + no token", len(second.Clusters), second.NextToken)
	}

	// A malformed token is rejected.
	_, err = client.DescribeClusters(ctx, &awsmemorydb.DescribeClustersInput{NextToken: aws.String("!!not-base64!!")})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("bad token: got %v, want InvalidParameterValueException", err)
	}
}

func TestSDKServiceUpdates(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
		ClusterName: aws.String("su"), NodeType: aws.String("db.r6g.large"),
		ACLName: aws.String("open-access"), NumShards: aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	updates, err := client.DescribeServiceUpdates(ctx, &awsmemorydb.DescribeServiceUpdatesInput{})
	if err != nil {
		t.Fatalf("DescribeServiceUpdates: %v", err)
	}

	if len(updates.ServiceUpdates) != 1 || aws.ToString(updates.ServiceUpdates[0].ClusterName) != "su" {
		t.Fatalf("service updates wrong: %+v", updates.ServiceUpdates)
	}

	batch, err := client.BatchUpdateCluster(ctx, &awsmemorydb.BatchUpdateClusterInput{
		ClusterNames: []string{"su", "ghost"},
	})
	if err != nil {
		t.Fatalf("BatchUpdateCluster: %v", err)
	}

	if len(batch.ProcessedClusters) != 1 || len(batch.UnprocessedClusters) != 1 {
		t.Fatalf("batch: processed=%d unprocessed=%d; want 1/1", len(batch.ProcessedClusters), len(batch.UnprocessedClusters))
	}

	if aws.ToString(batch.UnprocessedClusters[0].ErrorType) != "ClusterNotFoundFault" {
		t.Fatalf("unprocessed error type = %q", aws.ToString(batch.UnprocessedClusters[0].ErrorType))
	}
}

func TestSDKUnknownOperation(t *testing.T) {
	client := newSDKClient(t)

	_, err := client.DescribeClusters(context.Background(), &awsmemorydb.DescribeClustersInput{
		ClusterName: aws.String("nope"),
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ClusterNotFoundFault" {
		t.Fatalf("Describe(missing): got %v, want ClusterNotFoundFault", err)
	}
}
