package lambda_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// publishV1 creates function name and publishes version 1, returning the
// unqualified function ARN (the $LATEST ARN, without a :version suffix).
func publishV1(t *testing.T, client *awslambda.Client, name string) string {
	t.Helper()

	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String(name),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("v1-code")},
	}); err != nil {
		t.Fatalf("CreateFunction(%s): %v", name, err)
	}

	if _, err := client.PublishVersion(ctx, &awslambda.PublishVersionInput{
		FunctionName: aws.String(name),
	}); err != nil {
		t.Fatalf("PublishVersion(%s): %v", name, err)
	}

	out, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String(name),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration(%s): %v", name, err)
	}

	return aws.ToString(out.FunctionArn)
}

// TestSDKGetFunctionConfigurationQualifiedForms is a regression guard for the
// audit finding that the FunctionName path segment accepted only a bare name:
// Terraform's aws_lambda_function{publish=true} update-and-wait flow does a
// GetFunctionConfiguration on the fully-qualified ARN (arn:...:function:foo:2)
// after publishing, which returned ResourceNotFoundException. Real Lambda
// accepts a bare name, name:qualifier, an unqualified ARN, and a qualified ARN.
func TestSDKGetFunctionConfigurationQualifiedForms(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	arn := publishV1(t, client, "foo")

	cases := []struct {
		ref     string
		version string
	}{
		{"foo", "$LATEST"},
		{"foo:1", "1"},
		{arn, "$LATEST"},
		{arn + ":1", "1"},
	}

	for _, tc := range cases {
		out, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
			FunctionName: aws.String(tc.ref),
		})
		if err != nil {
			t.Fatalf("GetFunctionConfiguration(FunctionName=%q): %v", tc.ref, err)
		}

		if got := aws.ToString(out.Version); got != tc.version {
			t.Errorf("GetFunctionConfiguration(FunctionName=%q) Version = %q, want %q", tc.ref, got, tc.version)
		}
	}
}

// TestSDKGetFunctionConfigurationMissingQualifier verifies a FunctionName that
// embeds a non-existent version qualifier is a ResourceNotFoundException, not a
// spurious success.
func TestSDKGetFunctionConfigurationMissingQualifier(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	publishV1(t, client, "foo")

	_, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("foo:99"),
	})
	if errorCode(err) != "ResourceNotFoundException" {
		t.Fatalf("GetFunctionConfiguration(foo:99) error = %v, want ResourceNotFoundException", err)
	}
}

// TestSDKGetFunctionConfigurationQualifierConflict verifies that a qualifier
// embedded in the FunctionName that disagrees with the explicit Qualifier
// parameter is a ValidationException, while agreeing qualifiers resolve normally.
func TestSDKGetFunctionConfigurationQualifierConflict(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	publishV1(t, client, "foo")

	_, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("foo:1"),
		Qualifier:    aws.String("2"),
	})
	if errorCode(err) != "ValidationException" {
		t.Fatalf("GetFunctionConfiguration(foo:1, Qualifier=2) error = %v, want ValidationException", err)
	}

	out, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("foo:1"),
		Qualifier:    aws.String("1"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration(foo:1, Qualifier=1): %v", err)
	}

	if got := aws.ToString(out.Version); got != "1" {
		t.Errorf("GetFunctionConfiguration(foo:1, Qualifier=1) Version = %q, want %q", got, "1")
	}
}

// TestSDKQualifiedFormsAcrossOps spot-checks that Invoke, UpdateFunctionCode and
// DeleteFunction also accept the qualified/ARN FunctionName forms.
func TestSDKQualifiedFormsAcrossOps(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	arn := publishV1(t, client, "foo")

	// Invoke against the version-qualified ARN.
	if _, err := client.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName: aws.String(arn + ":1"),
		Payload:      []byte(`{}`),
	}); err != nil {
		t.Fatalf("Invoke(%s:1): %v", arn, err)
	}

	// UpdateFunctionCode against the unqualified ARN (operates on $LATEST).
	if _, err := client.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{
		FunctionName: aws.String(arn),
		ZipFile:      []byte("v2-code"),
	}); err != nil {
		t.Fatalf("UpdateFunctionCode(%s): %v", arn, err)
	}

	// DeleteFunction against the name:qualifier short form.
	if _, err := client.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{
		FunctionName: aws.String("foo:1"),
	}); err != nil {
		t.Fatalf("DeleteFunction(foo:1): %v", err)
	}
}
