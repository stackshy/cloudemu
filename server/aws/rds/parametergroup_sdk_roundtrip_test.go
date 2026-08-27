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

	// The full engine-default set is returned with the modification overlaid.
	mc := findSDKParam(params.Parameters, "max_connections")
	if mc == nil || aws.ToString(mc.ParameterValue) != "200" || aws.ToString(mc.Source) != "user" {
		t.Fatalf("max_connections = %+v, want value 200 source user", mc)
	}
	if len(params.Parameters) < 10 {
		t.Fatalf("got %d params, want the full engine-default set", len(params.Parameters))
	}

	if _, err := client.ResetDBParameterGroup(ctx, &awsrds.ResetDBParameterGroupInput{
		DBParameterGroupName: aws.String("pg-1"),
		ResetAllParameters:   aws.Bool(true),
	}); err != nil {
		t.Fatalf("ResetDBParameterGroup: %v", err)
	}

	// After reset-all the engine defaults remain, but nothing is user-sourced.
	after, _ := client.DescribeDBParameters(ctx, &awsrds.DescribeDBParametersInput{
		DBParameterGroupName: aws.String("pg-1"),
	})
	if len(after.Parameters) == 0 {
		t.Fatal("after reset-all: engine defaults should still be returned")
	}
	for i := range after.Parameters {
		if aws.ToString(after.Parameters[i].Source) == "user" {
			t.Fatalf("after reset-all, %s is still user-sourced", aws.ToString(after.Parameters[i].ParameterName))
		}
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

	if cs := findSDKParam(params.Parameters, "character_set_server"); cs == nil ||
		aws.ToString(cs.ParameterValue) != "utf8mb4" || aws.ToString(cs.Source) != "user" {
		t.Fatalf("cluster params missing the modification: got %d", len(params.Parameters))
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

func findSDKParam(params []awsrdstypes.Parameter, name string) *awsrdstypes.Parameter {
	for i := range params {
		if aws.ToString(params[i].ParameterName) == name {
			return &params[i]
		}
	}

	return nil
}

// TestSDKRDSDescribeDefaultParameters pins that DescribeDBParameters on the
// always-present default parameter group (never explicitly created) returns the
// engine-default set — real RDS never returns an empty parameter list.
func TestSDKRDSDescribeDefaultParameters(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.DescribeDBParameters(ctx, &awsrds.DescribeDBParametersInput{
		DBParameterGroupName: aws.String("default.mysql8.0"),
	})
	if err != nil {
		t.Fatalf("DescribeDBParameters(default.mysql8.0): %v", err)
	}

	if len(out.Parameters) < 10 {
		t.Fatalf("default group returned %d params, want the engine-default set", len(out.Parameters))
	}

	mc := findSDKParam(out.Parameters, "max_connections")
	if mc == nil || aws.ToString(mc.Source) != "engine-default" || aws.ToString(mc.ParameterValue) == "" {
		t.Fatalf("max_connections in default group = %+v, want an engine-default value", mc)
	}
}
