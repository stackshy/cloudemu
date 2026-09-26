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

// TestSDKBootstrapCreatorAdminEntry checks that an API mode cluster gets an
// admin access entry for the caller. The SDK signs with access key "test".
func TestSDKBootstrapCreatorAdminEntry(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("boot"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks-cluster"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
		AccessConfig:       &ekstypes.CreateAccessConfigRequest{AuthenticationMode: ekstypes.AuthenticationModeApi},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	out, err := client.ListAccessEntries(ctx, &awseks.ListAccessEntriesInput{
		ClusterName: aws.String("boot"), AssociatedPolicyArn: aws.String(sdkAdminPolicy),
	})
	if err != nil || len(out.AccessEntries) != 1 || !strings.HasSuffix(out.AccessEntries[0], ":user/test") {
		t.Fatalf("creator entry = %+v err %v", out, err)
	}

	pol, err := client.ListAssociatedAccessPolicies(ctx, &awseks.ListAssociatedAccessPoliciesInput{
		ClusterName: aws.String("boot"), PrincipalArn: aws.String(out.AccessEntries[0]),
	})
	if err != nil || len(pol.AssociatedAccessPolicies) != 1 ||
		pol.AssociatedAccessPolicies[0].AccessScope.Type != ekstypes.AccessScopeTypeCluster {
		t.Fatalf("creator policies = %+v err %v", pol, err)
	}
}

// TestSDKAuthModeBackwardRejected checks the one-way mode rule on the wire.
func TestSDKAuthModeBackwardRejected(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	createClusterWithMode(t, client, "oneway", ekstypes.AuthenticationModeApiAndConfigMap)

	_, err := client.UpdateClusterConfig(ctx, &awseks.UpdateClusterConfigInput{
		Name:         aws.String("oneway"),
		AccessConfig: &ekstypes.UpdateAccessConfigRequest{AuthenticationMode: ekstypes.AuthenticationModeConfigMap},
	})

	var invalid *ekstypes.InvalidParameterException
	if !errors.As(err, &invalid) {
		t.Fatalf("want InvalidParameterException, got %v", err)
	}

	want := "Unsupported authentication mode update from API_AND_CONFIG_MAP to CONFIG_MAP"
	if aws.ToString(invalid.Message) != want {
		t.Fatalf("message = %q, want %q", aws.ToString(invalid.Message), want)
	}

	if _, err := client.UpdateClusterConfig(ctx, &awseks.UpdateClusterConfigInput{
		Name:         aws.String("oneway"),
		AccessConfig: &ekstypes.UpdateAccessConfigRequest{AuthenticationMode: ekstypes.AuthenticationModeApi},
	}); err != nil {
		t.Fatalf("forward update: %v", err)
	}
}
