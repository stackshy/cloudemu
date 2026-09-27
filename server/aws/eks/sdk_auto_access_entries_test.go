package eks_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const (
	sdkAccountID = "123456789012"
	sdkNodeRole  = "arn:aws:iam::123456789012:role/eks-node"
	sdkPodRole   = "arn:aws:iam::123456789012:role/eks-pods"
)

// identityServer serves EKS, STS and IAM from one cloud so EKS can resolve
// the caller the same way GetCallerIdentity does.
func identityServer(t *testing.T) (*httptest.Server, *awsprovider.Provider) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{
		EKS: cloud.EKS, IAM: cloud.IAM, STS: true, AccountID: sdkAccountID, Region: "us-east-1",
	}))
	t.Cleanup(ts.Close)

	return ts, cloud
}

func sdkConfig(t *testing.T, akid, secret, token string) aws.Config {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(akid, secret, token)),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return cfg
}

func eksClientFor(url string, cfg aws.Config) *awseks.Client {
	return awseks.NewFromConfig(cfg, func(o *awseks.Options) { o.BaseEndpoint = aws.String(url) })
}

func createAPICluster(t *testing.T, client *awseks.Client, name string) {
	t.Helper()

	if _, err := client.CreateCluster(context.Background(), &awseks.CreateClusterInput{
		Name:               aws.String(name),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks-cluster"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
		AccessConfig:       &ekstypes.CreateAccessConfigRequest{AuthenticationMode: ekstypes.AuthenticationModeApi},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
}

func adminEntries(t *testing.T, client *awseks.Client, cluster string) []string {
	t.Helper()

	out, err := client.ListAccessEntries(context.Background(), &awseks.ListAccessEntriesInput{
		ClusterName: aws.String(cluster), AssociatedPolicyArn: aws.String(sdkAdminPolicy),
	})
	if err != nil {
		t.Fatalf("ListAccessEntries: %v", err)
	}

	return out.AccessEntries
}

// TestSDKCreatorFromAssumedRoleSession checks that a cluster created with
// credentials from sts:AssumeRole gets its admin entry for the role, the
// identity GetCallerIdentity reports for those credentials.
func TestSDKCreatorFromAssumedRoleSession(t *testing.T) {
	ctx := context.Background()
	ts, cloud := identityServer(t)

	if _, err := cloud.IAM.CreateRole(ctx, iamdriver.RoleConfig{
		Name: "platform-admin",
		AssumeRolePolicyDoc: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow",` +
			`"Principal":{"AWS":"*"},"Action":"sts:AssumeRole"}]}`,
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	stsClient := awssts.NewFromConfig(sdkConfig(t, "test", "test", ""), func(o *awssts.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	assumed, err := stsClient.AssumeRole(ctx, &awssts.AssumeRoleInput{
		RoleArn: aws.String("arn:aws:iam::123456789012:role/platform-admin"), RoleSessionName: aws.String("ci"),
	})
	if err != nil {
		t.Fatalf("AssumeRole: %v", err)
	}

	c := assumed.Credentials
	client := eksClientFor(ts.URL, sdkConfig(t,
		aws.ToString(c.AccessKeyId), aws.ToString(c.SecretAccessKey), aws.ToString(c.SessionToken)))
	createAPICluster(t, client, "assumed")

	got := adminEntries(t, client, "assumed")
	if len(got) != 1 || got[0] != "arn:aws:iam::123456789012:role/platform-admin" {
		t.Fatalf("creator entries = %v, want the assumed role", got)
	}
}

// TestSDKCreatorFromFederatedUser checks that a federated user session gets
// no creator entry, since EKS can't use it as an access entry principal.
func TestSDKCreatorFromFederatedUser(t *testing.T) {
	ctx := context.Background()
	ts, _ := identityServer(t)

	stsClient := awssts.NewFromConfig(sdkConfig(t, "test", "test", ""), func(o *awssts.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	fed, err := stsClient.GetFederationToken(ctx, &awssts.GetFederationTokenInput{Name: aws.String("bob")})
	if err != nil {
		t.Fatalf("GetFederationToken: %v", err)
	}

	c := fed.Credentials
	client := eksClientFor(ts.URL, sdkConfig(t,
		aws.ToString(c.AccessKeyId), aws.ToString(c.SecretAccessKey), aws.ToString(c.SessionToken)))
	createAPICluster(t, client, "fed")

	out, err := client.ListAccessEntries(ctx, &awseks.ListAccessEntriesInput{ClusterName: aws.String("fed")})
	if err != nil || len(out.AccessEntries) != 0 {
		t.Fatalf("entries = %+v err %v, want none", out, err)
	}
}

// TestSDKCreatorFromIAMUserKey checks that a long-term key maps to the IAM
// user that owns it.
func TestSDKCreatorFromIAMUserKey(t *testing.T) {
	ctx := context.Background()
	ts, cloud := identityServer(t)

	if _, err := cloud.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "carol"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	ak, err := cloud.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{UserName: "carol"})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	client := eksClientFor(ts.URL, sdkConfig(t, ak.AccessKeyID, ak.SecretAccessKey, ""))
	createAPICluster(t, client, "userkey")

	got := adminEntries(t, client, "userkey")
	if len(got) != 1 || got[0] != "arn:aws:iam::123456789012:user/carol" {
		t.Fatalf("creator entries = %v, want user/carol", got)
	}
}

// TestSDKNodegroupAndFargateEntries checks the entries EKS makes for a
// managed nodegroup and a Fargate profile, and that they go with them.
func TestSDKNodegroupAndFargateEntries(t *testing.T) {
	ctx := context.Background()
	client := newSDKClient(t)
	createClusterWithMode(t, client, "auto", ekstypes.AuthenticationModeApi)

	if _, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName: aws.String("auto"), NodegroupName: aws.String("ng"), NodeRole: aws.String(sdkNodeRole),
		Subnets: []string{"subnet-1"},
	}); err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	if _, err := client.CreateFargateProfile(ctx, &awseks.CreateFargateProfileInput{
		ClusterName: aws.String("auto"), FargateProfileName: aws.String("fp"),
		PodExecutionRoleArn: aws.String(sdkPodRole),
		Selectors:           []ekstypes.FargateProfileSelector{{Namespace: aws.String("default")}},
	}); err != nil {
		t.Fatalf("CreateFargateProfile: %v", err)
	}

	list, err := client.ListAccessEntries(ctx, &awseks.ListAccessEntriesInput{ClusterName: aws.String("auto")})
	if err != nil {
		t.Fatalf("ListAccessEntries: %v", err)
	}

	sort.Strings(list.AccessEntries)

	if len(list.AccessEntries) != 2 || list.AccessEntries[0] != sdkNodeRole || list.AccessEntries[1] != sdkPodRole {
		t.Fatalf("entries = %v", list.AccessEntries)
	}

	node, err := client.DescribeAccessEntry(ctx, &awseks.DescribeAccessEntryInput{
		ClusterName: aws.String("auto"), PrincipalArn: aws.String(sdkNodeRole),
	})
	if err != nil || aws.ToString(node.AccessEntry.Type) != "EC2_LINUX" {
		t.Fatalf("node entry = %+v err %v", node, err)
	}

	pod, err := client.DescribeAccessEntry(ctx, &awseks.DescribeAccessEntryInput{
		ClusterName: aws.String("auto"), PrincipalArn: aws.String(sdkPodRole),
	})
	if err != nil || aws.ToString(pod.AccessEntry.Type) != "FARGATE_LINUX" {
		t.Fatalf("fargate entry = %+v err %v", pod, err)
	}

	if _, err := client.DeleteNodegroup(ctx, &awseks.DeleteNodegroupInput{
		ClusterName: aws.String("auto"), NodegroupName: aws.String("ng"),
	}); err != nil {
		t.Fatalf("DeleteNodegroup: %v", err)
	}

	if _, err := client.DeleteFargateProfile(ctx, &awseks.DeleteFargateProfileInput{
		ClusterName: aws.String("auto"), FargateProfileName: aws.String("fp"),
	}); err != nil {
		t.Fatalf("DeleteFargateProfile: %v", err)
	}

	list, err = client.ListAccessEntries(ctx, &awseks.ListAccessEntriesInput{ClusterName: aws.String("auto")})
	if err != nil || len(list.AccessEntries) != 0 {
		t.Fatalf("entries after delete = %+v err %v", list, err)
	}
}

// TestSDKUpdateNodegroupVersionLaunchTemplate checks that the launch
// template version in UpdateNodegroupVersion is applied.
func TestSDKUpdateNodegroupVersionLaunchTemplate(t *testing.T) {
	ctx := context.Background()
	client := newSDKClient(t)
	createClusterWithMode(t, client, "ltv", ekstypes.AuthenticationModeApi)

	if _, err := client.CreateNodegroup(ctx, &awseks.CreateNodegroupInput{
		ClusterName: aws.String("ltv"), NodegroupName: aws.String("ng"), NodeRole: aws.String(sdkNodeRole),
		Subnets:        []string{"subnet-1"},
		LaunchTemplate: &ekstypes.LaunchTemplateSpecification{Id: aws.String("lt-0123"), Version: aws.String("1")},
	}); err != nil {
		t.Fatalf("CreateNodegroup: %v", err)
	}

	if _, err := client.UpdateNodegroupVersion(ctx, &awseks.UpdateNodegroupVersionInput{
		ClusterName: aws.String("ltv"), NodegroupName: aws.String("ng"),
		LaunchTemplate: &ekstypes.LaunchTemplateSpecification{Id: aws.String("lt-0123"), Version: aws.String("2")},
	}); err != nil {
		t.Fatalf("UpdateNodegroupVersion: %v", err)
	}

	desc, err := client.DescribeNodegroup(ctx, &awseks.DescribeNodegroupInput{
		ClusterName: aws.String("ltv"), NodegroupName: aws.String("ng"),
	})
	if err != nil || desc.Nodegroup.LaunchTemplate == nil || aws.ToString(desc.Nodegroup.LaunchTemplate.Version) != "2" {
		t.Fatalf("launch template after update = %+v err %v", desc, err)
	}

	_, err = client.UpdateNodegroupVersion(ctx, &awseks.UpdateNodegroupVersionInput{
		ClusterName: aws.String("ltv"), NodegroupName: aws.String("ng"),
		LaunchTemplate: &ekstypes.LaunchTemplateSpecification{Id: aws.String("lt-other"), Version: aws.String("1")},
	})

	var invalid *ekstypes.InvalidParameterException
	if !errors.As(err, &invalid) {
		t.Fatalf("switching templates: want InvalidParameterException, got %v", err)
	}
}
