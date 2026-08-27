package ecr_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

// multiRulePolicyDoc is a two-rule policy whose first rule selects on a
// multi-element tagPrefixList. Earlier code collapsed the list to its first
// element on read; this document exercises full-fidelity round-tripping.
const multiRulePolicyDoc = `{"rules":[` +
	`{"rulePriority":1,"description":"keep recent prod/stg",` +
	`"selection":{"tagStatus":"tagged","tagPrefixList":["prod","stg"],` +
	`"countType":"imageCountMoreThan","countNumber":5},"action":{"type":"expire"}},` +
	`{"rulePriority":2,"description":"expire untagged",` +
	`"selection":{"tagStatus":"untagged","countType":"sinceImagePushed","countNumber":14},` +
	`"action":{"type":"expire"}}]}`

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

// TestSDKECRLifecyclePolicyRoundTrip guards fidelity of a multi-rule policy with
// a multi-element tagPrefixList. GetLifecyclePolicy must return the document
// exactly as PutLifecyclePolicy stored it — both rules and both prefix entries —
// so Terraform's aws_ecr_lifecycle_policy refresh sees no drift.
func TestSDKECRLifecyclePolicyRoundTrip(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
		RepositoryName: aws.String("rt-repo"),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	if _, err := client.PutLifecyclePolicy(ctx, &awsecr.PutLifecyclePolicyInput{
		RepositoryName:      aws.String("rt-repo"),
		LifecyclePolicyText: aws.String(multiRulePolicyDoc),
	}); err != nil {
		t.Fatalf("PutLifecyclePolicy: %v", err)
	}

	got, err := client.GetLifecyclePolicy(ctx, &awsecr.GetLifecyclePolicyInput{
		RepositoryName: aws.String("rt-repo"),
	})
	if err != nil {
		t.Fatalf("GetLifecyclePolicy: %v", err)
	}

	gotText := aws.ToString(got.LifecyclePolicyText)
	if gotText != multiRulePolicyDoc {
		t.Fatalf("GetLifecyclePolicy text not verbatim:\n got=%s\nwant=%s", gotText, multiRulePolicyDoc)
	}

	// Structural check independent of byte-for-byte equality: both rules present,
	// and the first rule's tagPrefixList retains both elements.
	var in, out map[string]any
	if err := json.Unmarshal([]byte(multiRulePolicyDoc), &in); err != nil {
		t.Fatalf("unmarshal input: %v", err)
	}

	if err := json.Unmarshal([]byte(gotText), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if !reflect.DeepEqual(in, out) {
		t.Fatalf("policy structure drifted:\n in=%v\nout=%v", in, out)
	}

	rules, _ := out["rules"].([]any)
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(rules))
	}

	sel, _ := rules[0].(map[string]any)["selection"].(map[string]any)
	prefixes, _ := sel["tagPrefixList"].([]any)
	if len(prefixes) != 2 {
		t.Fatalf("tagPrefixList = %v, want 2 elements", prefixes)
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
