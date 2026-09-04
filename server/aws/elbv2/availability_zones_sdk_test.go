package elbv2_test

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newSDKClientsWithEC2 wires EC2 (for real subnets/AZs), VPC, and ELBv2 behind
// one server and returns clients for both, so a test can create subnets with
// a known AvailabilityZone and then point a load balancer at them.
func newSDKClientsWithEC2(t *testing.T) (*elb.Client, *awsec2.Client) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{ELB: cloud.ELB, EC2: cloud.EC2, VPC: cloud.VPC})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	elbClient := elb.NewFromConfig(cfg, func(o *elb.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ec2Client := awsec2.NewFromConfig(cfg, func(o *awsec2.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	return elbClient, ec2Client
}

// mkVPCAndSubnetsInZones creates a VPC and one subnet per given AZ, returning
// the subnet IDs in the same order as zones.
func mkVPCAndSubnetsInZones(t *testing.T, ec2Client *awsec2.Client, zones ...string) []string {
	t.Helper()
	ctx := context.Background()

	vpcOut, err := ec2Client.CreateVpc(ctx, &awsec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}

	vpcID := aws.ToString(vpcOut.Vpc.VpcId)

	ids := make([]string, len(zones))

	for i, zone := range zones {
		subOut, err := ec2Client.CreateSubnet(ctx, &awsec2.CreateSubnetInput{
			VpcId:            aws.String(vpcID),
			CidrBlock:        aws.String("10.0." + strconv.Itoa(i) + ".0/24"),
			AvailabilityZone: aws.String(zone),
		})
		if err != nil {
			t.Fatalf("CreateSubnet(%s): %v", zone, err)
		}

		ids[i] = aws.ToString(subOut.Subnet.SubnetId)
	}

	return ids
}

// TestDescribeLoadBalancersReportsRealSubnetAZs proves a load balancer's
// AvailabilityZones reflects each member subnet's actual zone, resolved via
// EC2, rather than the same placeholder zone repeated for every subnet. Real
// ELBv2 reports the true per-subnet AZ, and Terraform's aws_lb resource reads
// this back — a multi-AZ load balancer reporting one zone for every subnet is
// a perpetual plan diff.
func TestDescribeLoadBalancersReportsRealSubnetAZs(t *testing.T) {
	elbClient, ec2Client := newSDKClientsWithEC2(t)
	ctx := context.Background()

	subnetIDs := mkVPCAndSubnetsInZones(t, ec2Client, "us-east-1a", "us-east-1b")

	out, err := elbClient.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name:    aws.String("real-az-alb"),
		Subnets: subnetIDs,
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	zoneBySubnet := map[string]string{}
	for _, az := range out.LoadBalancers[0].AvailabilityZones {
		zoneBySubnet[aws.ToString(az.SubnetId)] = aws.ToString(az.ZoneName)
	}

	if zoneBySubnet[subnetIDs[0]] != "us-east-1a" {
		t.Errorf("zone for %s = %q, want us-east-1a", subnetIDs[0], zoneBySubnet[subnetIDs[0]])
	}

	if zoneBySubnet[subnetIDs[1]] != "us-east-1b" {
		t.Errorf("zone for %s = %q, want us-east-1b", subnetIDs[1], zoneBySubnet[subnetIDs[1]])
	}

	// Survives a Describe round-trip too.
	desc, err := elbClient.DescribeLoadBalancers(ctx, &elb.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{aws.ToString(out.LoadBalancers[0].LoadBalancerArn)},
	})
	if err != nil {
		t.Fatalf("DescribeLoadBalancers: %v", err)
	}

	zoneBySubnet = map[string]string{}
	for _, az := range desc.LoadBalancers[0].AvailabilityZones {
		zoneBySubnet[aws.ToString(az.SubnetId)] = aws.ToString(az.ZoneName)
	}

	if zoneBySubnet[subnetIDs[0]] == zoneBySubnet[subnetIDs[1]] {
		t.Errorf("subnets in different AZs reported the same zone: %v", zoneBySubnet)
	}
}

// TestSetSubnetsReportsRealSubnetAZs proves SetSubnets' response also carries
// the real per-subnet AZ for the replacement subnet list.
func TestSetSubnetsReportsRealSubnetAZs(t *testing.T) {
	elbClient, ec2Client := newSDKClientsWithEC2(t)
	ctx := context.Background()

	subnetIDs := mkVPCAndSubnetsInZones(t, ec2Client, "us-east-1a", "us-east-1c")

	lbOut, err := elbClient.CreateLoadBalancer(ctx, &elb.CreateLoadBalancerInput{
		Name:    aws.String("set-subnets-real-az"),
		Subnets: []string{subnetIDs[0]},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	lbARN := aws.ToString(lbOut.LoadBalancers[0].LoadBalancerArn)

	setOut, err := elbClient.SetSubnets(ctx, &elb.SetSubnetsInput{
		LoadBalancerArn: aws.String(lbARN),
		Subnets:         subnetIDs,
	})
	if err != nil {
		t.Fatalf("SetSubnets: %v", err)
	}

	zoneBySubnet := map[string]string{}
	for _, az := range setOut.AvailabilityZones {
		zoneBySubnet[aws.ToString(az.SubnetId)] = aws.ToString(az.ZoneName)
	}

	if zoneBySubnet[subnetIDs[0]] != "us-east-1a" {
		t.Errorf("zone for %s = %q, want us-east-1a", subnetIDs[0], zoneBySubnet[subnetIDs[0]])
	}

	if zoneBySubnet[subnetIDs[1]] != "us-east-1c" {
		t.Errorf("zone for %s = %q, want us-east-1c", subnetIDs[1], zoneBySubnet[subnetIDs[1]])
	}
}
