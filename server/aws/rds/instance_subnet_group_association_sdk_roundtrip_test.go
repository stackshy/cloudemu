package rds_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

// A DB instance placed in a subnet group must report that association back as
// the nested complex DBInstance.DBSubnetGroup element real RDS returns — not a
// scalar DBSubnetGroupName the SDK has no field to bind. Terraform and the
// aws-sdk-go-v2 rdstypes.DBInstance read db_subnet_group_name off
// DBSubnetGroup.DBSubnetGroupName, so a nil DBSubnetGroup silently drops the
// association the caller just set.
func TestDBInstanceSubnetGroupAssociationRoundTrip(t *testing.T) {
	ctx := context.Background()
	rdsc, ec2c := newSubnetGroupClients(t)
	vpcID, subnetIDs := mkSubnets(t, ec2c)

	if _, err := rdsc.CreateDBSubnetGroup(ctx, &awsrds.CreateDBSubnetGroupInput{
		DBSubnetGroupName:        aws.String("app-db-subnets"),
		DBSubnetGroupDescription: aws.String("private db subnets"),
		SubnetIds:                subnetIDs,
	}); err != nil {
		t.Fatalf("CreateDBSubnetGroup: %v", err)
	}

	created, err := rdsc.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("app-db"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		Engine:               aws.String("postgres"),
		MasterUsername:       aws.String("admin"),
		MasterUserPassword:   aws.String("password123"),
		AllocatedStorage:     aws.Int32(20),
		DBSubnetGroupName:    aws.String("app-db-subnets"),
	})
	if err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	assertInstanceSubnetGroup(t, "CreateDBInstance", created.DBInstance.DBSubnetGroup, vpcID, len(subnetIDs))

	described, err := rdsc.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("app-db"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	if len(described.DBInstances) != 1 {
		t.Fatalf("describe = %d instances, want 1", len(described.DBInstances))
	}

	assertInstanceSubnetGroup(t, "DescribeDBInstances", described.DBInstances[0].DBSubnetGroup, vpcID, len(subnetIDs))
}

func assertInstanceSubnetGroup(t *testing.T, op string, sg *rdstypes.DBSubnetGroup, vpcID string, wantSubnets int) {
	t.Helper()

	if sg == nil {
		t.Fatalf("%s: DBInstance.DBSubnetGroup is nil — the association was dropped", op)
	}

	if got := aws.ToString(sg.DBSubnetGroupName); got != "app-db-subnets" {
		t.Errorf("%s: DBSubnetGroupName = %q, want app-db-subnets", op, got)
	}

	if got := aws.ToString(sg.VpcId); got != vpcID {
		t.Errorf("%s: DBSubnetGroup.VpcId = %q, want %q", op, got, vpcID)
	}

	if n := len(sg.Subnets); n != wantSubnets {
		t.Errorf("%s: DBSubnetGroup.Subnets = %d, want %d", op, n, wantSubnets)
	}
}
