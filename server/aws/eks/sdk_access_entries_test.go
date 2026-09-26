package eks_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

const (
	sdkRoleArn     = "arn:aws:iam::123456789012:role/team/dev/deployer"
	sdkAdminPolicy = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"
	sdkViewPolicy  = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy"
)

func createClusterWithMode(t *testing.T, client *awseks.Client, name string, mode ekstypes.AuthenticationMode) {
	t.Helper()

	_, err := client.CreateCluster(context.Background(), &awseks.CreateClusterInput{
		Name:               aws.String(name),
		Version:            aws.String("1.30"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks-cluster"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
		AccessConfig:       &ekstypes.CreateAccessConfigRequest{AuthenticationMode: mode},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
}

// TestSDKAccessEntryLifecycle drives every access entry op through the real
// SDK. The principal ARN has an IAM path, so the %2F in the URL must survive
// the path split.
func TestSDKAccessEntryLifecycle(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	createClusterWithMode(t, client, "ae", ekstypes.AuthenticationModeApiAndConfigMap)

	created, err := client.CreateAccessEntry(ctx, &awseks.CreateAccessEntryInput{
		ClusterName:      aws.String("ae"),
		PrincipalArn:     aws.String(sdkRoleArn),
		KubernetesGroups: []string{"deployers"},
		Tags:             map[string]string{"team": "dev"},
	})
	if err != nil {
		t.Fatalf("CreateAccessEntry: %v", err)
	}

	e := created.AccessEntry
	if aws.ToString(e.Type) != "STANDARD" ||
		aws.ToString(e.Username) != "arn:aws:sts::123456789012:assumed-role/deployer/{{SessionName}}" {
		t.Fatalf("defaults not applied: type %q username %q", aws.ToString(e.Type), aws.ToString(e.Username))
	}

	if e.CreatedAt == nil || e.ModifiedAt == nil || aws.ToString(e.AccessEntryArn) == "" {
		t.Fatalf("computed fields missing: %+v", e)
	}

	_, err = client.CreateAccessEntry(ctx, &awseks.CreateAccessEntryInput{
		ClusterName: aws.String("ae"), PrincipalArn: aws.String(sdkRoleArn),
	})

	var inUse *ekstypes.ResourceInUseException
	if !errors.As(err, &inUse) {
		t.Fatalf("duplicate: want ResourceInUseException, got %v", err)
	}

	desc, err := client.DescribeAccessEntry(ctx, &awseks.DescribeAccessEntryInput{
		ClusterName: aws.String("ae"), PrincipalArn: aws.String(sdkRoleArn),
	})
	if err != nil || aws.ToString(desc.AccessEntry.PrincipalArn) != sdkRoleArn {
		t.Fatalf("DescribeAccessEntry: %+v err %v", desc, err)
	}

	upd, err := client.UpdateAccessEntry(ctx, &awseks.UpdateAccessEntryInput{
		ClusterName: aws.String("ae"), PrincipalArn: aws.String(sdkRoleArn),
		KubernetesGroups: []string{"a", "b"}, Username: aws.String("dev:{{SessionName}}"),
	})
	if err != nil || len(upd.AccessEntry.KubernetesGroups) != 2 || aws.ToString(upd.AccessEntry.Username) != "dev:{{SessionName}}" {
		t.Fatalf("UpdateAccessEntry: %+v err %v", upd, err)
	}

	list, err := client.ListAccessEntries(ctx, &awseks.ListAccessEntriesInput{ClusterName: aws.String("ae")})
	if err != nil || len(list.AccessEntries) != 1 || list.AccessEntries[0] != sdkRoleArn {
		t.Fatalf("ListAccessEntries: %+v err %v", list, err)
	}

	tags, err := client.ListTagsForResource(ctx, &awseks.ListTagsForResourceInput{ResourceArn: e.AccessEntryArn})
	if err != nil || tags.Tags["team"] != "dev" {
		t.Fatalf("ListTagsForResource on entry ARN: %+v err %v", tags, err)
	}

	if _, err := client.DeleteAccessEntry(ctx, &awseks.DeleteAccessEntryInput{
		ClusterName: aws.String("ae"), PrincipalArn: aws.String(sdkRoleArn),
	}); err != nil {
		t.Fatalf("DeleteAccessEntry: %v", err)
	}

	_, err = client.DescribeAccessEntry(ctx, &awseks.DescribeAccessEntryInput{
		ClusterName: aws.String("ae"), PrincipalArn: aws.String(sdkRoleArn),
	})

	var notFound *ekstypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("describe after delete: want ResourceNotFoundException, got %v", err)
	}
}

// TestSDKAccessPolicyAssociation covers Associate, ListAssociated, the
// associatedPolicyArn filter and Disassociate.
func TestSDKAccessPolicyAssociation(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	createClusterWithMode(t, client, "ap", ekstypes.AuthenticationModeApi)

	if _, err := client.CreateAccessEntry(ctx, &awseks.CreateAccessEntryInput{
		ClusterName: aws.String("ap"), PrincipalArn: aws.String(sdkRoleArn),
	}); err != nil {
		t.Fatalf("CreateAccessEntry: %v", err)
	}

	assoc, err := client.AssociateAccessPolicy(ctx, &awseks.AssociateAccessPolicyInput{
		ClusterName: aws.String("ap"), PrincipalArn: aws.String(sdkRoleArn), PolicyArn: aws.String(sdkAdminPolicy),
		AccessScope: &ekstypes.AccessScope{Type: ekstypes.AccessScopeTypeCluster},
	})
	if err != nil {
		t.Fatalf("AssociateAccessPolicy: %v", err)
	}

	if aws.ToString(assoc.PrincipalArn) != sdkRoleArn || aws.ToString(assoc.AssociatedAccessPolicy.PolicyArn) != sdkAdminPolicy ||
		assoc.AssociatedAccessPolicy.AssociatedAt == nil {
		t.Fatalf("associate response: %+v", assoc)
	}

	if _, err := client.AssociateAccessPolicy(ctx, &awseks.AssociateAccessPolicyInput{
		ClusterName: aws.String("ap"), PrincipalArn: aws.String(sdkRoleArn), PolicyArn: aws.String(sdkViewPolicy),
		AccessScope: &ekstypes.AccessScope{Type: ekstypes.AccessScopeTypeNamespace, Namespaces: []string{"dev"}},
	}); err != nil {
		t.Fatalf("AssociateAccessPolicy view: %v", err)
	}

	listed, err := client.ListAssociatedAccessPolicies(ctx, &awseks.ListAssociatedAccessPoliciesInput{
		ClusterName: aws.String("ap"), PrincipalArn: aws.String(sdkRoleArn),
	})
	if err != nil || len(listed.AssociatedAccessPolicies) != 2 || aws.ToString(listed.ClusterName) != "ap" {
		t.Fatalf("ListAssociatedAccessPolicies: %+v err %v", listed, err)
	}

	filtered, err := client.ListAccessEntries(ctx, &awseks.ListAccessEntriesInput{
		ClusterName: aws.String("ap"), AssociatedPolicyArn: aws.String(sdkViewPolicy),
	})
	if err != nil || len(filtered.AccessEntries) != 1 {
		t.Fatalf("filtered ListAccessEntries: %+v err %v", filtered, err)
	}

	if _, err := client.DisassociateAccessPolicy(ctx, &awseks.DisassociateAccessPolicyInput{
		ClusterName: aws.String("ap"), PrincipalArn: aws.String(sdkRoleArn), PolicyArn: aws.String(sdkViewPolicy),
	}); err != nil {
		t.Fatalf("DisassociateAccessPolicy: %v", err)
	}

	_, err = client.AssociateAccessPolicy(ctx, &awseks.AssociateAccessPolicyInput{
		ClusterName: aws.String("ap"), PrincipalArn: aws.String(sdkRoleArn), PolicyArn: aws.String(sdkAdminPolicy),
		AccessScope: &ekstypes.AccessScope{Type: ekstypes.AccessScopeTypeNamespace},
	})

	var invalid *ekstypes.InvalidParameterException
	if !errors.As(err, &invalid) {
		t.Fatalf("namespace scope without namespaces: want InvalidParameterException, got %v", err)
	}
}

// TestSDKAccessEntriesRejectedOnConfigMapCluster checks the real error for a
// cluster whose authentication mode is CONFIG_MAP.
func TestSDKAccessEntriesRejectedOnConfigMapCluster(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()
	createClusterWithMode(t, client, "cm", ekstypes.AuthenticationModeConfigMap)

	_, err := client.CreateAccessEntry(ctx, &awseks.CreateAccessEntryInput{
		ClusterName: aws.String("cm"), PrincipalArn: aws.String(sdkRoleArn),
	})

	var invalidReq *ekstypes.InvalidRequestException
	if !errors.As(err, &invalidReq) {
		t.Fatalf("want InvalidRequestException, got %v", err)
	}

	want := "The cluster's authentication mode must be set to one of [API, API_AND_CONFIG_MAP] to perform this operation."
	if aws.ToString(invalidReq.Message) != want {
		t.Fatalf("message = %q, want %q", aws.ToString(invalidReq.Message), want)
	}
}

func TestSDKListAccessPolicies(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	first, err := client.ListAccessPolicies(ctx, &awseks.ListAccessPoliciesInput{MaxResults: aws.Int32(5)})
	if err != nil || len(first.AccessPolicies) != 5 || first.NextToken == nil {
		t.Fatalf("first page: %+v err %v", first, err)
	}

	total := 0
	found := false

	pager := awseks.NewListAccessPoliciesPaginator(client, &awseks.ListAccessPoliciesInput{MaxResults: aws.Int32(5)})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("page: %v", err)
		}

		for _, p := range page.AccessPolicies {
			total++

			if aws.ToString(p.Arn) == sdkAdminPolicy && aws.ToString(p.Name) == "AmazonEKSClusterAdminPolicy" {
				found = true
			}
		}
	}

	if !found || total < 20 {
		t.Fatalf("paged %d policies, cluster admin found=%v", total, found)
	}
}
