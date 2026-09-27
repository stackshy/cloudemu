package eks_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

func scalingCluster(t *testing.T, client *awseks.Client) {
	t.Helper()

	if _, err := client.CreateCluster(context.Background(), &awseks.CreateClusterInput{
		Name:               aws.String("c1"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
}

// TestSDKEKSCreateNodegroupScalingDefault checks the scalingConfig EKS fills
// in when CreateNodegroup omits it.
func TestSDKEKSCreateNodegroupScalingDefault(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	scalingCluster(t, client)

	out, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		NodeRole:      aws.String("arn:aws:iam::123456789012:role/node"),
		Subnets:       []string{"subnet-1"},
	})
	if err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	sc := out.Nodegroup.ScalingConfig
	if sc == nil || aws.ToInt32(sc.MinSize) != 1 || aws.ToInt32(sc.MaxSize) != 2 || aws.ToInt32(sc.DesiredSize) != 2 {
		t.Fatalf("default scalingConfig = %+v, want min 1 max 2 desired 2", sc)
	}
}

// TestSDKEKSCreateNodegroupPartialScalingRejected checks that CreateNodegroup
// needs all three sizes or none of them.
func TestSDKEKSCreateNodegroupPartialScalingRejected(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	scalingCluster(t, client)

	_, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		NodeRole:      aws.String("arn:aws:iam::123456789012:role/node"),
		Subnets:       []string{"subnet-1"},
		ScalingConfig: &ekstypes.NodegroupScalingConfig{MaxSize: aws.Int32(3)},
	})

	var ipe *ekstypes.InvalidParameterException
	if !errors.As(err, &ipe) {
		t.Fatalf("expected InvalidParameterException, got %v", err)
	}

	if !strings.Contains(aws.ToString(ipe.Message), "minSize, maxSize and desiredSize") {
		t.Fatalf("message = %q", aws.ToString(ipe.Message))
	}
}

// TestSDKEKSUpdateNodegroupScalingMergedValidation checks that a partial
// update is validated against the merged config.
func TestSDKEKSUpdateNodegroupScalingMergedValidation(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	scalingCluster(t, client)

	if _, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		NodeRole:      aws.String("arn:aws:iam::123456789012:role/node"),
		Subnets:       []string{"subnet-1"},
	}); err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	_, err := client.UpdateNodegroupConfig(ctx, &awseks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		ScalingConfig: &ekstypes.NodegroupScalingConfig{DesiredSize: aws.Int32(3)},
	})

	var ipe *ekstypes.InvalidParameterException
	if !errors.As(err, &ipe) || !strings.Contains(aws.ToString(ipe.Message), "desired capacity 3 can't be greater than max size 2") {
		t.Fatalf("expected InvalidParameterException for desired above max, got %v", err)
	}
}
