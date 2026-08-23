package iam_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// TestSDKUpdateAccessKeyStatus proves UpdateAccessKey can deactivate a key and
// the new status is reflected in ListAccessKeys (previously undispatched →
// InvalidAction).
func TestSDKUpdateAccessKeyStatus(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("keyed")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	created, err := client.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{UserName: aws.String("keyed")})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	keyID := aws.ToString(created.AccessKey.AccessKeyId)

	if _, err := client.UpdateAccessKey(ctx, &iam.UpdateAccessKeyInput{
		UserName:    aws.String("keyed"),
		AccessKeyId: aws.String(keyID),
		Status:      iamtypes.StatusTypeInactive,
	}); err != nil {
		t.Fatalf("UpdateAccessKey: %v", err)
	}

	listed, err := client.ListAccessKeys(ctx, &iam.ListAccessKeysInput{UserName: aws.String("keyed")})
	if err != nil {
		t.Fatalf("ListAccessKeys: %v", err)
	}

	if len(listed.AccessKeyMetadata) != 1 {
		t.Fatalf("ListAccessKeys = %d keys, want 1", len(listed.AccessKeyMetadata))
	}

	if status := listed.AccessKeyMetadata[0].Status; status != iamtypes.StatusTypeInactive {
		t.Fatalf("access key status = %q, want Inactive", status)
	}
}
