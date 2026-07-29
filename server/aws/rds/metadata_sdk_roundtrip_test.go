package rds_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	awsrdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

func TestSDKRDSMetadata(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	ev, err := client.DescribeDBEngineVersions(ctx, &awsrds.DescribeDBEngineVersionsInput{
		Engine: aws.String("mysql"),
	})
	if err != nil {
		t.Fatalf("DescribeDBEngineVersions: %v", err)
	}

	if len(ev.DBEngineVersions) == 0 {
		t.Fatal("expected mysql engine versions")
	}

	oo, err := client.DescribeOrderableDBInstanceOptions(ctx, &awsrds.DescribeOrderableDBInstanceOptionsInput{
		Engine: aws.String("postgres"),
	})
	if err != nil {
		t.Fatalf("DescribeOrderableDBInstanceOptions: %v", err)
	}

	if len(oo.OrderableDBInstanceOptions) == 0 {
		t.Fatal("expected orderable options for postgres")
	}
}

func TestSDKRDSTagging(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("db"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
	})
	if err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	arn := aws.ToString(created.DBInstance.DBInstanceArn)

	if _, err := client.AddTagsToResource(ctx, &awsrds.AddTagsToResourceInput{
		ResourceName: aws.String(arn),
		Tags: []awsrdstypes.Tag{
			{Key: aws.String("env"), Value: aws.String("prod")},
			{Key: aws.String("team"), Value: aws.String("data")},
		},
	}); err != nil {
		t.Fatalf("AddTagsToResource: %v", err)
	}

	listed, err := client.ListTagsForResource(ctx, &awsrds.ListTagsForResourceInput{
		ResourceName: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(listed.TagList) != 2 {
		t.Fatalf("got %d tags, want 2", len(listed.TagList))
	}

	if _, err := client.RemoveTagsFromResource(ctx, &awsrds.RemoveTagsFromResourceInput{
		ResourceName: aws.String(arn),
		TagKeys:      []string{"env"},
	}); err != nil {
		t.Fatalf("RemoveTagsFromResource: %v", err)
	}

	listed, err = client.ListTagsForResource(ctx, &awsrds.ListTagsForResourceInput{
		ResourceName: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(listed.TagList) != 1 || aws.ToString(listed.TagList[0].Key) != "team" {
		t.Fatalf("after remove: %+v", listed.TagList)
	}
}
