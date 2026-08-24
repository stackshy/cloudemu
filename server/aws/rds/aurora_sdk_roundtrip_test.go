package rds_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

// TestSDKRDSFailoverEmptyCluster guards that failing a cluster over with no
// member instances is rejected as InvalidDBClusterStateFault, not silently
// accepted.
func TestSDKRDSFailoverEmptyCluster(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBCluster(ctx, &awsrds.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("empty-cl"),
		Engine:              aws.String("aurora-mysql"),
	}); err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	_, err := client.FailoverDBCluster(ctx, &awsrds.FailoverDBClusterInput{
		DBClusterIdentifier: aws.String("empty-cl"),
	})

	var state *rdstypes.InvalidDBClusterStateFault
	if !errors.As(err, &state) {
		t.Fatalf("failover empty cluster: got %v, want InvalidDBClusterStateFault", err)
	}
}

func TestSDKRDSClusterEndpointsAndFailover(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBCluster(ctx, &awsrds.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("cl"),
		Engine:              aws.String("aurora-mysql"),
	}); err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	ep, err := client.CreateDBClusterEndpoint(ctx, &awsrds.CreateDBClusterEndpointInput{
		DBClusterIdentifier:         aws.String("cl"),
		DBClusterEndpointIdentifier: aws.String("reader-ep"),
		EndpointType:                aws.String("READER"),
	})
	if err != nil {
		t.Fatalf("CreateDBClusterEndpoint: %v", err)
	}

	if aws.ToString(ep.EndpointType) != "CUSTOM" || aws.ToString(ep.CustomEndpointType) != "READER" {
		t.Fatalf("endpoint types wrong: EndpointType=%q CustomEndpointType=%q",
			aws.ToString(ep.EndpointType), aws.ToString(ep.CustomEndpointType))
	}

	desc, err := client.DescribeDBClusterEndpoints(ctx, &awsrds.DescribeDBClusterEndpointsInput{
		DBClusterIdentifier: aws.String("cl"),
	})
	if err != nil {
		t.Fatalf("DescribeDBClusterEndpoints: %v", err)
	}

	// The built-in WRITER + READER endpoints Aurora auto-provisions plus the one
	// custom endpoint created above.
	types := map[string]int{}
	for i := range desc.DBClusterEndpoints {
		types[aws.ToString(desc.DBClusterEndpoints[i].EndpointType)]++
	}

	if types["WRITER"] != 1 || types["READER"] != 1 || types["CUSTOM"] != 1 {
		t.Fatalf("endpoint types = %v, want one each of WRITER/READER/CUSTOM", types)
	}

	if _, err := client.DeleteDBClusterEndpoint(ctx, &awsrds.DeleteDBClusterEndpointInput{
		DBClusterEndpointIdentifier: aws.String("reader-ep"),
	}); err != nil {
		t.Fatalf("DeleteDBClusterEndpoint: %v", err)
	}

	// Failover with a member instance.
	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("member"),
		DBClusterIdentifier:  aws.String("cl"),
		Engine:               aws.String("aurora-mysql"),
		DBInstanceClass:      aws.String("db.r5.large"),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	fo, err := client.FailoverDBCluster(ctx, &awsrds.FailoverDBClusterInput{
		DBClusterIdentifier: aws.String("cl"),
	})
	if err != nil {
		t.Fatalf("FailoverDBCluster: %v", err)
	}

	if aws.ToString(fo.DBCluster.DBClusterIdentifier) != "cl" {
		t.Fatalf("failover returned wrong cluster: %q", aws.ToString(fo.DBCluster.DBClusterIdentifier))
	}
}

func TestSDKRDSGlobalClusterLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBCluster(ctx, &awsrds.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("primary"),
		Engine:              aws.String("aurora-postgresql"),
	}); err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	src, err := client.DescribeDBClusters(ctx, &awsrds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String("primary"),
	})
	if err != nil {
		t.Fatalf("DescribeDBClusters: %v", err)
	}

	srcArn := aws.ToString(src.DBClusters[0].DBClusterArn)

	created, err := client.CreateGlobalCluster(ctx, &awsrds.CreateGlobalClusterInput{
		GlobalClusterIdentifier:   aws.String("global-1"),
		SourceDBClusterIdentifier: aws.String("primary"),
	})
	if err != nil {
		t.Fatalf("CreateGlobalCluster: %v", err)
	}

	if len(created.GlobalCluster.GlobalClusterMembers) != 1 {
		t.Fatalf("global members = %d, want 1", len(created.GlobalCluster.GlobalClusterMembers))
	}

	desc, err := client.DescribeGlobalClusters(ctx, &awsrds.DescribeGlobalClustersInput{})
	if err != nil {
		t.Fatalf("DescribeGlobalClusters: %v", err)
	}

	if len(desc.GlobalClusters) != 1 {
		t.Fatalf("got %d global clusters, want 1", len(desc.GlobalClusters))
	}

	if _, err := client.RemoveFromGlobalCluster(ctx, &awsrds.RemoveFromGlobalClusterInput{
		GlobalClusterIdentifier: aws.String("global-1"),
		DbClusterIdentifier:     aws.String(srcArn),
	}); err != nil {
		t.Fatalf("RemoveFromGlobalCluster: %v", err)
	}

	if _, err := client.DeleteGlobalCluster(ctx, &awsrds.DeleteGlobalClusterInput{
		GlobalClusterIdentifier: aws.String("global-1"),
	}); err != nil {
		t.Fatalf("DeleteGlobalCluster: %v", err)
	}
}
