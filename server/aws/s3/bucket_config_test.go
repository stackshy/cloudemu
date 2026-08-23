package s3_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// TestSDKBucketPolicyRoundTrip covers the config-persistence gap for
// PutBucketPolicy: the JSON document must read back byte-for-byte (previously a
// PUT was a no-op and GET returned NoSuchBucketPolicy), and DELETE clears it.
func TestSDKBucketPolicyRoundTrip(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "policy-bucket"
	mustCreateBucket(t, client, bucket)

	// Fresh bucket: no policy configured yet.
	if _, err := client.GetBucketPolicy(ctx, &awss3.GetBucketPolicyInput{Bucket: aws.String(bucket)}); err == nil {
		t.Fatal("GetBucketPolicy on a fresh bucket: expected NoSuchBucketPolicy, got nil")
	}

	policy := `{"Version":"2012-10-17","Statement":[{"Sid":"AllowAll","Effect":"Allow",` +
		`"Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::policy-bucket/*"}]}`

	if _, err := client.PutBucketPolicy(ctx, &awss3.PutBucketPolicyInput{
		Bucket: aws.String(bucket), Policy: aws.String(policy),
	}); err != nil {
		t.Fatalf("PutBucketPolicy: %v", err)
	}

	got, err := client.GetBucketPolicy(ctx, &awss3.GetBucketPolicyInput{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatalf("GetBucketPolicy: %v", err)
	}
	if aws.ToString(got.Policy) != policy {
		t.Fatalf("policy round-trip mismatch:\n got %q\nwant %q", aws.ToString(got.Policy), policy)
	}

	if _, err := client.DeleteBucketPolicy(ctx, &awss3.DeleteBucketPolicyInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("DeleteBucketPolicy: %v", err)
	}
	if _, err := client.GetBucketPolicy(ctx, &awss3.GetBucketPolicyInput{Bucket: aws.String(bucket)}); err == nil {
		t.Fatal("GetBucketPolicy after delete: expected NoSuchBucketPolicy, got nil")
	}
}

// TestSDKBucketCorsRoundTrip covers the config-persistence gap for
// PutBucketCors: the CORS rules must read back instead of NoSuchCORSConfiguration.
func TestSDKBucketCorsRoundTrip(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "cors-bucket"
	mustCreateBucket(t, client, bucket)

	if _, err := client.PutBucketCors(ctx, &awss3.PutBucketCorsInput{
		Bucket: aws.String(bucket),
		CORSConfiguration: &types.CORSConfiguration{
			CORSRules: []types.CORSRule{{
				AllowedMethods: []string{"GET", "PUT"},
				AllowedOrigins: []string{"https://example.com"},
				AllowedHeaders: []string{"*"},
				MaxAgeSeconds:  aws.Int32(3000),
			}},
		},
	}); err != nil {
		t.Fatalf("PutBucketCors: %v", err)
	}

	got, err := client.GetBucketCors(ctx, &awss3.GetBucketCorsInput{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatalf("GetBucketCors: %v", err)
	}
	if len(got.CORSRules) != 1 {
		t.Fatalf("CORSRules = %d, want 1", len(got.CORSRules))
	}
	if got.CORSRules[0].AllowedOrigins[0] != "https://example.com" ||
		aws.ToInt32(got.CORSRules[0].MaxAgeSeconds) != 3000 {
		t.Fatalf("CORS rule round-trip mismatch: %+v", got.CORSRules[0])
	}
}

// TestSDKBucketEncryptionRoundTrip covers the config-persistence gap for
// PutBucketEncryption: the SSE rule must read back instead of the
// ServerSideEncryptionConfigurationNotFoundError.
func TestSDKBucketEncryptionRoundTrip(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "enc-bucket"
	mustCreateBucket(t, client, bucket)

	if _, err := client.PutBucketEncryption(ctx, &awss3.PutBucketEncryptionInput{
		Bucket: aws.String(bucket),
		ServerSideEncryptionConfiguration: &types.ServerSideEncryptionConfiguration{
			Rules: []types.ServerSideEncryptionRule{{
				ApplyServerSideEncryptionByDefault: &types.ServerSideEncryptionByDefault{
					SSEAlgorithm: types.ServerSideEncryptionAes256,
				},
			}},
		},
	}); err != nil {
		t.Fatalf("PutBucketEncryption: %v", err)
	}

	got, err := client.GetBucketEncryption(ctx, &awss3.GetBucketEncryptionInput{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatalf("GetBucketEncryption: %v", err)
	}
	if got.ServerSideEncryptionConfiguration == nil || len(got.ServerSideEncryptionConfiguration.Rules) != 1 {
		t.Fatalf("encryption config = %+v, want 1 rule", got.ServerSideEncryptionConfiguration)
	}
	alg := got.ServerSideEncryptionConfiguration.Rules[0].ApplyServerSideEncryptionByDefault.SSEAlgorithm
	if alg != types.ServerSideEncryptionAes256 {
		t.Fatalf("SSEAlgorithm = %q, want AES256", alg)
	}
}

// TestSDKBucketLifecycleRoundTrip covers the config-persistence gap for
// PutBucketLifecycleConfiguration: the rule must read back instead of
// NoSuchLifecycleConfiguration.
func TestSDKBucketLifecycleRoundTrip(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "lc-bucket"
	mustCreateBucket(t, client, bucket)

	if _, err := client.PutBucketLifecycleConfiguration(ctx, &awss3.PutBucketLifecycleConfigurationInput{
		Bucket: aws.String(bucket),
		LifecycleConfiguration: &types.BucketLifecycleConfiguration{
			Rules: []types.LifecycleRule{{
				ID:         aws.String("expire-logs"),
				Status:     types.ExpirationStatusEnabled,
				Filter:     &types.LifecycleRuleFilter{Prefix: aws.String("logs/")},
				Expiration: &types.LifecycleExpiration{Days: aws.Int32(30)},
			}},
		},
	}); err != nil {
		t.Fatalf("PutBucketLifecycleConfiguration: %v", err)
	}

	got, err := client.GetBucketLifecycleConfiguration(ctx, &awss3.GetBucketLifecycleConfigurationInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		t.Fatalf("GetBucketLifecycleConfiguration: %v", err)
	}
	if len(got.Rules) != 1 || aws.ToString(got.Rules[0].ID) != "expire-logs" {
		t.Fatalf("lifecycle rules = %+v, want one 'expire-logs' rule", got.Rules)
	}
	if got.Rules[0].Expiration == nil || aws.ToInt32(got.Rules[0].Expiration.Days) != 30 {
		t.Fatalf("lifecycle expiration = %+v, want 30 days", got.Rules[0].Expiration)
	}
}

// TestSDKBucketWebsiteRoundTrip covers the config-persistence gap for
// PutBucketWebsite: the website config must read back instead of
// NoSuchWebsiteConfiguration.
func TestSDKBucketWebsiteRoundTrip(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	const bucket = "web-bucket"
	mustCreateBucket(t, client, bucket)

	if _, err := client.PutBucketWebsite(ctx, &awss3.PutBucketWebsiteInput{
		Bucket: aws.String(bucket),
		WebsiteConfiguration: &types.WebsiteConfiguration{
			IndexDocument: &types.IndexDocument{Suffix: aws.String("index.html")},
			ErrorDocument: &types.ErrorDocument{Key: aws.String("error.html")},
		},
	}); err != nil {
		t.Fatalf("PutBucketWebsite: %v", err)
	}

	got, err := client.GetBucketWebsite(ctx, &awss3.GetBucketWebsiteInput{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatalf("GetBucketWebsite: %v", err)
	}
	if got.IndexDocument == nil || aws.ToString(got.IndexDocument.Suffix) != "index.html" {
		t.Fatalf("website index = %+v, want index.html", got.IndexDocument)
	}
	if got.ErrorDocument == nil || aws.ToString(got.ErrorDocument.Key) != "error.html" {
		t.Fatalf("website error = %+v, want error.html", got.ErrorDocument)
	}

	if _, err := client.DeleteBucketWebsite(ctx, &awss3.DeleteBucketWebsiteInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("DeleteBucketWebsite: %v", err)
	}
	_, err = client.GetBucketWebsite(ctx, &awss3.GetBucketWebsiteInput{Bucket: aws.String(bucket)})
	if err == nil || !strings.Contains(err.Error(), "NoSuchWebsiteConfiguration") {
		t.Fatalf("GetBucketWebsite after delete: want NoSuchWebsiteConfiguration, got %v", err)
	}
}
