package elasticache_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"

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
		CacheSubnetGroupName:        aws.String("zop-cache-sng"),
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
			CacheSubnetGroupName: aws.String("zop-cache-sng"),
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
