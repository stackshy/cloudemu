package rds_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
)

// TestSDKRDSClusterAttributes asserts a created DBCluster reports the
// descriptive attributes real Aurora returns — EngineMode, a resource id,
// AllocatedStorage, StorageEncrypted and the spread of AvailabilityZones.
func TestSDKRDSClusterAttributes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateDBCluster(ctx, &awsrds.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("attrs-cl"),
		Engine:              aws.String("aurora-mysql"),
		StorageEncrypted:    aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	cl := out.DBCluster

	if aws.ToString(cl.EngineMode) != "provisioned" {
		t.Fatalf("EngineMode=%q, want provisioned", aws.ToString(cl.EngineMode))
	}

	if aws.ToString(cl.DbClusterResourceId) == "" {
		t.Fatal("DbClusterResourceId empty; want a stable cluster- resource id")
	}

	if aws.ToInt32(cl.AllocatedStorage) != 1 {
		t.Fatalf("AllocatedStorage=%d, want 1", aws.ToInt32(cl.AllocatedStorage))
	}

	if !aws.ToBool(cl.StorageEncrypted) {
		t.Fatal("StorageEncrypted=false, want true")
	}

	if len(cl.AvailabilityZones) != 3 {
		t.Fatalf("AvailabilityZones=%v, want 3 zones", cl.AvailabilityZones)
	}
}
