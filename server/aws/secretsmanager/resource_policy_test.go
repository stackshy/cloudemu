package secretsmanager_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

const samplePolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow",` +
	`"Principal":{"AWS":"arn:aws:iam::111122223333:root"},` +
	`"Action":"secretsmanager:GetSecretValue","Resource":"*"}]}`

const publicPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow",` +
	`"Principal":"*","Action":"secretsmanager:GetSecretValue","Resource":"*"}]}`

// TestSDKResourcePolicyRoundTrip is the F3 reproduction: Put/Get/Delete round-trip
// the JSON resource policy, the write path Terraform's
// aws_secretsmanager_secret_policy needs.
func TestSDKResourcePolicyRoundTrip(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	created, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("policy-secret"), SecretString: aws.String("v1"),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	if _, err := client.PutResourcePolicy(ctx, &awssm.PutResourcePolicyInput{
		SecretId: created.ARN, ResourcePolicy: aws.String(samplePolicy),
	}); err != nil {
		t.Fatalf("PutResourcePolicy: %v", err)
	}

	got, err := client.GetResourcePolicy(ctx, &awssm.GetResourcePolicyInput{SecretId: created.ARN})
	if err != nil {
		t.Fatalf("GetResourcePolicy: %v", err)
	}

	if aws.ToString(got.ResourcePolicy) != samplePolicy {
		t.Fatalf("ResourcePolicy round-trip = %q, want %q", aws.ToString(got.ResourcePolicy), samplePolicy)
	}

	if _, err := client.DeleteResourcePolicy(ctx, &awssm.DeleteResourcePolicyInput{SecretId: created.ARN}); err != nil {
		t.Fatalf("DeleteResourcePolicy: %v", err)
	}

	// After delete, GetResourcePolicy still succeeds (not an error) with no policy.
	cleared, err := client.GetResourcePolicy(ctx, &awssm.GetResourcePolicyInput{SecretId: created.ARN})
	if err != nil {
		t.Fatalf("GetResourcePolicy after delete: %v", err)
	}

	if cleared.ResourcePolicy != nil {
		t.Fatalf("ResourcePolicy after delete = %q, want nil", aws.ToString(cleared.ResourcePolicy))
	}
}

// TestSDKValidateResourcePolicy verifies a valid policy passes and a malformed
// one fails with validation errors (without being stored).
func TestSDKValidateResourcePolicy(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	valid, err := client.ValidateResourcePolicy(ctx, &awssm.ValidateResourcePolicyInput{
		ResourcePolicy: aws.String(samplePolicy),
	})
	if err != nil {
		t.Fatalf("ValidateResourcePolicy(valid): %v", err)
	}

	if !valid.PolicyValidationPassed {
		t.Fatalf("valid policy: PolicyValidationPassed = false, errors = %+v", valid.ValidationErrors)
	}

	malformed, err := client.ValidateResourcePolicy(ctx, &awssm.ValidateResourcePolicyInput{
		ResourcePolicy: aws.String("{not valid json"),
	})
	if err != nil {
		t.Fatalf("ValidateResourcePolicy(malformed): %v", err)
	}

	if malformed.PolicyValidationPassed {
		t.Fatal("malformed policy: PolicyValidationPassed = true, want false")
	}

	if len(malformed.ValidationErrors) == 0 {
		t.Fatal("malformed policy: no ValidationErrors returned")
	}
}

// TestSDKBlockPublicPolicy verifies BlockPublicPolicy rejects a policy that
// grants public access ("Principal":"*").
func TestSDKBlockPublicPolicy(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("blocked"), SecretString: aws.String("v1"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	_, err := client.PutResourcePolicy(ctx, &awssm.PutResourcePolicyInput{
		SecretId:          aws.String("blocked"),
		ResourcePolicy:    aws.String(publicPolicy),
		BlockPublicPolicy: aws.Bool(true),
	})

	var public *smtypes.PublicPolicyException
	if !errors.As(err, &public) {
		t.Fatalf("BlockPublicPolicy on public policy: got %v, want PublicPolicyException", err)
	}

	// Without BlockPublicPolicy the same policy is accepted.
	if _, err := client.PutResourcePolicy(ctx, &awssm.PutResourcePolicyInput{
		SecretId: aws.String("blocked"), ResourcePolicy: aws.String(publicPolicy),
	}); err != nil {
		t.Fatalf("PutResourcePolicy(public, no block): %v", err)
	}
}
