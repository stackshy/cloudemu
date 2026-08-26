package secretsmanager_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// TestSDKBatchGetSecretValue is the F7 reproduction: BatchGetSecretValue over a
// SecretIdList returns SecretValues for the secrets that exist and a per-item
// Errors entry for a missing one, rather than failing the whole call.
func TestSDKBatchGetSecretValue(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	for name, val := range map[string]string{"a": "va", "b": "vb"} {
		if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
			Name: aws.String(name), SecretString: aws.String(val),
		}); err != nil {
			t.Fatalf("CreateSecret(%s): %v", name, err)
		}
	}

	out, err := client.BatchGetSecretValue(ctx, &awssm.BatchGetSecretValueInput{
		SecretIdList: []string{"a", "b", "missing"},
	})
	if err != nil {
		t.Fatalf("BatchGetSecretValue: %v", err)
	}

	if len(out.SecretValues) != 2 {
		t.Fatalf("got %d SecretValues, want 2: %+v", len(out.SecretValues), out.SecretValues)
	}

	byName := map[string]string{}
	for _, v := range out.SecretValues {
		byName[aws.ToString(v.Name)] = aws.ToString(v.SecretString)

		if aws.ToString(v.VersionId) == "" {
			t.Errorf("SecretValue %q has empty VersionId", aws.ToString(v.Name))
		}

		if len(v.VersionStages) != 1 || v.VersionStages[0] != "AWSCURRENT" {
			t.Errorf("SecretValue %q stages = %v, want [AWSCURRENT]", aws.ToString(v.Name), v.VersionStages)
		}
	}

	if byName["a"] != "va" || byName["b"] != "vb" {
		t.Fatalf("values = %+v, want a=va b=vb", byName)
	}

	if len(out.Errors) != 1 {
		t.Fatalf("got %d Errors, want 1: %+v", len(out.Errors), out.Errors)
	}

	if aws.ToString(out.Errors[0].SecretId) != "missing" ||
		aws.ToString(out.Errors[0].ErrorCode) != "ResourceNotFoundException" {
		t.Fatalf("Errors[0] = %+v, want SecretId=missing ErrorCode=ResourceNotFoundException", out.Errors[0])
	}
}

// TestSDKBatchGetSecretValueEmpty verifies an empty SecretIdList is handled
// (no values, no errors) rather than erroring.
func TestSDKBatchGetSecretValueEmpty(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	out, err := client.BatchGetSecretValue(ctx, &awssm.BatchGetSecretValueInput{
		SecretIdList: []string{},
	})
	if err != nil {
		t.Fatalf("BatchGetSecretValue(empty): %v", err)
	}

	if len(out.SecretValues) != 0 || len(out.Errors) != 0 {
		t.Fatalf("empty batch = values %+v errors %+v, want both empty", out.SecretValues, out.Errors)
	}
}
