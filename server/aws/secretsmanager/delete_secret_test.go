package secretsmanager_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// TestSDKDescribeSecretScheduledForDeletion guards that DescribeSecret keeps
// working for a secret scheduled for deletion, returning 200 with DeletedDate
// set rather than ResourceNotFoundException.
func TestSDKDescribeSecretScheduledForDeletion(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("svc/db"), SecretString: aws.String("v1"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	if _, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{SecretId: aws.String("svc/db")}); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	desc, err := client.DescribeSecret(ctx, &awssm.DescribeSecretInput{SecretId: aws.String("svc/db")})
	if err != nil {
		t.Fatalf("DescribeSecret on scheduled-for-deletion secret: %v", err)
	}

	if desc.DeletedDate == nil {
		t.Fatal("DescribeSecret: DeletedDate not set for a scheduled-for-deletion secret")
	}
}

// TestSDKGetPutValueScheduledForDeletion guards that GetSecretValue and
// PutSecretValue on a secret scheduled for deletion return
// InvalidRequestException, not ResourceNotFoundException.
func TestSDKGetPutValueScheduledForDeletion(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("svc/db"), SecretString: aws.String("v1"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	if _, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{SecretId: aws.String("svc/db")}); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	var invalidReq *smtypes.InvalidRequestException

	if _, err := client.GetSecretValue(ctx, &awssm.GetSecretValueInput{
		SecretId: aws.String("svc/db"),
	}); !errors.As(err, &invalidReq) {
		t.Fatalf("GetSecretValue(scheduled for deletion): got %v, want InvalidRequestException", err)
	}

	if _, err := client.PutSecretValue(ctx, &awssm.PutSecretValueInput{
		SecretId: aws.String("svc/db"), SecretString: aws.String("v2"),
	}); !errors.As(err, &invalidReq) {
		t.Fatalf("PutSecretValue(scheduled for deletion): got %v, want InvalidRequestException", err)
	}
}

// TestSDKForceDeleteWithoutRecovery guards that ForceDeleteWithoutRecovery
// permanently removes the secret so RestoreSecret afterwards fails.
func TestSDKForceDeleteWithoutRecovery(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("force/me"), SecretString: aws.String("v1"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	if _, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{
		SecretId: aws.String("force/me"), ForceDeleteWithoutRecovery: aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteSecret(force): %v", err)
	}

	var notFound *smtypes.ResourceNotFoundException

	if _, err := client.RestoreSecret(ctx, &awssm.RestoreSecretInput{
		SecretId: aws.String("force/me"),
	}); !errors.As(err, &notFound) {
		t.Fatalf("RestoreSecret after force delete: got %v, want ResourceNotFoundException", err)
	}
}

// TestSDKDeleteSecretRecoveryWindowHonored guards that RecoveryWindowInDays
// drives the returned DeletionDate: a 7-day and a 30-day window produce
// DeletionDates ~23 days apart.
func TestSDKDeleteSecretRecoveryWindowHonored(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("win/me"), SecretString: aws.String("v1"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	del7, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{
		SecretId: aws.String("win/me"), RecoveryWindowInDays: aws.Int64(7),
	})
	if err != nil {
		t.Fatalf("DeleteSecret(7): %v", err)
	}

	if _, err := client.RestoreSecret(ctx, &awssm.RestoreSecretInput{SecretId: aws.String("win/me")}); err != nil {
		t.Fatalf("RestoreSecret: %v", err)
	}

	del30, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{
		SecretId: aws.String("win/me"), RecoveryWindowInDays: aws.Int64(30),
	})
	if err != nil {
		t.Fatalf("DeleteSecret(30): %v", err)
	}

	if del7.DeletionDate == nil || del30.DeletionDate == nil {
		t.Fatal("DeleteSecret returned nil DeletionDate")
	}

	delta := del30.DeletionDate.Sub(*del7.DeletionDate)
	if delta < 22*24*time.Hour || delta > 24*24*time.Hour {
		t.Fatalf("DeletionDate delta between 7 and 30 day window = %v, want ~23 days", delta)
	}
}

// TestSDKDeleteSecretParameterValidation guards the two DeleteSecret parameter
// rules: RecoveryWindowInDays and ForceDeleteWithoutRecovery are mutually
// exclusive, and RecoveryWindowInDays must be within 7-30.
func TestSDKDeleteSecretParameterValidation(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	for _, name := range []string{"both/me", "range/me"} {
		if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
			Name: aws.String(name), SecretString: aws.String("v1"),
		}); err != nil {
			t.Fatalf("CreateSecret(%s): %v", name, err)
		}
	}

	var invalidParam *smtypes.InvalidParameterException

	if _, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{
		SecretId:                   aws.String("both/me"),
		RecoveryWindowInDays:       aws.Int64(10),
		ForceDeleteWithoutRecovery: aws.Bool(true),
	}); !errors.As(err, &invalidParam) {
		t.Fatalf("DeleteSecret(both params): got %v, want InvalidParameterException", err)
	}

	if _, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{
		SecretId: aws.String("range/me"), RecoveryWindowInDays: aws.Int64(3),
	}); !errors.As(err, &invalidParam) {
		t.Fatalf("DeleteSecret(window=3): got %v, want InvalidParameterException", err)
	}
}

// TestSDKCreateSecretScheduledForDeletion guards that CreateSecret for a name
// currently scheduled for deletion returns InvalidRequestException, not
// ResourceExistsException.
func TestSDKCreateSecretScheduledForDeletion(t *testing.T) {
	client := newSecretsClient(t)
	ctx := context.Background()

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("svc/db"), SecretString: aws.String("v1"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	if _, err := client.DeleteSecret(ctx, &awssm.DeleteSecretInput{SecretId: aws.String("svc/db")}); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}

	var invalidReq *smtypes.InvalidRequestException

	if _, err := client.CreateSecret(ctx, &awssm.CreateSecretInput{
		Name: aws.String("svc/db"), SecretString: aws.String("v2"),
	}); !errors.As(err, &invalidReq) {
		t.Fatalf("CreateSecret(name scheduled for deletion): got %v, want InvalidRequestException", err)
	}
}
