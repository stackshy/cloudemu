package eks_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

func newSDKClient(t *testing.T) *awseks.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		EKS: cloud.EKS,
		// S3 included so we exercise routing precedence — EKS must claim
		// /clusters paths before the catch-all S3 handler sees them.
		S3: cloud.S3,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awseks.NewFromConfig(cfg, func(o *awseks.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKEKSClusterLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:    aws.String("c1"),
		Version: aws.String("1.30"),
		RoleArn: aws.String("arn:aws:iam::123456789012:role/eks-cluster"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{
			SubnetIds: []string{"subnet-1", "subnet-2"},
		},
		Tags: map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if aws.ToString(out.Cluster.Name) != "c1" {
		t.Fatalf("got name %q, want c1", aws.ToString(out.Cluster.Name))
	}

	if out.Cluster.Status != ekstypes.ClusterStatusActive {
		t.Fatalf("got status %q, want ACTIVE", out.Cluster.Status)
	}

	if aws.ToString(out.Cluster.Endpoint) == "" {
		t.Fatal("expected endpoint to be set (placeholder for Wave 1)")
	}

	if out.Cluster.CertificateAuthority == nil || aws.ToString(out.Cluster.CertificateAuthority.Data) == "" {
		t.Fatal("expected stub certificate-authority data")
	}

	got, err := client.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("c1")})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	if aws.ToString(got.Cluster.Version) != "1.30" {
		t.Fatalf("got version %q, want 1.30", aws.ToString(got.Cluster.Version))
	}

	list, err := client.ListClusters(ctx, &awseks.ListClustersInput{})
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}

	if len(list.Clusters) != 1 || list.Clusters[0] != "c1" {
		t.Fatalf("got %v, want [c1]", list.Clusters)
	}

	upd, err := client.UpdateClusterVersion(ctx, &awseks.UpdateClusterVersionInput{
		Name:    aws.String("c1"),
		Version: aws.String("1.31"),
	})
	if err != nil {
		t.Fatalf("UpdateClusterVersion: %v", err)
	}

	if upd.Update == nil || aws.ToString(upd.Update.Id) == "" {
		t.Fatal("expected update with non-empty id")
	}

	got, err = client.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("c1")})
	if err != nil {
		t.Fatalf("DescribeCluster after update: %v", err)
	}

	if aws.ToString(got.Cluster.Version) != "1.31" {
		t.Fatalf("version did not apply: got %q", aws.ToString(got.Cluster.Version))
	}

	pubAccess := true

	_, err = client.UpdateClusterConfig(ctx, &awseks.UpdateClusterConfigInput{
		Name: aws.String("c1"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{
			EndpointPublicAccess: &pubAccess,
			PublicAccessCidrs:    []string{"0.0.0.0/0"},
		},
	})
	if err != nil {
		t.Fatalf("UpdateClusterConfig: %v", err)
	}

	if _, err := client.DeleteCluster(ctx, &awseks.DeleteClusterInput{Name: aws.String("c1")}); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	if _, err := client.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("c1")}); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestSDKEKSNodegroupLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:    aws.String("c1"),
		Version: aws.String("1.30"),
		RoleArn: aws.String("arn:aws:iam::1:role/r"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{
			SubnetIds: []string{"subnet-1"},
		},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	minSize := int32(1)
	maxSize := int32(3)
	desired := int32(2)

	out, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		NodeRole:      aws.String("arn:aws:iam::1:role/r"),
		Subnets:       []string{"subnet-1"},
		ScalingConfig: &ekstypes.NodegroupScalingConfig{
			MinSize:     &minSize,
			MaxSize:     &maxSize,
			DesiredSize: &desired,
		},
	})
	if err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	if out.Nodegroup.Status != ekstypes.NodegroupStatusActive {
		t.Fatalf("got status %q, want ACTIVE", out.Nodegroup.Status)
	}

	got, err := client.DescribeNodegroup(ctx, &awseks.DescribeNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
	})
	if err != nil {
		t.Fatalf("DescribeNodegroup: %v", err)
	}

	if got.Nodegroup.ScalingConfig == nil || aws.ToInt32(got.Nodegroup.ScalingConfig.DesiredSize) != 2 {
		t.Fatal("expected desired size 2 to round-trip")
	}

	list, err := client.ListNodegroups(ctx, &awseks.ListNodegroupsInput{ClusterName: aws.String("c1")})
	if err != nil {
		t.Fatalf("ListNodegroups: %v", err)
	}

	if len(list.Nodegroups) != 1 || list.Nodegroups[0] != "ng1" {
		t.Fatalf("got %v, want [ng1]", list.Nodegroups)
	}

	newDesired := int32(3)

	if _, err := client.UpdateNodegroupConfig(ctx, &awseks.UpdateNodegroupConfigInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		ScalingConfig: &ekstypes.NodegroupScalingConfig{DesiredSize: &newDesired},
	}); err != nil {
		t.Fatalf("UpdateNodegroupConfig: %v", err)
	}

	if _, err := client.UpdateNodegroupVersion(ctx, &awseks.UpdateNodegroupVersionInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
		Version:       aws.String("1.31"),
	}); err != nil {
		t.Fatalf("UpdateNodegroupVersion: %v", err)
	}

	if _, err := client.DeleteNodegroup(ctx, &awseks.DeleteNodegroupInput{
		ClusterName:   aws.String("c1"),
		NodegroupName: aws.String("ng1"),
	}); err != nil {
		t.Fatalf("DeleteNodegroup: %v", err)
	}
}

func TestSDKEKSFargateProfileLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("c1"),
		Version:            aws.String("1.30"),
		RoleArn:            aws.String("arn:aws:iam::1:role/r"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	out, err := client.CreateFargateProfile(ctx, &awseks.CreateFargateProfileInput{
		ClusterName:         aws.String("c1"),
		FargateProfileName:  aws.String("fp1"),
		PodExecutionRoleArn: aws.String("arn:aws:iam::1:role/fargate"),
		Selectors: []ekstypes.FargateProfileSelector{
			{Namespace: aws.String("default")},
		},
	})
	if err != nil {
		t.Fatalf("CreateFargateProfile: %v", err)
	}

	if out.FargateProfile.Status != ekstypes.FargateProfileStatusActive {
		t.Fatalf("got status %q, want ACTIVE", out.FargateProfile.Status)
	}

	got, err := client.DescribeFargateProfile(ctx, &awseks.DescribeFargateProfileInput{
		ClusterName:        aws.String("c1"),
		FargateProfileName: aws.String("fp1"),
	})
	if err != nil {
		t.Fatalf("DescribeFargateProfile: %v", err)
	}

	if aws.ToString(got.FargateProfile.FargateProfileName) != "fp1" {
		t.Fatalf("got name %q, want fp1", aws.ToString(got.FargateProfile.FargateProfileName))
	}

	list, err := client.ListFargateProfiles(ctx, &awseks.ListFargateProfilesInput{ClusterName: aws.String("c1")})
	if err != nil {
		t.Fatalf("ListFargateProfiles: %v", err)
	}

	if len(list.FargateProfileNames) != 1 {
		t.Fatalf("got %d profiles, want 1", len(list.FargateProfileNames))
	}

	if _, err := client.DeleteFargateProfile(ctx, &awseks.DeleteFargateProfileInput{
		ClusterName:        aws.String("c1"),
		FargateProfileName: aws.String("fp1"),
	}); err != nil {
		t.Fatalf("DeleteFargateProfile: %v", err)
	}
}

func TestSDKEKSAddonLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("c1"),
		Version:            aws.String("1.30"),
		RoleArn:            aws.String("arn:aws:iam::1:role/r"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	out, err := client.CreateAddon(ctx, &awseks.CreateAddonInput{
		ClusterName:  aws.String("c1"),
		AddonName:    aws.String("vpc-cni"),
		AddonVersion: aws.String("v1.0.0"),
	})
	if err != nil {
		t.Fatalf("CreateAddon: %v", err)
	}

	if out.Addon.Status != ekstypes.AddonStatusActive {
		t.Fatalf("got status %q, want ACTIVE", out.Addon.Status)
	}

	got, err := client.DescribeAddon(ctx, &awseks.DescribeAddonInput{
		ClusterName: aws.String("c1"),
		AddonName:   aws.String("vpc-cni"),
	})
	if err != nil {
		t.Fatalf("DescribeAddon: %v", err)
	}

	if aws.ToString(got.Addon.AddonVersion) != "v1.0.0" {
		t.Fatalf("got version %q, want v1.0.0", aws.ToString(got.Addon.AddonVersion))
	}

	list, err := client.ListAddons(ctx, &awseks.ListAddonsInput{ClusterName: aws.String("c1")})
	if err != nil {
		t.Fatalf("ListAddons: %v", err)
	}

	if len(list.Addons) != 1 {
		t.Fatalf("got %d addons, want 1", len(list.Addons))
	}

	if _, err := client.UpdateAddon(ctx, &awseks.UpdateAddonInput{
		ClusterName:  aws.String("c1"),
		AddonName:    aws.String("vpc-cni"),
		AddonVersion: aws.String("v2.0.0"),
	}); err != nil {
		t.Fatalf("UpdateAddon: %v", err)
	}

	got, err = client.DescribeAddon(ctx, &awseks.DescribeAddonInput{
		ClusterName: aws.String("c1"),
		AddonName:   aws.String("vpc-cni"),
	})
	if err != nil {
		t.Fatalf("DescribeAddon after update: %v", err)
	}

	if aws.ToString(got.Addon.AddonVersion) != "v2.0.0" {
		t.Fatalf("update did not apply: got %q", aws.ToString(got.Addon.AddonVersion))
	}

	if _, err := client.DeleteAddon(ctx, &awseks.DeleteAddonInput{
		ClusterName: aws.String("c1"),
		AddonName:   aws.String("vpc-cni"),
	}); err != nil {
		t.Fatalf("DeleteAddon: %v", err)
	}
}

// Sanity check: when both EKS and S3 are wired, an S3 request still reaches
// the S3 handler — EKS's Matches must be rooted at /clusters specifically.
func TestSDKEKSRoutingDoesNotShadowS3(t *testing.T) {
	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EKS: cloud.EKS, S3: cloud.S3})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	// A bare GET / is a list-buckets request; that should hit S3, not EKS.
	resp, err := ts.Client().Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}

	defer resp.Body.Close()

	if resp.Header.Get("X-Amzn-ErrorType") == "ResourceNotFoundException" {
		t.Fatal("EKS handler shadowed an S3 request")
	}
}

// Error mapping: an unknown cluster yields ResourceNotFoundException so the
// SDK's typed-error middleware can map it correctly.
func TestSDKEKSDescribeMissingClusterReturnsTypedError(t *testing.T) {
	client := newSDKClient(t)

	_, err := client.DescribeCluster(context.Background(), &awseks.DescribeClusterInput{
		Name: aws.String("does-not-exist"),
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var notFound *ekstypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("expected ResourceNotFoundException, got %T: %v", err, err)
	}
}
