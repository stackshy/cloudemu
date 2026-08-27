package eks_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newEKSConfigClient stands up an EKS wire server backed by a fresh AWS cloud
// and returns a real aws-sdk-go-v2 EKS client pointed at it.
func newEKSConfigClient(t *testing.T) *awseks.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{EKS: cloud.EKS, S3: cloud.S3})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return newEKSClient(t, ts.URL)
}

// TestSDKEKSDescribeClusterConfigEcho verifies CreateCluster -> DescribeCluster
// round-trips logging, kubernetesNetworkConfig, and accessConfig over the wire.
func TestSDKEKSDescribeClusterConfigEcho(t *testing.T) {
	ctx := context.Background()
	client := newEKSConfigClient(t)

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("c1"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks-cluster"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
		Logging: &ekstypes.Logging{
			ClusterLogging: []ekstypes.LogSetup{
				{Types: []ekstypes.LogType{ekstypes.LogTypeApi, ekstypes.LogTypeAudit}, Enabled: aws.Bool(true)},
			},
		},
		KubernetesNetworkConfig: &ekstypes.KubernetesNetworkConfigRequest{
			ServiceIpv4Cidr: aws.String("172.20.0.0/16"),
			IpFamily:        ekstypes.IpFamilyIpv4,
		},
		AccessConfig: &ekstypes.CreateAccessConfigRequest{
			AuthenticationMode:                      ekstypes.AuthenticationModeApi,
			BootstrapClusterCreatorAdminPermissions: aws.Bool(false),
		},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got, err := client.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("c1")})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	cl := got.Cluster

	if cl.Logging == nil || len(cl.Logging.ClusterLogging) != 1 {
		t.Fatalf("logging not echoed: %+v", cl.Logging)
	}

	if !aws.ToBool(cl.Logging.ClusterLogging[0].Enabled) {
		t.Fatal("logging[0].enabled = false, want true")
	}

	if cl.KubernetesNetworkConfig == nil ||
		aws.ToString(cl.KubernetesNetworkConfig.ServiceIpv4Cidr) != "172.20.0.0/16" {
		t.Fatalf("serviceIpv4Cidr not echoed: %+v", cl.KubernetesNetworkConfig)
	}

	if cl.KubernetesNetworkConfig.IpFamily != ekstypes.IpFamilyIpv4 {
		t.Fatalf("ipFamily = %q, want ipv4", cl.KubernetesNetworkConfig.IpFamily)
	}

	if cl.AccessConfig == nil || cl.AccessConfig.AuthenticationMode != ekstypes.AuthenticationModeApi {
		t.Fatalf("authenticationMode not echoed: %+v", cl.AccessConfig)
	}

	if aws.ToBool(cl.AccessConfig.BootstrapClusterCreatorAdminPermissions) {
		t.Fatal("bootstrapClusterCreatorAdminPermissions = true, want false (explicit)")
	}
}

// TestSDKEKSDescribeClusterConfigDefaults verifies that a cluster created
// without logging / networkConfig / accessConfig reports the AWS defaults, so
// an aws_eks_cluster read after create does not drift.
func TestSDKEKSDescribeClusterConfigDefaults(t *testing.T) {
	ctx := context.Background()
	client := newEKSConfigClient(t)

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("c2"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks-cluster"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got, err := client.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("c2")})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	cl := got.Cluster

	if cl.Logging == nil || len(cl.Logging.ClusterLogging) != 1 {
		t.Fatalf("default logging = %+v, want one entry", cl.Logging)
	}

	entry := cl.Logging.ClusterLogging[0]
	if aws.ToBool(entry.Enabled) {
		t.Fatal("default logging enabled = true, want false")
	}

	if len(entry.Types) != 5 {
		t.Fatalf("default logging types = %d, want 5", len(entry.Types))
	}

	if cl.KubernetesNetworkConfig == nil ||
		aws.ToString(cl.KubernetesNetworkConfig.ServiceIpv4Cidr) != "10.100.0.0/16" ||
		cl.KubernetesNetworkConfig.IpFamily != ekstypes.IpFamilyIpv4 {
		t.Fatalf("default kubernetesNetworkConfig = %+v", cl.KubernetesNetworkConfig)
	}

	if cl.AccessConfig == nil || cl.AccessConfig.AuthenticationMode != ekstypes.AuthenticationModeConfigMap {
		t.Fatalf("default authenticationMode = %+v, want CONFIG_MAP", cl.AccessConfig)
	}

	if !aws.ToBool(cl.AccessConfig.BootstrapClusterCreatorAdminPermissions) {
		t.Fatal("default bootstrapClusterCreatorAdminPermissions = false, want true")
	}
}
