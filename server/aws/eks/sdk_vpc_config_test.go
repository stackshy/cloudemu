package eks_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// TestSDKEKSDescribeClusterVPCConfig verifies DescribeCluster reports a
// synthesized clusterSecurityGroupId (sg-...) and derives vpcId from the
// cluster's subnets. Terraform node-group modules reference
// aws_eks_cluster.vpc_config[0].cluster_security_group_id and vpc_id.
func TestSDKEKSDescribeClusterVPCConfig(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()

	vpc, err := cloud.VPC.CreateVPC(ctx, netdriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
	if err != nil {
		t.Fatalf("CreateVPC: %v", err)
	}

	subnet, err := cloud.VPC.CreateSubnet(ctx, netdriver.SubnetConfig{
		VPCID:     vpc.ID,
		CIDRBlock: "10.0.1.0/24",
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}

	srv := awsserver.New(awsserver.Drivers{EKS: cloud.EKS, S3: cloud.S3})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := awseks.NewFromConfig(cfg, func(o *awseks.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:    aws.String("c1"),
		RoleArn: aws.String("arn:aws:iam::123456789012:role/eks-cluster"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{
			SubnetIds: []string{subnet.ID},
		},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got, err := client.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("c1")})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	vc := got.Cluster.ResourcesVpcConfig
	if vc == nil {
		t.Fatal("resourcesVpcConfig is nil")
	}

	if sg := aws.ToString(vc.ClusterSecurityGroupId); !strings.HasPrefix(sg, "sg-") {
		t.Fatalf("clusterSecurityGroupId = %q, want sg- prefix", sg)
	}

	if id := aws.ToString(vc.VpcId); id != vpc.ID {
		t.Fatalf("vpcId = %q, want %q (derived from subnet)", id, vpc.ID)
	}
}
