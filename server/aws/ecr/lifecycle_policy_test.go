package ecr_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
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

// TestSDKECRDeleteLifecyclePolicy exercises the Terraform destroy path: a
// lifecycle policy is put, then deleted. Delete echoes the removed policy body
// and registryId, and a subsequent Get reports the policy is gone.
func TestSDKECRDeleteLifecyclePolicy(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("del-lc-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if _, err := client.PutLifecyclePolicy(ctx, &awsecr.PutLifecyclePolicyInput{
		RepositoryName:      aws.String("del-lc-repo"),
		LifecyclePolicyText: aws.String(lifecyclePolicyDoc),
	}); err != nil {
		t.Fatalf("PutLifecyclePolicy: %v", err)
	}

	del, err := client.DeleteLifecyclePolicy(ctx, &awsecr.DeleteLifecyclePolicyInput{
		RepositoryName: aws.String("del-lc-repo"),
	})
	if err != nil {
		t.Fatalf("DeleteLifecyclePolicy: %v", err)
	}

	if aws.ToString(del.RepositoryName) != "del-lc-repo" {
		t.Fatalf("DeleteLifecyclePolicy repositoryName = %q, want del-lc-repo", aws.ToString(del.RepositoryName))
	}

	if aws.ToString(del.RegistryId) != "123456789012" {
		t.Fatalf("DeleteLifecyclePolicy registryId = %q, want 123456789012", aws.ToString(del.RegistryId))
	}

	if aws.ToString(del.LifecyclePolicyText) == "" {
		t.Fatal("DeleteLifecyclePolicy returned empty lifecyclePolicyText; want the deleted policy body")
	}

	// The policy is gone: Get now reports LifecyclePolicyNotFoundException.
	_, err = client.GetLifecyclePolicy(ctx, &awsecr.GetLifecyclePolicyInput{
		RepositoryName: aws.String("del-lc-repo"),
	})

	var lcNotFound *ecrtypes.LifecyclePolicyNotFoundException
	if !errors.As(err, &lcNotFound) {
		t.Fatalf("Get after delete: want LifecyclePolicyNotFoundException, got %v", err)
	}
}

// TestSDKECRDeleteLifecyclePolicyNoPolicy asserts a repository with no lifecycle
// policy reports LifecyclePolicyNotFoundException on delete, not a generic
// repository error.
func TestSDKECRDeleteLifecyclePolicyNoPolicy(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("no-lc-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	_, err := client.DeleteLifecyclePolicy(ctx, &awsecr.DeleteLifecyclePolicyInput{
		RepositoryName: aws.String("no-lc-repo"),
	})

	var lcNotFound *ecrtypes.LifecyclePolicyNotFoundException
	if !errors.As(err, &lcNotFound) {
		t.Fatalf("delete with no policy: want LifecyclePolicyNotFoundException, got %v", err)
	}
}

// TestSDKECRDeleteLifecyclePolicyMissingRepo asserts deleting a policy on a
// repository that does not exist reports RepositoryNotFoundException.
func TestSDKECRDeleteLifecyclePolicyMissingRepo(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	_, err := client.DeleteLifecyclePolicy(ctx, &awsecr.DeleteLifecyclePolicyInput{
		RepositoryName: aws.String("ghost-repo"),
	})

	var repoNotFound *ecrtypes.RepositoryNotFoundException
	if !errors.As(err, &repoNotFound) {
		t.Fatalf("delete on missing repo: want RepositoryNotFoundException, got %v", err)
	}
}
