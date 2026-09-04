package ssm_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// newSSMAndKMSClients wires SSM and KMS from the same provider instance, so
// SSM's SecureString values are sealed through real KMS envelope encryption
// (matching how cmd/cloudemu wires them) — needed to exercise a KMS key
// becoming unusable underneath an already-created SecureString parameter.
func newSSMAndKMSClients(t *testing.T) (*awsssm.Client, *awskms.Client) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{SSM: cloud.SSM, KMS: cloud.KMS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	ssmClient := awsssm.NewFromConfig(cfg, func(o *awsssm.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
	kmsClient := awskms.NewFromConfig(cfg, func(o *awskms.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	return ssmClient, kmsClient
}

func apiErrorCode(t *testing.T, err error) string {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an API error: %v", err)
	}

	return apiErr.ErrorCode()
}

// TestSDKGetParameterDisabledKMSKeyIsInvalidKeyID is a real-user e2e
// regression: decrypting a SecureString whose KMS key has been disabled must
// surface the distinct InvalidKeyId client error (400), not a generic 500
// InternalServerError leaking the KMS failure message.
func TestSDKGetParameterDisabledKMSKeyIsInvalidKeyID(t *testing.T) {
	ssmClient, kmsClient := newSSMAndKMSClients(t)
	ctx := context.Background()

	key, err := kmsClient.CreateKey(ctx, &awskms.CreateKeyInput{})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	keyID := aws.ToString(key.KeyMetadata.KeyId)

	if _, err := ssmClient.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/secure/p"),
		Value: aws.String("s1"),
		Type:  ssmtypes.ParameterTypeSecureString,
		KeyId: aws.String(keyID),
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	if _, err := kmsClient.DisableKey(ctx, &awskms.DisableKeyInput{KeyId: aws.String(keyID)}); err != nil {
		t.Fatalf("DisableKey: %v", err)
	}

	_, err = ssmClient.GetParameter(ctx, &awsssm.GetParameterInput{
		Name:           aws.String("/secure/p"),
		WithDecryption: aws.Bool(true),
	})
	if err == nil {
		t.Fatal("GetParameter(WithDecryption) with disabled key: expected error, got nil")
	}

	if code := apiErrorCode(t, err); code != "InvalidKeyId" {
		t.Fatalf("error code = %q, want InvalidKeyId", code)
	}

	// PutParameter against a new SecureString with the disabled key fails the
	// same way.
	_, err = ssmClient.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/secure/new"),
		Value: aws.String("s2"),
		Type:  ssmtypes.ParameterTypeSecureString,
		KeyId: aws.String(keyID),
	})
	if err == nil {
		t.Fatal("PutParameter with disabled key: expected error, got nil")
	}

	if code := apiErrorCode(t, err); code != "InvalidKeyId" {
		t.Fatalf("error code = %q, want InvalidKeyId", code)
	}
}

// TestSDKListTagsForResourceStableOrder is a real-user e2e regression: real
// ListTagsForResource returns a stable tag order across repeated reads of
// unchanged state. Before the fix, ranging the tags map directly (unsorted)
// made the order vary from call to call.
func TestSDKListTagsForResourceStableOrder(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/app/multitag"),
		Value: aws.String("v"),
		Type:  ssmtypes.ParameterTypeString,
		Tags: []ssmtypes.Tag{
			{Key: aws.String("A"), Value: aws.String("1")},
			{Key: aws.String("B"), Value: aws.String("2")},
			{Key: aws.String("C"), Value: aws.String("3")},
			{Key: aws.String("D"), Value: aws.String("4")},
			{Key: aws.String("E"), Value: aws.String("5")},
		},
	}); err != nil {
		t.Fatalf("PutParameter: %v", err)
	}

	var first []string

	for i := 0; i < 5; i++ {
		out, err := client.ListTagsForResource(ctx, &awsssm.ListTagsForResourceInput{
			ResourceType: ssmtypes.ResourceTypeForTaggingParameter,
			ResourceId:   aws.String("/app/multitag"),
		})
		if err != nil {
			t.Fatalf("ListTagsForResource: %v", err)
		}

		keys := make([]string, 0, len(out.TagList))
		for _, tag := range out.TagList {
			keys = append(keys, aws.ToString(tag.Key))
		}

		if i == 0 {
			first = keys

			if !sort.StringsAreSorted(first) {
				t.Fatalf("TagList order = %v, want alphabetically sorted", first)
			}

			continue
		}

		if len(keys) != len(first) {
			t.Fatalf("call %d: TagList = %v, want %v", i, keys, first)
		}

		for j := range keys {
			if keys[j] != first[j] {
				t.Fatalf("call %d: TagList = %v, want stable order %v", i, keys, first)
			}
		}
	}
}
