package redshift_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"
	redshifttypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestSDKRedshiftSubnetGroupVpcAndSubnets proves CreateClusterSubnetGroup /
// DescribeClusterSubnetGroups return the derived VpcId and the full Subnets list
// (with availability zones). Without it a Terraform aws_redshift_subnet_group
// reads subnet_ids/vpc_id back empty and drifts on every plan.
func TestSDKRedshiftSubnetGroupVpcAndSubnets(t *testing.T) {
	ctx := context.Background()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.DriversFrom(cloud))
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.BaseEndpoint = aws.String(ts.URL)

	ec2c := awsec2.NewFromConfig(cfg)
	rsc := awsredshift.NewFromConfig(cfg)

	vpc, err := ec2c.CreateVpc(ctx, &awsec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	vpcID := aws.ToString(vpc.Vpc.VpcId)

	subnetAZs := map[string]string{"10.0.1.0/24": "us-east-1a", "10.0.2.0/24": "us-east-1b"}

	var subnetIDs []string
	for _, cidr := range []string{"10.0.1.0/24", "10.0.2.0/24"} {
		sub, err := ec2c.CreateSubnet(ctx, &awsec2.CreateSubnetInput{
			VpcId: aws.String(vpcID), CidrBlock: aws.String(cidr),
			AvailabilityZone: aws.String(subnetAZs[cidr]),
		})
		if err != nil {
			t.Fatalf("CreateSubnet(%s): %v", cidr, err)
		}
		subnetIDs = append(subnetIDs, aws.ToString(sub.Subnet.SubnetId))
	}

	created, err := rsc.CreateClusterSubnetGroup(ctx, &awsredshift.CreateClusterSubnetGroupInput{
		ClusterSubnetGroupName: aws.String("sg1"),
		Description:            aws.String("my sg"),
		SubnetIds:              subnetIDs,
	})
	if err != nil {
		t.Fatalf("CreateClusterSubnetGroup: %v", err)
	}

	assertSubnetGroup(t, "create", created.ClusterSubnetGroup, vpcID, subnetIDs)

	listed, err := rsc.DescribeClusterSubnetGroups(ctx, &awsredshift.DescribeClusterSubnetGroupsInput{
		ClusterSubnetGroupName: aws.String("sg1"),
	})
	if err != nil {
		t.Fatalf("DescribeClusterSubnetGroups: %v", err)
	}
	if len(listed.ClusterSubnetGroups) != 1 {
		t.Fatalf("got %d groups, want 1", len(listed.ClusterSubnetGroups))
	}

	assertSubnetGroup(t, "describe", &listed.ClusterSubnetGroups[0], vpcID, subnetIDs)
}

func assertSubnetGroup(t *testing.T, where string, g *redshifttypes.ClusterSubnetGroup, wantVpc string, wantSubnets []string) {
	t.Helper()

	if aws.ToString(g.VpcId) != wantVpc {
		t.Errorf("%s: VpcId = %q, want %q", where, aws.ToString(g.VpcId), wantVpc)
	}

	if len(g.Subnets) != len(wantSubnets) {
		t.Fatalf("%s: got %d subnets, want %d", where, len(g.Subnets), len(wantSubnets))
	}

	got := make(map[string]bool, len(g.Subnets))
	for _, s := range g.Subnets {
		got[aws.ToString(s.SubnetIdentifier)] = true
		if s.SubnetAvailabilityZone == nil || aws.ToString(s.SubnetAvailabilityZone.Name) == "" {
			t.Errorf("%s: subnet %q has no availability zone", where, aws.ToString(s.SubnetIdentifier))
		}
	}
	for _, id := range wantSubnets {
		if !got[id] {
			t.Errorf("%s: subnet %q missing from Subnets", where, id)
		}
	}
}
