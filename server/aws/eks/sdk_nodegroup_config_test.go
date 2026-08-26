package eks_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// TestSDKEKSNodegroupTaints verifies taints round-trip on create/describe and
// that UpdateNodegroupConfig honors addOrUpdateTaints/removeTaints. Terraform
// aws_eks_node_group taint{} blocks otherwise show perpetual drift.
func TestSDKEKSNodegroupTaints(t *testing.T) {
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
		Taints: []ekstypes.Taint{
			{Key: aws.String("dedicated"), Value: aws.String("gpu"), Effect: ekstypes.TaintEffectNoSchedule},
		},
	})
	if err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	if len(ng.Nodegroup.Taints) != 1 || aws.ToString(ng.Nodegroup.Taints[0].Key) != "dedicated" {
		t.Fatalf("create did not return taint, got %+v", ng.Nodegroup.Taints)
	}

	got, err := client.DescribeNodegroup(ctx, &awseks.DescribeNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
	})
	if err != nil {
		t.Fatalf("DescribeNodegroup: %v", err)
	}

	if len(got.Nodegroup.Taints) != 1 || got.Nodegroup.Taints[0].Effect != ekstypes.TaintEffectNoSchedule {
		t.Fatalf("describe did not persist taint, got %+v", got.Nodegroup.Taints)
	}

	if _, err := client.UpdateNodegroupConfig(ctx, &awseks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		Taints: &ekstypes.UpdateTaintsPayload{
			AddOrUpdateTaints: []ekstypes.Taint{
				{Key: aws.String("spot"), Value: aws.String("true"), Effect: ekstypes.TaintEffectPreferNoSchedule},
			},
			RemoveTaints: []ekstypes.Taint{
				{Key: aws.String("dedicated"), Effect: ekstypes.TaintEffectNoSchedule},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateNodegroupConfig: %v", err)
	}

	got, err = client.DescribeNodegroup(ctx, &awseks.DescribeNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
	})
	if err != nil {
		t.Fatalf("DescribeNodegroup after taint update: %v", err)
	}

	if len(got.Nodegroup.Taints) != 1 || aws.ToString(got.Nodegroup.Taints[0].Key) != "spot" {
		t.Fatalf("taint add/remove not applied, got %+v", got.Nodegroup.Taints)
	}
}
