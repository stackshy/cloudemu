package eks_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// TestSDKEKSUpdateNodegroupConfigPreservesSizes guards that a partial scaling
// update (only desiredSize) does not zero MinSize/MaxSize.
func TestSDKEKSUpdateNodegroupConfigPreservesSizes(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("c1"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if _, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		NodeRole:      aws.String("arn:aws:iam::123456789012:role/node"),
		Subnets:       []string{"subnet-1"},
		ScalingConfig: &ekstypes.NodegroupScalingConfig{
			MinSize:     aws.Int32(2),
			MaxSize:     aws.Int32(10),
			DesiredSize: aws.Int32(4),
		},
	}); err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	// Update only desiredSize.
	if _, err := client.UpdateNodegroupConfig(ctx, &awseks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		ScalingConfig: &ekstypes.NodegroupScalingConfig{
			DesiredSize: aws.Int32(6),
		},
	}); err != nil {
		t.Fatalf("UpdateNodegroupConfig: %v", err)
	}

	desc, err := client.DescribeNodegroup(ctx, &awseks.DescribeNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
	})
	if err != nil {
		t.Fatalf("DescribeNodegroup: %v", err)
	}

	sc := desc.Nodegroup.ScalingConfig
	if aws.ToInt32(sc.MinSize) != 2 || aws.ToInt32(sc.MaxSize) != 10 || aws.ToInt32(sc.DesiredSize) != 6 {
		t.Fatalf("scaling after partial update = min=%d max=%d desired=%d, want 2/10/6",
			aws.ToInt32(sc.MinSize), aws.ToInt32(sc.MaxSize), aws.ToInt32(sc.DesiredSize))
	}
}

// TestSDKEKSClusterOIDCIdentity guards that DescribeCluster surfaces a stable
// OIDC issuer under identity.oidc.issuer.
func TestSDKEKSClusterOIDCIdentity(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("oidc-c"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	desc, err := client.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("oidc-c")})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	if desc.Cluster.Identity == nil || desc.Cluster.Identity.Oidc == nil {
		t.Fatalf("identity/oidc nil: %+v", desc.Cluster.Identity)
	}

	issuer := aws.ToString(desc.Cluster.Identity.Oidc.Issuer)
	if !strings.HasPrefix(issuer, "https://oidc.eks.us-east-1.amazonaws.com/id/") {
		t.Fatalf("issuer = %q", issuer)
	}

	// Stable across describes.
	desc2, err := client.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("oidc-c")})
	if err != nil {
		t.Fatalf("DescribeCluster(2): %v", err)
	}

	if aws.ToString(desc2.Cluster.Identity.Oidc.Issuer) != issuer {
		t.Fatalf("issuer changed between describes")
	}
}

// TestSDKEKSDescribeAndListUpdates guards that a mutating op's update is
// retrievable via DescribeUpdate and ListUpdates (the UpdateSuccessful poller).
func TestSDKEKSDescribeAndListUpdates(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("u1"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	upd, err := client.UpdateClusterVersion(ctx, &awseks.UpdateClusterVersionInput{
		Name:    aws.String("u1"),
		Version: aws.String("1.30"),
	})
	if err != nil {
		t.Fatalf("UpdateClusterVersion: %v", err)
	}

	updateID := aws.ToString(upd.Update.Id)
	if updateID == "" {
		t.Fatal("update id empty")
	}

	got, err := client.DescribeUpdate(ctx, &awseks.DescribeUpdateInput{
		Name:     aws.String("u1"),
		UpdateId: aws.String(updateID),
	})
	if err != nil {
		t.Fatalf("DescribeUpdate: %v", err)
	}

	if aws.ToString(got.Update.Id) != updateID || got.Update.Status != ekstypes.UpdateStatusSuccessful {
		t.Fatalf("DescribeUpdate = id %q status %q", aws.ToString(got.Update.Id), got.Update.Status)
	}

	list, err := client.ListUpdates(ctx, &awseks.ListUpdatesInput{Name: aws.String("u1")})
	if err != nil {
		t.Fatalf("ListUpdates: %v", err)
	}

	if len(list.UpdateIds) != 1 || list.UpdateIds[0] != updateID {
		t.Fatalf("ListUpdates = %+v, want [%s]", list.UpdateIds, updateID)
	}
}

// TestSDKEKSListClustersPagination guards maxResults/nextToken on ListClusters.
func TestSDKEKSListClustersPagination(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	for _, n := range []string{"a", "b", "c"} {
		if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
			Name:               aws.String(n),
			RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks"),
			ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
		}); err != nil {
			t.Fatalf("CreateCluster(%s): %v", n, err)
		}
	}

	first, err := client.ListClusters(ctx, &awseks.ListClustersInput{MaxResults: aws.Int32(2)})
	if err != nil {
		t.Fatalf("ListClusters(page1): %v", err)
	}

	if len(first.Clusters) != 2 || aws.ToString(first.NextToken) == "" {
		t.Fatalf("page1: got %d clusters, nextToken=%q", len(first.Clusters), aws.ToString(first.NextToken))
	}

	second, err := client.ListClusters(ctx, &awseks.ListClustersInput{
		MaxResults: aws.Int32(2),
		NextToken:  first.NextToken,
	})
	if err != nil {
		t.Fatalf("ListClusters(page2): %v", err)
	}

	if len(second.Clusters) != 1 || aws.ToString(second.NextToken) != "" {
		t.Fatalf("page2: got %d clusters, nextToken=%q", len(second.Clusters), aws.ToString(second.NextToken))
	}
}
