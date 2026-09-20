package eks_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// TestSDKEKSNodegroupLaunchTemplate verifies a nodegroup created with a
// launchTemplate round-trips it over the wire on both Create and Describe,
// matching real EKS (the field must not be silently dropped).
func TestSDKEKSNodegroupLaunchTemplate(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:    aws.String("c1"),
		RoleArn: aws.String("arn:aws:iam::123456789012:role/eks-cluster"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{
			SubnetIds: []string{"subnet-1"},
		},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	ng, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		NodeRole:      aws.String("arn:aws:iam::123456789012:role/eks-node"),
		Subnets:       []string{"subnet-1"},
		LaunchTemplate: &ekstypes.LaunchTemplateSpecification{
			Id:      aws.String("lt-0123456789abcdef0"),
			Version: aws.String("2"),
		},
	})
	if err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	if ng.Nodegroup.LaunchTemplate == nil || aws.ToString(ng.Nodegroup.LaunchTemplate.Id) != "lt-0123456789abcdef0" {
		t.Fatalf("create did not return launchTemplate, got %+v", ng.Nodegroup.LaunchTemplate)
	}

	got, err := client.DescribeNodegroup(ctx, &awseks.DescribeNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
	})
	if err != nil {
		t.Fatalf("DescribeNodegroup: %v", err)
	}

	if got.Nodegroup.LaunchTemplate == nil {
		t.Fatal("describe did not persist launchTemplate")
	}

	if aws.ToString(got.Nodegroup.LaunchTemplate.Id) != "lt-0123456789abcdef0" {
		t.Fatalf("launchTemplate.id = %q, want lt-0123456789abcdef0", aws.ToString(got.Nodegroup.LaunchTemplate.Id))
	}

	if aws.ToString(got.Nodegroup.LaunchTemplate.Version) != "2" {
		t.Fatalf("launchTemplate.version = %q, want 2", aws.ToString(got.Nodegroup.LaunchTemplate.Version))
	}
}
