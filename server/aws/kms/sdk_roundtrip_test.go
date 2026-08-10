package kms_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newKMSClient(t *testing.T) *awskms.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{KMS: cloud.KMS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awskms.NewFromConfig(cfg, func(o *awskms.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	created, err := c.CreateKey(ctx, &awskms.CreateKeyInput{
		Description: aws.String("app key"),
		Tags:        []kmstypes.Tag{{TagKey: aws.String("env"), TagValue: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	keyID := aws.ToString(created.KeyMetadata.KeyId)
	if keyID == "" || aws.ToString(created.KeyMetadata.Arn) == "" {
		t.Fatalf("CreateKey returned empty id/arn: %+v", created.KeyMetadata)
	}

	if created.KeyMetadata.KeyState != kmstypes.KeyStateEnabled {
		t.Fatalf("new key state = %s, want Enabled", created.KeyMetadata.KeyState)
	}

	desc, err := c.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: aws.String(keyID)})
	if err != nil {
		t.Fatalf("DescribeKey: %v", err)
	}

	if aws.ToString(desc.KeyMetadata.KeyId) != keyID {
		t.Fatalf("DescribeKey id = %s, want %s", aws.ToString(desc.KeyMetadata.KeyId), keyID)
	}

	listed, err := c.ListKeys(ctx, &awskms.ListKeysInput{})
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}

	if len(listed.Keys) != 1 {
		t.Fatalf("ListKeys = %d, want 1", len(listed.Keys))
	}

	if _, err := c.DisableKey(ctx, &awskms.DisableKeyInput{KeyId: aws.String(keyID)}); err != nil {
		t.Fatalf("DisableKey: %v", err)
	}

	desc, _ = c.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: aws.String(keyID)})
	if desc.KeyMetadata.Enabled {
		t.Fatal("key should be disabled")
	}
}

func TestSDKAliases(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	created, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	keyID := aws.ToString(created.KeyMetadata.KeyId)

	if _, err := c.CreateAlias(ctx, &awskms.CreateAliasInput{
		AliasName: aws.String("alias/app"), TargetKeyId: aws.String(keyID),
	}); err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}

	// DescribeKey by alias resolves to the same key.
	desc, err := c.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: aws.String("alias/app")})
	if err != nil {
		t.Fatalf("DescribeKey by alias: %v", err)
	}

	if aws.ToString(desc.KeyMetadata.KeyId) != keyID {
		t.Fatalf("alias resolved to %s, want %s", aws.ToString(desc.KeyMetadata.KeyId), keyID)
	}

	aliases, err := c.ListAliases(ctx, &awskms.ListAliasesInput{})
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}

	var found bool
	for _, a := range aliases.Aliases {
		if aws.ToString(a.AliasName) == "alias/app" {
			found = true
		}
	}

	if !found {
		t.Fatal("alias/app not in ListAliases")
	}
}

func TestSDKScheduleDeletionAndTags(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	created, _ := c.CreateKey(ctx, &awskms.CreateKeyInput{})
	keyID := aws.ToString(created.KeyMetadata.KeyId)

	sched, err := c.ScheduleKeyDeletion(ctx, &awskms.ScheduleKeyDeletionInput{
		KeyId: aws.String(keyID), PendingWindowInDays: aws.Int32(7),
	})
	if err != nil {
		t.Fatalf("ScheduleKeyDeletion: %v", err)
	}

	if sched.DeletionDate == nil {
		t.Fatal("ScheduleKeyDeletion returned no deletion date")
	}

	if _, err := c.CancelKeyDeletion(ctx, &awskms.CancelKeyDeletionInput{KeyId: aws.String(keyID)}); err != nil {
		t.Fatalf("CancelKeyDeletion: %v", err)
	}

	if _, err := c.TagResource(ctx, &awskms.TagResourceInput{
		KeyId: aws.String(keyID),
		Tags:  []kmstypes.Tag{{TagKey: aws.String("team"), TagValue: aws.String("platform")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := c.ListResourceTags(ctx, &awskms.ListResourceTagsInput{KeyId: aws.String(keyID)})
	if err != nil {
		t.Fatalf("ListResourceTags: %v", err)
	}

	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].TagKey) != "team" {
		t.Fatalf("unexpected tags: %+v", tags.Tags)
	}
}

func TestSDKDescribeMissingKeyReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	c := newKMSClient(t)

	_, err := c.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: aws.String("does-not-exist")})
	if err == nil {
		t.Fatal("expected error for missing key")
	}

	var nf *kmstypes.NotFoundException
	if !errors.As(err, &nf) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want NotFoundException, got API error %q", apiErr.ErrorCode())
		}

		t.Fatalf("want NotFoundException, got %v", err)
	}
}
