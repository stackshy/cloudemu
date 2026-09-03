package secretsmanager_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// TestSDKSecretBinaryRoundTrip guards the binary round-trip fix: SecretBinary
// must come back byte-for-byte with SecretString empty (previously the bytes
// were corrupted into SecretString via UTF-8 replacement).
func TestSDKSecretBinaryRoundTrip(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	payload := []byte{0, 1, 2, 255, 128}

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name:         aws.String("bin"),
		SecretBinary: payload,
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	got, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("bin")})
	if err != nil {
		t.Fatalf("GetSecretValue: %v", err)
	}

	if !bytes.Equal(got.SecretBinary, payload) {
		t.Fatalf("SecretBinary = %v, want %v", got.SecretBinary, payload)
	}

	if aws.ToString(got.SecretString) != "" {
		t.Fatalf("SecretString = %q, want empty for a binary secret", aws.ToString(got.SecretString))
	}
}

// TestSDKDescribeSecretVersionStages guards VersionIdsToStages being populated
// with AWSCURRENT/AWSPREVIOUS.
func TestSDKDescribeSecretVersionStages(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	created, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("staged"), SecretString: aws.String("v1"),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	put, err := client.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId: aws.String("staged"), SecretString: aws.String("v2"),
	})
	if err != nil {
		t.Fatalf("PutSecretValue: %v", err)
	}

	desc, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("staged")})
	if err != nil {
		t.Fatalf("DescribeSecret: %v", err)
	}

	stages := desc.VersionIdsToStages
	if len(stages) != 2 {
		t.Fatalf("VersionIdsToStages = %+v, want 2 entries", stages)
	}

	if got := stages[aws.ToString(put.VersionId)]; len(got) != 1 || got[0] != "AWSCURRENT" {
		t.Fatalf("current version stages = %v, want [AWSCURRENT]", got)
	}

	if got := stages[aws.ToString(created.VersionId)]; len(got) != 1 || got[0] != "AWSPREVIOUS" {
		t.Fatalf("previous version stages = %v, want [AWSPREVIOUS]", got)
	}
}

// TestSDKGetSecretValueByStage guards VersionStage=AWSPREVIOUS returning the
// superseded value rather than the current one.
func TestSDKGetSecretValueByStage(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("rot"), SecretString: aws.String("old"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	if _, err := client.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId: aws.String("rot"), SecretString: aws.String("new"),
	}); err != nil {
		t.Fatalf("PutSecretValue: %v", err)
	}

	prev, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId: aws.String("rot"), VersionStage: aws.String("AWSPREVIOUS"),
	})
	if err != nil {
		t.Fatalf("GetSecretValue(AWSPREVIOUS): %v", err)
	}

	if aws.ToString(prev.SecretString) != "old" {
		t.Fatalf("AWSPREVIOUS value = %q, want old", aws.ToString(prev.SecretString))
	}
}

// TestSDKDeleteRestoreSecret guards DeleteSecret returning a DeletionDate and
// RestoreSecret making a soft-deleted secret usable again.
func TestSDKDeleteRestoreSecret(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("recoverable"), SecretString: aws.String("v"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	del, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{SecretId: aws.String("recoverable")})
	if err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	if del.DeletionDate == nil {
		t.Fatal("DeleteSecret returned nil DeletionDate")
	}

	if _, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("recoverable")}); err == nil {
		t.Fatal("GetSecretValue after delete: want error, got nil")
	}

	restored, err := client.RestoreSecret(ctx, &awssm.RestoreSecretInput{SecretId: aws.String("recoverable")})
	if err != nil {
		t.Fatalf("RestoreSecret: %v", err)
	}

	if aws.ToString(restored.Name) != "recoverable" {
		t.Fatalf("RestoreSecret echoed name %q", aws.ToString(restored.Name))
	}

	val, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{SecretId: aws.String("recoverable")})
	if err != nil {
		t.Fatalf("GetSecretValue after restore: %v", err)
	}

	if aws.ToString(val.SecretString) != "v" {
		t.Fatalf("restored value = %q, want v", aws.ToString(val.SecretString))
	}
}

// TestSDKRotateSecret guards RotateSecret being dispatched and advancing the
// version.
func TestSDKRotateSecret(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	created, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("torotate"), SecretString: aws.String("v"),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	rot, err := client.RotateSecret(ctx, &awssm.RotateSecretInput{
		SecretId: aws.String("torotate"), RotateImmediately: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("RotateSecret: %v", err)
	}

	if aws.ToString(rot.VersionId) == "" {
		t.Fatal("RotateSecret returned empty VersionId")
	}

	if aws.ToString(rot.VersionId) == aws.ToString(created.VersionId) {
		t.Fatal("RotateSecret did not advance the version")
	}
}

// TestSDKRotateSecretConfig guards RotateSecret storing RotationLambdaARN and
// RotationRules and DescribeSecret echoing RotationEnabled/LastRotatedDate.
func TestSDKRotateSecretConfig(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("rotconfig"), SecretString: aws.String("v"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	lambdaARN := "arn:aws:lambda:us-east-1:123456789012:function:rotate"

	if _, err := client.RotateSecret(ctx, &awssm.RotateSecretInput{
		SecretId:          aws.String("rotconfig"),
		RotationLambdaARN: aws.String(lambdaARN),
		RotationRules:     &smtypes.RotationRulesType{AutomaticallyAfterDays: aws.Int64(30)},
		RotateImmediately: aws.Bool(true),
	}); err != nil {
		t.Fatalf("RotateSecret: %v", err)
	}

	desc, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("rotconfig")})
	if err != nil {
		t.Fatalf("DescribeSecret: %v", err)
	}

	if !aws.ToBool(desc.RotationEnabled) {
		t.Fatal("RotationEnabled = false, want true after RotateSecret")
	}

	if aws.ToString(desc.RotationLambdaARN) != lambdaARN {
		t.Fatalf("RotationLambdaARN = %q, want %q", aws.ToString(desc.RotationLambdaARN), lambdaARN)
	}

	if desc.RotationRules == nil || aws.ToInt64(desc.RotationRules.AutomaticallyAfterDays) != 30 {
		t.Fatalf("RotationRules = %+v, want AutomaticallyAfterDays=30", desc.RotationRules)
	}

	if desc.LastRotatedDate == nil {
		t.Fatal("LastRotatedDate is nil after an immediate rotation")
	}

	if desc.NextRotationDate == nil {
		t.Fatal("NextRotationDate is nil with AutomaticallyAfterDays set")
	}
}

// TestSDKRotateSecretDeferred guards RotateImmediately=false configuring
// rotation without advancing the version.
func TestSDKRotateSecretDeferred(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	created, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("rotdeferred"), SecretString: aws.String("v"),
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	rot, err := client.RotateSecret(ctx, &awssm.RotateSecretInput{
		SecretId:          aws.String("rotdeferred"),
		RotationLambdaARN: aws.String("arn:aws:lambda:us-east-1:123456789012:function:rotate"),
		RotateImmediately: aws.Bool(false),
	})
	if err != nil {
		t.Fatalf("RotateSecret: %v", err)
	}

	if aws.ToString(rot.VersionId) != aws.ToString(created.VersionId) {
		t.Fatal("RotateImmediately=false must not advance the version")
	}

	desc, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("rotdeferred")})
	if err != nil {
		t.Fatalf("DescribeSecret: %v", err)
	}

	if !aws.ToBool(desc.RotationEnabled) {
		t.Fatal("RotationEnabled = false, want true after a configure-only RotateSecret")
	}

	if desc.LastRotatedDate != nil {
		t.Fatal("LastRotatedDate must stay unset when no rotation actually ran")
	}
}

// TestSDKCancelRotateSecret guards CancelRotateSecret disabling rotation while
// keeping the configured lambda ARN, and rejecting an unknown secret.
func TestSDKCancelRotateSecret(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("rotcancel"), SecretString: aws.String("v"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	lambdaARN := "arn:aws:lambda:us-east-1:123456789012:function:rotate"

	if _, err := client.RotateSecret(ctx, &awssm.RotateSecretInput{
		SecretId: aws.String("rotcancel"), RotationLambdaARN: aws.String(lambdaARN), RotateImmediately: aws.Bool(true),
	}); err != nil {
		t.Fatalf("RotateSecret: %v", err)
	}

	if _, err := client.CancelRotateSecret(ctx, &awssm.CancelRotateSecretInput{
		SecretId: aws.String("rotcancel"),
	}); err != nil {
		t.Fatalf("CancelRotateSecret: %v", err)
	}

	desc, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("rotcancel")})
	if err != nil {
		t.Fatalf("DescribeSecret: %v", err)
	}

	if aws.ToBool(desc.RotationEnabled) {
		t.Fatal("RotationEnabled = true after CancelRotateSecret, want false")
	}

	if aws.ToString(desc.RotationLambdaARN) != lambdaARN {
		t.Fatal("CancelRotateSecret must not clear the configured RotationLambdaARN")
	}

	if _, err := client.CancelRotateSecret(ctx, &awssm.CancelRotateSecretInput{
		SecretId: aws.String("does-not-exist"),
	}); err == nil {
		t.Fatal("CancelRotateSecret on an unknown secret: want error, got nil")
	}
}

// TestSDKGetRandomPassword guards GetRandomPassword being dispatched.
func TestSDKGetRandomPassword(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	out, err := client.GetRandomPassword(ctx, &awssm.GetRandomPasswordInput{
		PasswordLength:     aws.Int64(24),
		ExcludePunctuation: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("GetRandomPassword: %v", err)
	}

	if len([]rune(aws.ToString(out.RandomPassword))) != 24 {
		t.Fatalf("password length = %d, want 24", len([]rune(aws.ToString(out.RandomPassword))))
	}
}

// TestSDKListSecretsFilterAndPaginate guards MaxResults/NextToken/Filters being
// honored.
func TestSDKListSecretsFilterAndPaginate(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	for _, n := range []string{"app-a", "app-b", "db-c"} {
		if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
			Name: aws.String(n), SecretString: aws.String("v"),
		}); err != nil {
			t.Fatalf("CreateSecret(%s): %v", n, err)
		}
	}

	// Filter to the two "app-" secrets.
	filtered, err := client.ListSecrets(ctx, &awssm.ListSecretsInput{
		Filters: []smtypes.Filter{{
			Key:    smtypes.FilterNameStringTypeName,
			Values: []string{"app"},
		}},
	})
	if err != nil {
		t.Fatalf("ListSecrets(filter): %v", err)
	}

	if len(filtered.SecretList) != 2 {
		t.Fatalf("filtered list = %d entries, want 2", len(filtered.SecretList))
	}

	// Paginate one at a time across all three.
	seen := 0
	var token *string

	for {
		page, perr := client.ListSecrets(ctx, &awssm.ListSecretsInput{
			MaxResults: aws.Int32(1),
			NextToken:  token,
		})
		if perr != nil {
			t.Fatalf("ListSecrets(page): %v", perr)
		}

		if len(page.SecretList) > 1 {
			t.Fatalf("page returned %d entries, want at most 1", len(page.SecretList))
		}

		seen += len(page.SecretList)

		if page.NextToken == nil {
			break
		}

		token = page.NextToken
	}

	if seen != 3 {
		t.Fatalf("paginated total = %d, want 3", seen)
	}
}
