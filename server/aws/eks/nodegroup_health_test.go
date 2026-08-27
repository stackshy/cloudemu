package eks_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// TestSDKEKSNodegroupHealthAndResources guards the wire fidelity of a
// nodegroup's health and resources blocks: real EKS always returns a (non-nil,
// empty-when-healthy) health.issues list and a resources.autoScalingGroups list
// naming at least one managed ASG. The ASG name must be stable across Describe.
func TestSDKEKSNodegroupHealthAndResources(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("c1"),
		RoleArn:            aws.String("arn:aws:iam::1:role/r"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	created, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		NodeRole:      aws.String("arn:aws:iam::1:role/r"),
		Subnets:       []string{"subnet-1"},
	})
	if err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	assertNodegroupHealthResources(t, created.Nodegroup)

	got, err := client.DescribeNodegroup(ctx, &awseks.DescribeNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
	})
	if err != nil {
		t.Fatalf("DescribeNodegroup: %v", err)
	}

	assertNodegroupHealthResources(t, got.Nodegroup)

	// The synthetic ASG name must be deterministic: create and describe agree.
	if createdASG(created.Nodegroup) != createdASG(got.Nodegroup) {
		t.Fatalf("ASG name not stable: create=%q describe=%q",
			createdASG(created.Nodegroup), createdASG(got.Nodegroup))
	}
}

func assertNodegroupHealthResources(t *testing.T, ng *ekstypes.Nodegroup) {
	t.Helper()

	if ng.Health == nil {
		t.Fatal("expected non-nil health block")
	}

	if ng.Health.Issues == nil {
		t.Fatal("expected non-nil (empty) health.issues for a healthy nodegroup")
	}

	if len(ng.Health.Issues) != 0 {
		t.Fatalf("expected no health issues, got %+v", ng.Health.Issues)
	}

	if ng.Resources == nil {
		t.Fatal("expected non-nil resources block")
	}

	if len(ng.Resources.AutoScalingGroups) != 1 {
		t.Fatalf("expected exactly one managed ASG, got %+v", ng.Resources.AutoScalingGroups)
	}

	name := createdASG(ng)
	if !strings.HasPrefix(name, "eks-ng1-") {
		t.Fatalf("ASG name = %q, want eks-ng1-<uuid>", name)
	}
}

func createdASG(ng *ekstypes.Nodegroup) string {
	if ng.Resources == nil || len(ng.Resources.AutoScalingGroups) == 0 {
		return ""
	}

	return aws.ToString(ng.Resources.AutoScalingGroups[0].Name)
}
