package elasticache_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
)

func TestSDKReplicationGroupParameterGroup(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	for _, name := range []string{"pg1", "pg2"} {
		if _, err := client.CreateCacheParameterGroup(ctx, &awselasticache.CreateCacheParameterGroupInput{
			CacheParameterGroupName: aws.String(name), CacheParameterGroupFamily: aws.String("redis7"),
			Description: aws.String("x"),
		}); err != nil {
			t.Fatalf("CreateCacheParameterGroup %s: %v", name, err)
		}
	}

	_, err := client.CreateReplicationGroup(ctx, &awselasticache.CreateReplicationGroupInput{
		ReplicationGroupId: aws.String("rg0"), ReplicationGroupDescription: aws.String("x"),
		CacheParameterGroupName: aws.String("nope"),
	})
	requireCode(t, err, "CacheParameterGroupNotFound")

	if _, err = client.CreateReplicationGroup(ctx, &awselasticache.CreateReplicationGroupInput{
		ReplicationGroupId: aws.String("rg1"), ReplicationGroupDescription: aws.String("x"),
		CacheParameterGroupName: aws.String("pg1"),
	}); err != nil {
		t.Fatalf("CreateReplicationGroup: %v", err)
	}

	requireMemberGroup(t, client, "rg1-001", "pg1")

	_, err = client.DeleteCacheParameterGroup(ctx, &awselasticache.DeleteCacheParameterGroupInput{
		CacheParameterGroupName: aws.String("pg1"),
	})
	requireCode(t, err, "InvalidCacheParameterGroupState")

	_, err = client.ModifyReplicationGroup(ctx, &awselasticache.ModifyReplicationGroupInput{
		ReplicationGroupId: aws.String("rg1"), CacheParameterGroupName: aws.String("nope"),
	})
	requireCode(t, err, "CacheParameterGroupNotFound")

	if _, err = client.ModifyReplicationGroup(ctx, &awselasticache.ModifyReplicationGroupInput{
		ReplicationGroupId: aws.String("rg1"), CacheParameterGroupName: aws.String("pg2"),
	}); err != nil {
		t.Fatalf("ModifyReplicationGroup: %v", err)
	}

	requireMemberGroup(t, client, "rg1-001", "pg2")

	if _, err = client.DeleteCacheParameterGroup(ctx, &awselasticache.DeleteCacheParameterGroupInput{
		CacheParameterGroupName: aws.String("pg1"),
	}); err != nil {
		t.Fatalf("delete pg1 after modify: %v", err)
	}
}

func requireMemberGroup(t *testing.T, client *awselasticache.Client, member, want string) {
	t.Helper()

	out, err := client.DescribeCacheClusters(context.Background(), &awselasticache.DescribeCacheClustersInput{
		CacheClusterId: aws.String(member),
	})
	if err != nil || len(out.CacheClusters) != 1 || out.CacheClusters[0].CacheParameterGroup == nil {
		t.Fatalf("DescribeCacheClusters %s = %+v, %v", member, out, err)
	}

	if got := aws.ToString(out.CacheClusters[0].CacheParameterGroup.CacheParameterGroupName); got != want {
		t.Fatalf("%s parameter group = %q, want %q", member, got, want)
	}
}

func TestSDKDescribeCacheParameterGroupsListsDefaults(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCacheParameterGroup(ctx, &awselasticache.CreateCacheParameterGroupInput{
		CacheParameterGroupName: aws.String("mine"), CacheParameterGroupFamily: aws.String("redis7"),
		Description: aws.String("x"),
	}); err != nil {
		t.Fatalf("CreateCacheParameterGroup: %v", err)
	}

	out, err := client.DescribeCacheParameterGroups(ctx, &awselasticache.DescribeCacheParameterGroupsInput{})
	if err != nil {
		t.Fatalf("DescribeCacheParameterGroups: %v", err)
	}

	names := map[string]bool{}
	for _, g := range out.CacheParameterGroups {
		names[aws.ToString(g.CacheParameterGroupName)] = true
	}

	for _, want := range []string{"mine", "default.redis7", "default.redis7.cluster.on", "default.memcached1.6", "default.valkey8"} {
		if !names[want] {
			t.Errorf("list is missing %s", want)
		}
	}

	if names["default.memcached1.6.cluster.on"] {
		t.Errorf("memcached has no cluster.on default group")
	}
}
