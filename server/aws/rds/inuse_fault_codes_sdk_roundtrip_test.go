package rds_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

// TestSDKRDSDeleteParameterGroupInUseFault asserts that deleting a parameter
// group still attached to a DB instance surfaces the typed
// InvalidDBParameterGroupStateFault (not InvalidDBInstanceState).
func TestSDKRDSDeleteParameterGroupInUseFault(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBParameterGroup(ctx, &awsrds.CreateDBParameterGroupInput{
		DBParameterGroupName:   aws.String("mypg"),
		DBParameterGroupFamily: aws.String("mysql8.0"),
		Description:            aws.String("test pg"),
	}); err != nil {
		t.Fatalf("CreateDBParameterGroup: %v", err)
	}

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("pg-user-db"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
		DBParameterGroupName: aws.String("mypg"),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	_, err := client.DeleteDBParameterGroup(ctx, &awsrds.DeleteDBParameterGroupInput{
		DBParameterGroupName: aws.String("mypg"),
	})
	if err == nil {
		t.Fatal("delete in-use parameter group: want InvalidDBParameterGroupStateFault, got nil")
	}

	var state *rdstypes.InvalidDBParameterGroupStateFault
	if !errors.As(err, &state) {
		t.Fatalf("delete in-use parameter group: got %v, want InvalidDBParameterGroupStateFault", err)
	}
}

// TestSDKRDSDeleteSubnetGroupInUseFault asserts that deleting a subnet group
// still used by a DB instance surfaces InvalidDBSubnetGroupStateFault.
func TestSDKRDSDeleteSubnetGroupInUseFault(t *testing.T) {
	ctx := context.Background()
	rdsc, ec2c := newSubnetGroupClients(t)
	_, subnetIDs := mkSubnets(t, ec2c)

	if _, err := rdsc.CreateDBSubnetGroup(ctx, &awsrds.CreateDBSubnetGroupInput{
		DBSubnetGroupName:        aws.String("aud-sng"),
		DBSubnetGroupDescription: aws.String("test group"),
		SubnetIds:                subnetIDs,
	}); err != nil {
		t.Fatalf("CreateDBSubnetGroup: %v", err)
	}

	if _, err := rdsc.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("sng-user"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
		DBSubnetGroupName:    aws.String("aud-sng"),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	_, err := rdsc.DeleteDBSubnetGroup(ctx, &awsrds.DeleteDBSubnetGroupInput{
		DBSubnetGroupName: aws.String("aud-sng"),
	})
	if err == nil {
		t.Fatal("delete in-use subnet group: want InvalidDBSubnetGroupStateFault, got nil")
	}

	var state *rdstypes.InvalidDBSubnetGroupStateFault
	if !errors.As(err, &state) {
		t.Fatalf("delete in-use subnet group: got %v, want InvalidDBSubnetGroupStateFault", err)
	}
}

// TestSDKRDSDeleteOptionGroupInUseFault asserts that deleting an option group
// still associated with a DB instance surfaces InvalidOptionGroupStateFault.
func TestSDKRDSDeleteOptionGroupInUseFault(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateOptionGroup(ctx, &awsrds.CreateOptionGroupInput{
		OptionGroupName:        aws.String("aud-og"),
		EngineName:             aws.String("mysql"),
		MajorEngineVersion:     aws.String("8.0"),
		OptionGroupDescription: aws.String("test og"),
	}); err != nil {
		t.Fatalf("CreateOptionGroup: %v", err)
	}

	if _, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("og-user"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
		OptionGroupName:      aws.String("aud-og"),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	_, err := client.DeleteOptionGroup(ctx, &awsrds.DeleteOptionGroupInput{
		OptionGroupName: aws.String("aud-og"),
	})
	if err == nil {
		t.Fatal("delete in-use option group: want InvalidOptionGroupStateFault, got nil")
	}

	var state *rdstypes.InvalidOptionGroupStateFault
	if !errors.As(err, &state) {
		t.Fatalf("delete in-use option group: got %v, want InvalidOptionGroupStateFault", err)
	}
}
