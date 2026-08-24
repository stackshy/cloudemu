package elasticache_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newCacheSubnetGroupClients(t *testing.T) (*awselasticache.Client, *awsec2.Client) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(ts.URL)

	return awselasticache.NewFromConfig(cfg), awsec2.NewFromConfig(cfg)
}

// A VPC teardown lists cache subnet groups and deletes those whose VpcId
// matches the VPC going away. If VpcId came back empty the match would never
// fire and every cache subnet group would outlive its network.
func TestCacheSubnetGroupSDKRoundTrip(t *testing.T) {
	ctx := context.Background()
	cachec, ec2c := newCacheSubnetGroupClients(t)

	vpc, err := ec2c.CreateVpc(ctx, &awsec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	vpcID := aws.ToString(vpc.Vpc.VpcId)

	sub, err := ec2c.CreateSubnet(ctx, &awsec2.CreateSubnetInput{
		VpcId: aws.String(vpcID), CidrBlock: aws.String("10.0.1.0/24"),
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	subnetID := aws.ToString(sub.Subnet.SubnetId)

	created, err := cachec.CreateCacheSubnetGroup(ctx, &awselasticache.CreateCacheSubnetGroupInput{
		CacheSubnetGroupName:        aws.String("cache-sng-1"),
		CacheSubnetGroupDescription: aws.String("test"),
		SubnetIds:                   []string{subnetID},
	})
	if err != nil {
		t.Fatalf("CreateCacheSubnetGroup: %v", err)
	}

	if got := aws.ToString(created.CacheSubnetGroup.VpcId); got != vpcID {
		t.Errorf("VpcId = %q, want %q", got, vpcID)
	}

	listed, err := cachec.DescribeCacheSubnetGroups(ctx,
		&awselasticache.DescribeCacheSubnetGroupsInput{})
	if err != nil {
		t.Fatalf("DescribeCacheSubnetGroups: %v", err)
	}

	if len(listed.CacheSubnetGroups) != 1 {
		t.Fatalf("describe = %d groups, want 1", len(listed.CacheSubnetGroups))
	}

	if got := aws.ToString(listed.CacheSubnetGroups[0].VpcId); got != vpcID {
		t.Errorf("listed VpcId = %q, want %q", got, vpcID)
	}

	if _, err := cachec.DeleteCacheSubnetGroup(ctx,
		&awselasticache.DeleteCacheSubnetGroupInput{
			CacheSubnetGroupName: aws.String("cache-sng-1"),
		}); err != nil {
		t.Fatalf("DeleteCacheSubnetGroup: %v", err)
	}

	after, err := cachec.DescribeCacheSubnetGroups(ctx,
		&awselasticache.DescribeCacheSubnetGroupsInput{})
	if err != nil {
		t.Fatalf("describe after delete: %v", err)
	}

	if len(after.CacheSubnetGroups) != 0 {
		t.Errorf("group survived delete: %+v", after.CacheSubnetGroups)
	}
}

// TestDeleteCacheSubnetGroupInUseByCluster guards that a subnet group
// associated with a standalone cache cluster cannot be deleted — real
// ElastiCache refuses to delete a group associated with "any clusters", not
// only replication groups, and returns CacheSubnetGroupInUse.
func TestDeleteCacheSubnetGroupInUseByCluster(t *testing.T) {
	ctx := context.Background()
	cachec, ec2c := newCacheSubnetGroupClients(t)

	subnetID := makeSubnet(ctx, t, ec2c)

	if _, err := cachec.CreateCacheSubnetGroup(ctx, &awselasticache.CreateCacheSubnetGroupInput{
		CacheSubnetGroupName:        aws.String("sng-incluster"),
		CacheSubnetGroupDescription: aws.String("test"),
		SubnetIds:                   []string{subnetID},
	}); err != nil {
		t.Fatalf("CreateCacheSubnetGroup: %v", err)
	}

	if _, err := cachec.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
		CacheClusterId:       aws.String("incluster"),
		Engine:               aws.String("redis"),
		CacheNodeType:        aws.String("cache.t3.micro"),
		NumCacheNodes:        aws.Int32(1),
		CacheSubnetGroupName: aws.String("sng-incluster"),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	_, err := cachec.DeleteCacheSubnetGroup(ctx, &awselasticache.DeleteCacheSubnetGroupInput{
		CacheSubnetGroupName: aws.String("sng-incluster"),
	})
	if err == nil {
		t.Fatal("deleting a subnet group in use by a cache cluster should fail")
	}

	var inUse *ectypes.CacheSubnetGroupInUse
	if !errors.As(err, &inUse) {
		t.Fatalf("error = %v, want *CacheSubnetGroupInUse", err)
	}
}

// TestDeleteCacheSubnetGroupInUseErrorCode guards that the in-use error surfaces
// as the typed CacheSubnetGroupInUse fault (not InvalidCacheClusterState) so
// callers and Terraform can branch on it.
func TestDeleteCacheSubnetGroupInUseErrorCode(t *testing.T) {
	ctx := context.Background()
	cachec, ec2c := newCacheSubnetGroupClients(t)

	subnetID := makeSubnet(ctx, t, ec2c)

	if _, err := cachec.CreateCacheSubnetGroup(ctx, &awselasticache.CreateCacheSubnetGroupInput{
		CacheSubnetGroupName:        aws.String("sng-inrg"),
		CacheSubnetGroupDescription: aws.String("test"),
		SubnetIds:                   []string{subnetID},
	}); err != nil {
		t.Fatalf("CreateCacheSubnetGroup: %v", err)
	}

	if _, err := cachec.CreateReplicationGroup(ctx, &awselasticache.CreateReplicationGroupInput{
		ReplicationGroupId:          aws.String("rg-inrg"),
		ReplicationGroupDescription: aws.String("test"),
		Engine:                      aws.String("redis"),
		CacheNodeType:               aws.String("cache.t3.micro"),
		CacheSubnetGroupName:        aws.String("sng-inrg"),
	}); err != nil {
		t.Fatalf("CreateReplicationGroup: %v", err)
	}

	_, err := cachec.DeleteCacheSubnetGroup(ctx, &awselasticache.DeleteCacheSubnetGroupInput{
		CacheSubnetGroupName: aws.String("sng-inrg"),
	})
	if err == nil {
		t.Fatal("deleting a subnet group in use by a replication group should fail")
	}

	var inUse *ectypes.CacheSubnetGroupInUse
	if !errors.As(err, &inUse) {
		t.Fatalf("error = %v, want *CacheSubnetGroupInUse", err)
	}
}

// makeSubnet creates a VPC + subnet and returns the subnet id.
func makeSubnet(ctx context.Context, t *testing.T, ec2c *awsec2.Client) string {
	t.Helper()

	vpc, err := ec2c.CreateVpc(ctx, &awsec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	sub, err := ec2c.CreateSubnet(ctx, &awsec2.CreateSubnetInput{
		VpcId: vpc.Vpc.VpcId, CidrBlock: aws.String("10.0.1.0/24"),
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	return aws.ToString(sub.Subnet.SubnetId)
}

func TestDeleteUnknownCacheSubnetGroupFails(t *testing.T) {
	cachec, _ := newCacheSubnetGroupClients(t)

	_, err := cachec.DeleteCacheSubnetGroup(context.Background(),
		&awselasticache.DeleteCacheSubnetGroupInput{
			CacheSubnetGroupName: aws.String("nope"),
		})
	if err == nil {
		t.Error("deleting an unknown cache subnet group should fail")
	}
}
