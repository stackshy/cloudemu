package rds_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

// TestSDKRDSClusterRoleAssociation exercises AddRoleToDBCluster /
// RemoveRoleFromDBCluster end to end against the real aws-sdk-go-v2 client and
// verifies the associations surface in DescribeDBClusters.AssociatedRoles.
func TestSDKRDSClusterRoleAssociation(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBCluster(ctx, &awsrds.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("roles-cl"),
		Engine:              aws.String("aurora-mysql"),
	}); err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	const s3Role = "arn:aws:iam::123456789012:role/s3-import"

	if _, err := client.AddRoleToDBCluster(ctx, &awsrds.AddRoleToDBClusterInput{
		DBClusterIdentifier: aws.String("roles-cl"),
		RoleArn:             aws.String(s3Role),
		FeatureName:         aws.String("s3Import"),
	}); err != nil {
		t.Fatalf("AddRoleToDBCluster: %v", err)
	}

	desc, err := client.DescribeDBClusters(ctx, &awsrds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String("roles-cl"),
	})
	if err != nil {
		t.Fatalf("DescribeDBClusters: %v", err)
	}

	roles := desc.DBClusters[0].AssociatedRoles
	if len(roles) != 1 {
		t.Fatalf("AssociatedRoles = %+v, want one", roles)
	}

	if aws.ToString(roles[0].RoleArn) != s3Role ||
		aws.ToString(roles[0].FeatureName) != "s3Import" ||
		aws.ToString(roles[0].Status) != "ACTIVE" {
		t.Fatalf("role = %+v, want {%s s3Import ACTIVE}", roles[0], s3Role)
	}

	// Re-adding the same ARN is rejected.
	_, err = client.AddRoleToDBCluster(ctx, &awsrds.AddRoleToDBClusterInput{
		DBClusterIdentifier: aws.String("roles-cl"),
		RoleArn:             aws.String(s3Role),
	})

	var already *rdstypes.DBClusterRoleAlreadyExistsFault
	if !errors.As(err, &already) {
		t.Fatalf("duplicate add: got %v, want DBClusterRoleAlreadyExistsFault", err)
	}

	// Remove and confirm the list is empty again.
	if _, err := client.RemoveRoleFromDBCluster(ctx, &awsrds.RemoveRoleFromDBClusterInput{
		DBClusterIdentifier: aws.String("roles-cl"),
		RoleArn:             aws.String(s3Role),
	}); err != nil {
		t.Fatalf("RemoveRoleFromDBCluster: %v", err)
	}

	desc, err = client.DescribeDBClusters(ctx, &awsrds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String("roles-cl"),
	})
	if err != nil {
		t.Fatalf("DescribeDBClusters after remove: %v", err)
	}

	if len(desc.DBClusters[0].AssociatedRoles) != 0 {
		t.Fatalf("AssociatedRoles after remove = %+v, want empty", desc.DBClusters[0].AssociatedRoles)
	}

	// Removing an unassociated role reports DBClusterRoleNotFound.
	_, err = client.RemoveRoleFromDBCluster(ctx, &awsrds.RemoveRoleFromDBClusterInput{
		DBClusterIdentifier: aws.String("roles-cl"),
		RoleArn:             aws.String(s3Role),
	})

	var notFound *rdstypes.DBClusterRoleNotFoundFault
	if !errors.As(err, &notFound) {
		t.Fatalf("remove unassociated: got %v, want DBClusterRoleNotFoundFault", err)
	}
}

// TestSDKRDSAddRoleUnknownCluster verifies an add against a missing cluster is a
// typed DBClusterNotFoundFault.
func TestSDKRDSAddRoleUnknownCluster(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.AddRoleToDBCluster(ctx, &awsrds.AddRoleToDBClusterInput{
		DBClusterIdentifier: aws.String("ghost"),
		RoleArn:             aws.String("arn:aws:iam::123456789012:role/r"),
	})

	var notFound *rdstypes.DBClusterNotFoundFault
	if !errors.As(err, &notFound) {
		t.Fatalf("add on missing cluster: got %v, want DBClusterNotFoundFault", err)
	}
}
