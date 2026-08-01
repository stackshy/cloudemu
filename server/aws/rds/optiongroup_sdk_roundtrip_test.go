package rds_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	awsrdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

func TestSDKRDSOptionGroupLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateOptionGroup(ctx, &awsrds.CreateOptionGroupInput{
		OptionGroupName:        aws.String("og-1"),
		EngineName:             aws.String("mysql"),
		MajorEngineVersion:     aws.String("8.0"),
		OptionGroupDescription: aws.String("app options"),
	})
	if err != nil {
		t.Fatalf("CreateOptionGroup: %v", err)
	}

	if aws.ToString(created.OptionGroup.OptionGroupArn) == "" {
		t.Error("expected option group ARN")
	}

	if _, err := client.ModifyOptionGroup(ctx, &awsrds.ModifyOptionGroupInput{
		OptionGroupName: aws.String("og-1"),
		OptionsToInclude: []awsrdstypes.OptionConfiguration{
			{OptionName: aws.String("MARIADB_AUDIT_PLUGIN")},
		},
	}); err != nil {
		t.Fatalf("ModifyOptionGroup: %v", err)
	}

	desc, err := client.DescribeOptionGroups(ctx, &awsrds.DescribeOptionGroupsInput{
		OptionGroupName: aws.String("og-1"),
	})
	if err != nil {
		t.Fatalf("DescribeOptionGroups: %v", err)
	}

	if len(desc.OptionGroupsList) != 1 || len(desc.OptionGroupsList[0].Options) != 1 {
		t.Fatalf("unexpected describe: %+v", desc.OptionGroupsList)
	}

	copied, err := client.CopyOptionGroup(ctx, &awsrds.CopyOptionGroupInput{
		SourceOptionGroupIdentifier:  aws.String("og-1"),
		TargetOptionGroupIdentifier:  aws.String("og-2"),
		TargetOptionGroupDescription: aws.String("copy"),
	})
	if err != nil {
		t.Fatalf("CopyOptionGroup: %v", err)
	}

	if aws.ToString(copied.OptionGroup.OptionGroupName) != "og-2" {
		t.Fatalf("copy name = %q, want og-2", aws.ToString(copied.OptionGroup.OptionGroupName))
	}

	if _, err := client.DeleteOptionGroup(ctx, &awsrds.DeleteOptionGroupInput{
		OptionGroupName: aws.String("og-1"),
	}); err != nil {
		t.Fatalf("DeleteOptionGroup: %v", err)
	}
}

func TestSDKRDSDescribeOptionGroupOptions(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.DescribeOptionGroupOptions(ctx, &awsrds.DescribeOptionGroupOptionsInput{
		EngineName: aws.String("sqlserver-ee"),
	})
	if err != nil {
		t.Fatalf("DescribeOptionGroupOptions: %v", err)
	}

	if len(out.OptionGroupOptions) == 0 {
		t.Fatal("expected a non-empty option catalog for sqlserver-ee")
	}
}
