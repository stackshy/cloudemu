package ssm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
)

// describeOne returns the single ParameterMetadata for name via DescribeParameters.
func describeOne(t *testing.T, client *awsssm.Client, name string) ssmtypes.ParameterMetadata {
	t.Helper()

	out, err := client.DescribeParameters(context.Background(), &awsssm.DescribeParametersInput{})
	if err != nil {
		t.Fatalf("DescribeParameters: %v", err)
	}

	for _, md := range out.Parameters {
		if aws.ToString(md.Name) == name {
			return md
		}
	}

	t.Fatalf("parameter %q not found in DescribeParameters", name)

	return ssmtypes.ParameterMetadata{}
}

// TestSDKSecureStringKeyIDRoundTrip verifies that a SecureString's KeyId is
// surfaced on DescribeParameters ParameterMetadata (defaulting to alias/aws/ssm
// when omitted) — and NOT on the GetParameter Parameter shape, matching AWS.
func TestSDKSecureStringKeyIDRoundTrip(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/app/secure-default"),
		Value: aws.String("v"),
		Type:  ssmtypes.ParameterTypeSecureString,
	}); err != nil {
		t.Fatalf("PutParameter(default key): %v", err)
	}

	if got := aws.ToString(describeOne(t, client, "/app/secure-default").KeyId); got != "alias/aws/ssm" {
		t.Fatalf("default KeyId = %q, want alias/aws/ssm", got)
	}

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/app/secure-explicit"),
		Value: aws.String("v"),
		Type:  ssmtypes.ParameterTypeSecureString,
		KeyId: aws.String("alias/my-key"),
	}); err != nil {
		t.Fatalf("PutParameter(explicit key): %v", err)
	}

	if got := aws.ToString(describeOne(t, client, "/app/secure-explicit").KeyId); got != "alias/my-key" {
		t.Fatalf("explicit KeyId = %q, want alias/my-key", got)
	}

	// GetParameter's Parameter shape has no KeyId field, so nothing to assert
	// there; a String parameter carrying a KeyId is rejected instead.
	_, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:  aws.String("/app/plain"),
		Value: aws.String("v"),
		Type:  ssmtypes.ParameterTypeString,
		KeyId: aws.String("alias/my-key"),
	})
	if err == nil {
		t.Fatal("PutParameter(String with KeyId): expected error, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationException" {
		t.Fatalf("String+KeyId error = %v, want ValidationException", err)
	}
}

// TestSDKAllowedPatternRoundTrip verifies AllowedPattern enforcement on put and
// that it is reflected on DescribeParameters ParameterMetadata.
func TestSDKAllowedPatternRoundTrip(t *testing.T) {
	client := newSSMClient(t)
	ctx := context.Background()

	if _, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:           aws.String("/app/num"),
		Value:          aws.String("12345"),
		Type:           ssmtypes.ParameterTypeString,
		AllowedPattern: aws.String(`^\d+$`),
	}); err != nil {
		t.Fatalf("PutParameter(matching): %v", err)
	}

	if got := aws.ToString(describeOne(t, client, "/app/num").AllowedPattern); got != `^\d+$` {
		t.Fatalf("AllowedPattern = %q, want ^\\d+$", got)
	}

	// A value that violates the pattern is rejected as ValidationException.
	_, err := client.PutParameter(ctx, &awsssm.PutParameterInput{
		Name:           aws.String("/app/num"),
		Value:          aws.String("abc"),
		Type:           ssmtypes.ParameterTypeString,
		Overwrite:      aws.Bool(true),
		AllowedPattern: aws.String(`^\d+$`),
	})
	if err == nil {
		t.Fatal("PutParameter(mismatch on overwrite): expected error, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ValidationException" {
		t.Fatalf("mismatch error = %v, want ValidationException", err)
	}
}
