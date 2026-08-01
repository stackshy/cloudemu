package rds_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	awsrdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

func TestSDKRDSParameterGroupLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateDBParameterGroup(ctx, &awsrds.CreateDBParameterGroupInput{
		DBParameterGroupName:   aws.String("pg-1"),
		DBParameterGroupFamily: aws.String("mysql8.0"),
		Description:            aws.String("app params"),
	})
	if err != nil {
		t.Fatalf("CreateDBParameterGroup: %v", err)
	}

	if aws.ToString(created.DBParameterGroup.DBParameterGroupArn) == "" {
		t.Error("expected parameter group ARN")
	}

	desc, err := client.DescribeDBParameterGroups(ctx, &awsrds.DescribeDBParameterGroupsInput{})
	if err != nil {
		t.Fatalf("DescribeDBParameterGroups: %v", err)
	}

	if len(desc.DBParameterGroups) != 1 {
		t.Fatalf("got %d groups, want 1", len(desc.DBParameterGroups))
	}

	if _, err := client.ModifyDBParameterGroup(ctx, &awsrds.ModifyDBParameterGroupInput{
		DBParameterGroupName: aws.String("pg-1"),
		Parameters: []awsrdstypes.Parameter{
			{ParameterName: aws.String("max_connections"), ParameterValue: aws.String("200"), ApplyMethod: awsrdstypes.ApplyMethodPendingReboot},
		},
	}); err != nil {
		t.Fatalf("ModifyDBParameterGroup: %v", err)
	}

	params, err := client.DescribeDBParameters(ctx, &awsrds.DescribeDBParametersInput{
		DBParameterGroupName: aws.String("pg-1"),
	})
	if err != nil {
		t.Fatalf("DescribeDBParameters: %v", err)
	}

	if len(params.Parameters) != 1 || aws.ToString(params.Parameters[0].ParameterName) != "max_connections" {
		t.Fatalf("unexpected params: %+v", params.Parameters)
	}

	if _, err := client.ResetDBParameterGroup(ctx, &awsrds.ResetDBParameterGroupInput{
		DBParameterGroupName: aws.String("pg-1"),
		ResetAllParameters:   aws.Bool(true),
	}); err != nil {
		t.Fatalf("ResetDBParameterGroup: %v", err)
	}

	if params, _ := client.DescribeDBParameters(ctx, &awsrds.DescribeDBParametersInput{
		DBParameterGroupName: aws.String("pg-1"),
	}); len(params.Parameters) != 0 {
		t.Fatalf("after reset-all: got %d params, want 0", len(params.Parameters))
	}

	copied, err := client.CopyDBParameterGroup(ctx, &awsrds.CopyDBParameterGroupInput{
		SourceDBParameterGroupIdentifier:  aws.String("pg-1"),
		TargetDBParameterGroupIdentifier:  aws.String("pg-2"),
		TargetDBParameterGroupDescription: aws.String("copy"),
	})
	if err != nil {
		t.Fatalf("CopyDBParameterGroup: %v", err)
	}

	if aws.ToString(copied.DBParameterGroup.DBParameterGroupName) != "pg-2" {
		t.Fatalf("copy name = %q, want pg-2", aws.ToString(copied.DBParameterGroup.DBParameterGroupName))
	}

	if _, err := client.DeleteDBParameterGroup(ctx, &awsrds.DeleteDBParameterGroupInput{
		DBParameterGroupName: aws.String("pg-1"),
	}); err != nil {
		t.Fatalf("DeleteDBParameterGroup: %v", err)
	}
}

func TestSDKRDSClusterParameterGroupLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBClusterParameterGroup(ctx, &awsrds.CreateDBClusterParameterGroupInput{
		DBClusterParameterGroupName: aws.String("cpg-1"),
		DBParameterGroupFamily:      aws.String("aurora-mysql8.0"),
		Description:                 aws.String("cluster params"),
	}); err != nil {
		t.Fatalf("CreateDBClusterParameterGroup: %v", err)
	}

	if _, err := client.ModifyDBClusterParameterGroup(ctx, &awsrds.ModifyDBClusterParameterGroupInput{
		DBClusterParameterGroupName: aws.String("cpg-1"),
		Parameters: []awsrdstypes.Parameter{
			{ParameterName: aws.String("character_set_server"), ParameterValue: aws.String("utf8mb4")},
		},
	}); err != nil {
		t.Fatalf("ModifyDBClusterParameterGroup: %v", err)
	}

	params, err := client.DescribeDBClusterParameters(ctx, &awsrds.DescribeDBClusterParametersInput{
		DBClusterParameterGroupName: aws.String("cpg-1"),
	})
	if err != nil {
		t.Fatalf("DescribeDBClusterParameters: %v", err)
	}

	if len(params.Parameters) != 1 {
		t.Fatalf("got %d cluster params, want 1", len(params.Parameters))
	}

	desc, err := client.DescribeDBClusterParameterGroups(ctx, &awsrds.DescribeDBClusterParameterGroupsInput{})
	if err != nil {
		t.Fatalf("DescribeDBClusterParameterGroups: %v", err)
	}

	if len(desc.DBClusterParameterGroups) != 1 {
		t.Fatalf("got %d cluster groups, want 1", len(desc.DBClusterParameterGroups))
	}

	if _, err := client.DeleteDBClusterParameterGroup(ctx, &awsrds.DeleteDBClusterParameterGroupInput{
		DBClusterParameterGroupName: aws.String("cpg-1"),
	}); err != nil {
		t.Fatalf("DeleteDBClusterParameterGroup: %v", err)
	}
}
