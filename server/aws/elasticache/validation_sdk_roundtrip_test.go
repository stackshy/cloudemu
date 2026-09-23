package elasticache_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/smithy-go"
)

func requireCode(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != want {
		t.Fatalf("err = %v, want code %s", err, want)
	}
}

func TestSDKCreateCacheClusterValidation(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("c1"), Engine: aws.String("mysql"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
	})
	requireCode(t, err, "InvalidParameterValue")

	_, err = client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("m1"), Engine: aws.String("memcached"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(0),
	})
	requireCode(t, err, "InvalidParameterValue")

	_, err = client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("c2"), Engine: aws.String("redis"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
		CacheParameterGroupName: aws.String("totally-does-not-exist"),
	})
	requireCode(t, err, "CacheParameterGroupNotFound")

	if _, err = client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("c3"), Engine: aws.String("valkey"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
		CacheParameterGroupName: aws.String("default.valkey8"),
	}); err != nil {
		t.Fatalf("valid create: %v", err)
	}

	_, err = client.ModifyCacheCluster(ctx, &awselasticache.ModifyCacheClusterInput{
		CacheClusterId: aws.String("c3"), NumCacheNodes: aws.Int32(0),
	})
	requireCode(t, err, "InvalidParameterValue")
}

func TestSDKCreateReplicationGroupValidation(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateReplicationGroup(ctx, &awselasticache.CreateReplicationGroupInput{
		ReplicationGroupId: aws.String("rg1"), ReplicationGroupDescription: aws.String("x"),
		Engine: aws.String("memcached"),
	})
	requireCode(t, err, "InvalidParameterValue")

	_, err = client.CreateReplicationGroup(ctx, &awselasticache.CreateReplicationGroupInput{
		ReplicationGroupId: aws.String("rg2"), ReplicationGroupDescription: aws.String("x"),
		NumCacheClusters: aws.Int32(0),
	})
	requireCode(t, err, "InvalidParameterValue")
}

func TestSDKDeleteCacheParameterGroupInUse(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCacheParameterGroup(ctx, &awselasticache.CreateCacheParameterGroupInput{
		CacheParameterGroupName: aws.String("pg"), CacheParameterGroupFamily: aws.String("redis7"),
		Description: aws.String("x"),
	}); err != nil {
		t.Fatalf("CreateCacheParameterGroup: %v", err)
	}

	if _, err := client.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("c1"), Engine: aws.String("redis"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
		CacheParameterGroupName: aws.String("pg"),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	_, err := client.DeleteCacheParameterGroup(ctx, &awselasticache.DeleteCacheParameterGroupInput{
		CacheParameterGroupName: aws.String("pg"),
	})
	requireCode(t, err, "InvalidCacheParameterGroupState")

	out, err := client.DescribeCacheParameterGroups(ctx, &awselasticache.DescribeCacheParameterGroupsInput{
		CacheParameterGroupName: aws.String("default.redis7"),
	})
	if err != nil || len(out.CacheParameterGroups) != 1 {
		t.Fatalf("describe default.redis7 = %+v, %v", out, err)
	}
}
