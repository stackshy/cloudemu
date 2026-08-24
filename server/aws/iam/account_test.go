package iam_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

func TestSDKGetAccountSummary(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	for _, name := range []string{"u1", "u2"} {
		if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String(name)}); err != nil {
			t.Fatalf("CreateUser %s: %v", name, err)
		}
	}

	out, err := client.GetAccountSummary(ctx, &iam.GetAccountSummaryInput{})
	if err != nil {
		t.Fatalf("GetAccountSummary: %v", err)
	}

	if got := out.SummaryMap["Users"]; got != 2 {
		t.Fatalf("SummaryMap[Users] = %d, want 2", got)
	}

	if got := out.SummaryMap["AccessKeysPerUserQuota"]; got != 2 {
		t.Fatalf("SummaryMap[AccessKeysPerUserQuota] = %d, want 2", got)
	}

	if got := out.SummaryMap["UsersQuota"]; got != 5000 {
		t.Fatalf("SummaryMap[UsersQuota] = %d, want 5000", got)
	}
}

func TestSDKAccountPasswordPolicy(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	// No policy set yet -> NoSuchEntity.
	_, err := client.GetAccountPasswordPolicy(ctx, &iam.GetAccountPasswordPolicyInput{})

	var notFound *iamtypes.NoSuchEntityException
	if !errors.As(err, &notFound) {
		t.Fatalf("GetAccountPasswordPolicy before set: want NoSuchEntityException, got %v", err)
	}

	if _, err := client.UpdateAccountPasswordPolicy(ctx, &iam.UpdateAccountPasswordPolicyInput{
		MinimumPasswordLength: aws.Int32(12),
		RequireSymbols:        true,
		MaxPasswordAge:        aws.Int32(90),
	}); err != nil {
		t.Fatalf("UpdateAccountPasswordPolicy: %v", err)
	}

	got, err := client.GetAccountPasswordPolicy(ctx, &iam.GetAccountPasswordPolicyInput{})
	if err != nil {
		t.Fatalf("GetAccountPasswordPolicy: %v", err)
	}

	if aws.ToInt32(got.PasswordPolicy.MinimumPasswordLength) != 12 {
		t.Fatalf("MinimumPasswordLength = %d, want 12", aws.ToInt32(got.PasswordPolicy.MinimumPasswordLength))
	}

	if !got.PasswordPolicy.RequireSymbols {
		t.Fatal("RequireSymbols = false, want true")
	}

	if !got.PasswordPolicy.ExpirePasswords {
		t.Fatal("ExpirePasswords = false, want true (MaxPasswordAge set)")
	}

	if aws.ToInt32(got.PasswordPolicy.MaxPasswordAge) != 90 {
		t.Fatalf("MaxPasswordAge = %d, want 90", aws.ToInt32(got.PasswordPolicy.MaxPasswordAge))
	}

	if _, err := client.DeleteAccountPasswordPolicy(ctx, &iam.DeleteAccountPasswordPolicyInput{}); err != nil {
		t.Fatalf("DeleteAccountPasswordPolicy: %v", err)
	}

	_, err = client.GetAccountPasswordPolicy(ctx, &iam.GetAccountPasswordPolicyInput{})
	if !errors.As(err, &notFound) {
		t.Fatalf("GetAccountPasswordPolicy after delete: want NoSuchEntityException, got %v", err)
	}
}
