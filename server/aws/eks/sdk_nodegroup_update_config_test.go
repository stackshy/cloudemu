package eks_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// TestSDKEKSNodegroupUpdateConfig verifies a nodegroup's updateConfig round-trips
// through the real SDK: a default create reports maxUnavailable=1, an explicit
// value is echoed, and UpdateNodegroupConfig applies a new one. Terraform's
// aws_eks_node_group update_config block otherwise drifts on every plan because
// DescribeNodegroup omits the field.
func TestSDKEKSNodegroupUpdateConfig(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("c1"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks-cluster"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	// Default: no updateConfig in the request -> maxUnavailable=1 on describe.
	def, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("def"),
		NodeRole:      aws.String("arn:aws:iam::123456789012:role/eks-node"),
		Subnets:       []string{"subnet-1"},
	})
	if err != nil {
		t.Fatalf("CreateNodegroup(default): %v", err)
	}

	if def.Nodegroup.UpdateConfig == nil || aws.ToInt32(def.Nodegroup.UpdateConfig.MaxUnavailable) != 1 {
		t.Fatalf("default updateConfig = %+v, want maxUnavailable=1", def.Nodegroup.UpdateConfig)
	}

	// Explicit maxUnavailable echoes back.
	exp, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("exp"),
		NodeRole:      aws.String("arn:aws:iam::123456789012:role/eks-node"),
		Subnets:       []string{"subnet-1"},
		UpdateConfig:  &ekstypes.NodegroupUpdateConfig{MaxUnavailable: aws.Int32(2)},
	})
	if err != nil {
		t.Fatalf("CreateNodegroup(explicit): %v", err)
	}

	if aws.ToInt32(exp.Nodegroup.UpdateConfig.MaxUnavailable) != 2 {
		t.Fatalf("explicit updateConfig = %+v, want maxUnavailable=2", exp.Nodegroup.UpdateConfig)
	}

	// UpdateNodegroupConfig applies a new value that describe reflects.
	if _, err = client.UpdateNodegroupConfig(ctx, &awseks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("exp"),
		UpdateConfig:  &ekstypes.NodegroupUpdateConfig{MaxUnavailable: aws.Int32(5)},
	}); err != nil {
		t.Fatalf("UpdateNodegroupConfig: %v", err)
	}

	got, err := client.DescribeNodegroup(ctx, &awseks.DescribeNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("exp"),
	})
	if err != nil {
		t.Fatalf("DescribeNodegroup: %v", err)
	}

	if aws.ToInt32(got.Nodegroup.UpdateConfig.MaxUnavailable) != 5 {
		t.Fatalf("post-update updateConfig = %+v, want maxUnavailable=5", got.Nodegroup.UpdateConfig)
	}
}
