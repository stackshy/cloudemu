package kms_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// TestSDKReservedTagPrefix: tags in the aws: namespace fail with TagException
// on both CreateKey and TagResource.
func TestSDKReservedTagPrefix(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	_, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		Tags: []kmstypes.Tag{{TagKey: aws.String("aws:owner"), TagValue: aws.String("x")}},
	})

	var tagErr *kmstypes.TagException
	if !errors.As(err, &tagErr) {
		t.Fatalf("CreateKey: err = %v, want TagException", err)
	}

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.TagResource(ctx, &awskms.TagResourceInput{
		KeyId: key.KeyMetadata.KeyId,
		Tags:  []kmstypes.Tag{{TagKey: aws.String("aws:owner"), TagValue: aws.String("x")}},
	})
	if !errors.As(err, &tagErr) {
		t.Fatalf("TagResource: err = %v, want TagException", err)
	}
}

// TestSDKCreateGrantInvalidPrincipal: a non-ARN grantee, or an IAM ARN that is
// not an account, user or role, fails with InvalidArnException, while a role
// ARN succeeds.
func TestSDKCreateGrantInvalidPrincipal(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	key, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.CreateGrant(ctx, &awskms.CreateGrantInput{
		KeyId: key.KeyMetadata.KeyId, GranteePrincipal: aws.String("not-an-arn"),
		Operations: []kmstypes.GrantOperation{kmstypes.GrantOperationEncrypt},
	})

	var arnErr *kmstypes.InvalidArnException
	if !errors.As(err, &arnErr) {
		t.Fatalf("err = %v, want InvalidArnException", err)
	}

	for _, p := range []string{
		"arn:aws:iam::111122223333:group/G",
		"arn:aws:iam::111122223333:policy/P",
		"arn:aws:iam:us-east-1:111122223333:role/R",
	} {
		_, err = c.CreateGrant(ctx, &awskms.CreateGrantInput{
			KeyId: key.KeyMetadata.KeyId, GranteePrincipal: aws.String(p),
			Operations: []kmstypes.GrantOperation{kmstypes.GrantOperationEncrypt},
		})
		if !errors.As(err, &arnErr) {
			t.Fatalf("%s: err = %v, want InvalidArnException", p, err)
		}
	}

	if _, err := c.CreateGrant(ctx, &awskms.CreateGrantInput{
		KeyId: key.KeyMetadata.KeyId, GranteePrincipal: aws.String("arn:aws:iam::111122223333:role/app"),
		Operations: []kmstypes.GrantOperation{kmstypes.GrantOperationEncrypt},
	}); err != nil {
		t.Fatalf("valid role ARN: %v", err)
	}
}
