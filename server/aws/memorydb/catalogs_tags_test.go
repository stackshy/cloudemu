package memorydb_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmemorydb "github.com/aws/aws-sdk-go-v2/service/memorydb"
	mdbtypes "github.com/aws/aws-sdk-go-v2/service/memorydb/types"
)

// TestSDKListTagsReturnsCreateTimeTags guards that tags supplied at CreateCluster
// are addressable by ListTags(arn). They previously lived only on the cluster
// record and stayed invisible to ListTags until a later TagResource call.
func TestSDKListTagsReturnsCreateTimeTags(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateCluster(ctx, &awsmemorydb.CreateClusterInput{
		ClusterName: aws.String("tagged"),
		NodeType:    aws.String("db.r6g.large"),
		ACLName:     aws.String("open-access"),
		Tags: []mdbtypes.Tag{
			{Key: aws.String("env"), Value: aws.String("prod")},
			{Key: aws.String("team"), Value: aws.String("orders")},
		},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	arn := aws.ToString(created.Cluster.ARN)
	if arn == "" {
		t.Fatal("CreateCluster returned empty ARN")
	}

	out, err := client.ListTags(ctx, &awsmemorydb.ListTagsInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}

	got := map[string]string{}
	for _, tag := range out.TagList {
		got[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	if got["env"] != "prod" || got["team"] != "orders" {
		t.Fatalf("ListTags = %+v, want create-time tags env=prod team=orders", got)
	}
}
