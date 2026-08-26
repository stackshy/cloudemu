package redshift_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
	"github.com/aws/smithy-go"
)

// TestSDKRedshiftCreateClusterMissingSubnetGroup proves CreateCluster rejects a
// reference to a non-existent cluster subnet group with
// ClusterSubnetGroupNotFoundFault instead of silently succeeding — masking an
// IaC ordering error (cluster created before its subnet group).
func TestSDKRedshiftCreateClusterMissingSubnetGroup(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateCluster(ctx, &awsredshift.CreateClusterInput{
		ClusterIdentifier:      aws.String("c1"),
		MasterUsername:         aws.String("admin"),
		MasterUserPassword:     aws.String("Sup3rSecret!"),
		NodeType:               aws.String("ra3.xlplus"),
		ClusterSubnetGroupName: aws.String("does-not-exist"),
	})
	if err == nil {
		t.Fatal("expected ClusterSubnetGroupNotFoundFault, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ClusterSubnetGroupNotFoundFault" {
		t.Fatalf("ErrorCode = %v, want ClusterSubnetGroupNotFoundFault", err)
	}
}
