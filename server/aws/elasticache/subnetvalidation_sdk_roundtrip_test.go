package elasticache_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
)

// TestSDKCreateCacheClusterMissingSubnetGroup covers that creating a cluster
// against a non-existent cache subnet group is rejected with
// CacheSubnetGroupNotFoundFault, while an existing group succeeds.
func TestSDKCreateCacheClusterMissingSubnetGroup(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("cs"), Engine: aws.String("redis"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
		CacheSubnetGroupName: aws.String("does-not-exist"),
	})
	if err == nil {
		t.Fatal("CreateCacheCluster with missing subnet group succeeded, want error")
	}

	var notFound *ectypes.CacheSubnetGroupNotFoundFault
	if !errors.As(err, &notFound) {
		t.Fatalf("error = %v, want CacheSubnetGroupNotFoundFault", err)
	}

	// An existing subnet group is accepted.
	if _, err := client.CreateCacheSubnetGroup(ctx, &awselasticache.CreateCacheSubnetGroupInput{
		CacheSubnetGroupName:        aws.String("sg1"),
		CacheSubnetGroupDescription: aws.String("test"),
		SubnetIds:                   []string{"subnet-123"},
	}); err != nil {
		t.Fatalf("CreateCacheSubnetGroup: %v", err)
	}

	if _, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("cs2"), Engine: aws.String("redis"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
		CacheSubnetGroupName: aws.String("sg1"),
	}); err != nil {
		t.Fatalf("CreateCacheCluster with existing subnet group: %v", err)
	}
}
