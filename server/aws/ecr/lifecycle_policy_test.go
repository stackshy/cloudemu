package ecr_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
)

const lifecyclePolicyDoc = `{"rules":[{"rulePriority":1,"description":"expire",` +
	`"selection":{"tagStatus":"untagged","countType":"imageCountMoreThan","countNumber":10},` +
	`"action":{"type":"expire"}}]}`

// TestSDKECRLifecyclePolicyRegistryID guards that Put/GetLifecyclePolicy echo the
// owning registryId (the account id), which the real ECR API always returns and
// IaC tooling reads.
func TestSDKECRLifecyclePolicyRegistryID(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("lc-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	put, err := client.PutLifecyclePolicy(ctx, &awsecr.PutLifecyclePolicyInput{
		RepositoryName:      aws.String("lc-repo"),
		LifecyclePolicyText: aws.String(lifecyclePolicyDoc),
	})
	if err != nil {
		t.Fatalf("PutLifecyclePolicy: %v", err)
	}

	if aws.ToString(put.RegistryId) != "123456789012" {
		t.Fatalf("PutLifecyclePolicy registryId = %q, want 123456789012", aws.ToString(put.RegistryId))
	}

	got, err := client.GetLifecyclePolicy(ctx, &awsecr.GetLifecyclePolicyInput{
		RepositoryName: aws.String("lc-repo"),
	})
	if err != nil {
		t.Fatalf("GetLifecyclePolicy: %v", err)
	}

	if aws.ToString(got.RegistryId) != "123456789012" {
		t.Fatalf("GetLifecyclePolicy registryId = %q, want 123456789012", aws.ToString(got.RegistryId))
	}

	if aws.ToString(got.RepositoryName) != "lc-repo" {
		t.Fatalf("GetLifecyclePolicy repositoryName = %q, want lc-repo", aws.ToString(got.RepositoryName))
	}
}
