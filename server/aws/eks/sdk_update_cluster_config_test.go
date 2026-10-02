package eks_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// TestSDKEKSUpdateClusterConfigLogging verifies UpdateClusterConfig actually
// applies a logging change over the wire (rather than silently dropping it)
// and that the returned Update reports the LoggingUpdate type, matching real
// EKS, instead of a stale hardcoded one.
func TestSDKEKSUpdateClusterConfigLogging(t *testing.T) {
	client := newEKSConfigClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("c1"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks-cluster"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	upd, err := client.UpdateClusterConfig(ctx, &awseks.UpdateClusterConfigInput{
		Name: aws.String("c1"),
		Logging: &ekstypes.Logging{
			ClusterLogging: []ekstypes.LogSetup{
				{Types: []ekstypes.LogType{ekstypes.LogTypeApi, ekstypes.LogTypeAudit}, Enabled: aws.Bool(true)},
			},
		},
	})
	if err != nil {
		t.Fatalf("UpdateClusterConfig: %v", err)
	}

	if upd.Update == nil || upd.Update.Type != ekstypes.UpdateTypeLoggingUpdate {
		t.Fatalf("update.type = %+v, want LoggingUpdate", upd.Update)
	}

	got, err := client.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("c1")})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	if got.Cluster.Logging == nil {
		t.Fatal("DescribeCluster after update: logging = nil, want the applied change")
	}

	found := false

	for _, entry := range got.Cluster.Logging.ClusterLogging {
		if !aws.ToBool(entry.Enabled) {
			continue
		}

		for _, ty := range entry.Types {
			if ty == ekstypes.LogTypeApi {
				found = true
			}
		}
	}

	if !found {
		t.Fatalf("logging change was not applied, got %+v", got.Cluster.Logging)
	}
}

// TestSDKEKSUpdateClusterConfigAccessConfig verifies UpdateClusterConfig
// applies an accessConfig.authenticationMode change over the wire and returns
// an Update of type AccessConfigUpdate.
func TestSDKEKSUpdateClusterConfigAccessConfig(t *testing.T) {
	client := newEKSConfigClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("c1"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks-cluster"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	upd, err := client.UpdateClusterConfig(ctx, &awseks.UpdateClusterConfigInput{
		Name: aws.String("c1"),
		AccessConfig: &ekstypes.UpdateAccessConfigRequest{
			AuthenticationMode: ekstypes.AuthenticationModeApiAndConfigMap,
		},
	})
	if err != nil {
		t.Fatalf("UpdateClusterConfig: %v", err)
	}

	if upd.Update == nil || upd.Update.Type != ekstypes.UpdateTypeAccessConfigUpdate {
		t.Fatalf("update.type = %+v, want AccessConfigUpdate", upd.Update)
	}

	got, err := client.DescribeCluster(ctx, &awseks.DescribeClusterInput{Name: aws.String("c1")})
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	if got.Cluster.AccessConfig == nil ||
		got.Cluster.AccessConfig.AuthenticationMode != ekstypes.AuthenticationModeApiAndConfigMap {
		t.Fatalf("authenticationMode not applied, got %+v", got.Cluster.AccessConfig)
	}
}
