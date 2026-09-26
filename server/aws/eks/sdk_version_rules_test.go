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

// requireInvalidParameter fails unless err is an InvalidParameterException
// whose message contains want.
func requireInvalidParameter(t *testing.T, err error, want string) {
	t.Helper()

	var ipe *ekstypes.InvalidParameterException
	if !errors.As(err, &ipe) {
		t.Fatalf("expected InvalidParameterException, got %T: %v", err, err)
	}

	if !strings.Contains(aws.ToString(ipe.Message), want) {
		t.Fatalf("message %q does not contain %q", aws.ToString(ipe.Message), want)
	}
}

func createVersionCluster(t *testing.T, client *awseks.Client, version string) {
	t.Helper()

	if _, err := client.CreateCluster(context.Background(), &awseks.CreateClusterInput{
		Name:               aws.String("v1"),
		Version:            aws.String(version),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
}

func TestSDKEKSClusterVersionRules(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("bad"),
		Version:            aws.String("0.1"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	})
	requireInvalidParameter(t, err, "unsupported Kubernetes version 0.1")

	createVersionCluster(t, client, "1.30")

	_, err = client.UpdateClusterVersion(ctx, &awseks.UpdateClusterVersionInput{
		Name: aws.String("v1"), Version: aws.String("1.32"),
	})
	requireInvalidParameter(t, err, "Unsupported Kubernetes minor version update from 1.30 to 1.32")

	if _, err := client.UpdateClusterVersion(ctx, &awseks.UpdateClusterVersionInput{
		Name: aws.String("v1"), Version: aws.String("1.31"),
	}); err != nil {
		t.Fatalf("UpdateClusterVersion 1.31: %v", err)
	}

	if _, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName: aws.String("v1"), NodegroupName: aws.String("ng1"),
		NodeRole: aws.String("arn:aws:iam::123456789012:role/node"), Subnets: []string{"subnet-1"},
	}); err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	// The nodegroup runs 1.31, so a plain rollback is refused.
	_, err = client.UpdateClusterVersion(ctx, &awseks.UpdateClusterVersionInput{
		Name: aws.String("v1"), Version: aws.String("1.30"),
	})

	var ire *ekstypes.InvalidRequestException
	if !errors.As(err, &ire) {
		t.Fatalf("expected InvalidRequestException, got %T: %v", err, err)
	}

	upd, err := client.UpdateClusterVersion(ctx, &awseks.UpdateClusterVersionInput{
		Name: aws.String("v1"), Version: aws.String("1.30"), Force: true,
	})
	if err != nil {
		t.Fatalf("forced rollback: %v", err)
	}

	if upd.Update.Type != "VersionRollback" {
		t.Fatalf("update type = %q, want VersionRollback", upd.Update.Type)
	}
}

func TestSDKEKSNodegroupVersionCap(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	createVersionCluster(t, client, "1.31")

	_, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName: aws.String("v1"), NodegroupName: aws.String("ng1"), Version: aws.String("1.32"),
		NodeRole: aws.String("arn:aws:iam::123456789012:role/node"), Subnets: []string{"subnet-1"},
	})
	requireInvalidParameter(t, err, "cannot be newer than cluster v1 version 1.31")

	out, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName: aws.String("v1"), NodegroupName: aws.String("ng1"),
		NodeRole: aws.String("arn:aws:iam::123456789012:role/node"), Subnets: []string{"subnet-1"},
	})
	if err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	if aws.ToString(out.Nodegroup.Version) != "1.31" {
		t.Fatalf("nodegroup version = %q, want the cluster version 1.31", aws.ToString(out.Nodegroup.Version))
	}

	_, err = client.UpdateNodegroupVersion(ctx, &awseks.UpdateNodegroupVersionInput{
		ClusterName: aws.String("v1"), NodegroupName: aws.String("ng1"), Version: aws.String("1.32"),
	})
	requireInvalidParameter(t, err, "cannot be newer than cluster v1 version 1.31")
}

func TestSDKEKSAddonVersionResolution(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	createVersionCluster(t, client, "1.30")

	if _, err := client.CreateAddon(ctx, &awseks.CreateAddonInput{
		ClusterName: aws.String("v1"), AddonName: aws.String("vpc-cni"),
	}); err != nil {
		t.Fatalf("CreateAddon: %v", err)
	}

	got, err := client.DescribeAddon(ctx, &awseks.DescribeAddonInput{
		ClusterName: aws.String("v1"), AddonName: aws.String("vpc-cni"),
	})
	if err != nil {
		t.Fatalf("DescribeAddon: %v", err)
	}

	if v := aws.ToString(got.Addon.AddonVersion); v != "v1.23.1-eksbuild.1" {
		t.Fatalf("addonVersion = %q, want the 1.30 default v1.23.1-eksbuild.1", v)
	}

	_, err = client.CreateAddon(ctx, &awseks.CreateAddonInput{
		ClusterName: aws.String("v1"), AddonName: aws.String("coredns"), AddonVersion: aws.String("v0.0.1"),
	})
	requireInvalidParameter(t, err, "Addon version specified is not supported")

	_, err = client.CreateAddon(ctx, &awseks.CreateAddonInput{
		ClusterName: aws.String("v1"), AddonName: aws.String("made-up"),
	})
	requireInvalidParameter(t, err, "Addon made-up specified is not supported in 1.30 kubernetes version")

	_, err = client.CreateAddon(ctx, &awseks.CreateAddonInput{
		ClusterName: aws.String("v1"), AddonName: aws.String("coredns"), ConfigurationValues: aws.String("{broken"),
	})
	requireInvalidParameter(t, err, "ConfigurationValue provided in request is not supported")
}
