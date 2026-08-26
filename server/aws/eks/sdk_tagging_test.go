package eks_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// TestSDKEKSTagResourceAllARNTypes verifies the tagging API works against every
// EKS resource ARN — cluster, nodegroup, Fargate profile, and add-on — not just
// clusters. Terraform tag updates on aws_eks_node_group / aws_eks_fargate_profile
// / aws_eks_addon call TagResource against the child resource ARN.
func TestSDKEKSTagResourceAllARNTypes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:    aws.String("c1"),
		RoleArn: aws.String("arn:aws:iam::123456789012:role/eks-cluster"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{
			SubnetIds: []string{"subnet-1", "subnet-2"},
		},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	ngOut, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		NodeRole:      aws.String("arn:aws:iam::123456789012:role/eks-node"),
		Subnets:       []string{"subnet-1"},
	})
	if err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	fpOut, err := client.CreateFargateProfile(ctx, &awseks.CreateFargateProfileInput{
		ClusterName:         aws.String("c1"),
		FargateProfileName:  aws.String("fp1"),
		PodExecutionRoleArn: aws.String("arn:aws:iam::123456789012:role/eks-fargate"),
	})
	if err != nil {
		t.Fatalf("CreateFargateProfile: %v", err)
	}

	adOut, err := client.CreateAddon(ctx, &awseks.CreateAddonInput{
		ClusterName: aws.String("c1"),
		AddonName:   aws.String("vpc-cni"),
	})
	if err != nil {
		t.Fatalf("CreateAddon: %v", err)
	}

	clusterOut, err := client.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("c1")})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	cases := []struct {
		name string
		arn  string
	}{
		{"cluster", aws.ToString(clusterOut.Cluster.Arn)},
		{"nodegroup", aws.ToString(ngOut.Nodegroup.NodegroupArn)},
		{"fargateprofile", aws.ToString(fpOut.FargateProfile.FargateProfileArn)},
		{"addon", aws.ToString(adOut.Addon.AddonArn)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.arn == "" {
				t.Fatalf("%s ARN is empty", tc.name)
			}

			if _, err := client.TagResource(ctx, &awseks.TagResourceInput{
				ResourceArn: aws.String(tc.arn),
				Tags:        map[string]string{"team": "platform"},
			}); err != nil {
				t.Fatalf("TagResource(%s): %v", tc.name, err)
			}

			got, err := client.ListTagsForResource(ctx, &awseks.ListTagsForResourceInput{
				ResourceArn: aws.String(tc.arn),
			})
			if err != nil {
				t.Fatalf("ListTagsForResource(%s): %v", tc.name, err)
			}

			if got.Tags["team"] != "platform" {
				t.Fatalf("%s: tag not returned, got %v", tc.name, got.Tags)
			}

			if _, err := client.UntagResource(ctx, &awseks.UntagResourceInput{
				ResourceArn: aws.String(tc.arn),
				TagKeys:     []string{"team"},
			}); err != nil {
				t.Fatalf("UntagResource(%s): %v", tc.name, err)
			}

			got, err = client.ListTagsForResource(ctx, &awseks.ListTagsForResourceInput{
				ResourceArn: aws.String(tc.arn),
			})
			if err != nil {
				t.Fatalf("ListTagsForResource(%s) after untag: %v", tc.name, err)
			}

			if _, ok := got.Tags["team"]; ok {
				t.Fatalf("%s: tag not removed, got %v", tc.name, got.Tags)
			}
		})
	}
}
