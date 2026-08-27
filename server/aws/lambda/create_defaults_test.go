package lambda_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/smithy-go"
)

// TestSDKCreateFunctionDefaultMemoryAndTimeout covers CreateFunction applying
// AWS's documented create-time defaults when the client omits them: MemorySize
// defaults to 128 MB and Timeout to 3 seconds. Before the fix both came back 0,
// causing perpetual Terraform/CDK drift.
func TestSDKCreateFunctionDefaultMemoryAndTimeout(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	out, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("defaults"),
		Runtime:      lambdatypes.RuntimePython39,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("index.handler"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if out.MemorySize == nil || *out.MemorySize != 128 {
		t.Fatalf("create MemorySize = %v, want 128", out.MemorySize)
	}
	if out.Timeout == nil || *out.Timeout != 3 {
		t.Fatalf("create Timeout = %v, want 3", out.Timeout)
	}

	got, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("defaults"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration: %v", err)
	}
	if got.MemorySize == nil || *got.MemorySize != 128 {
		t.Fatalf("get MemorySize = %v, want 128", got.MemorySize)
	}
	if got.Timeout == nil || *got.Timeout != 3 {
		t.Fatalf("get Timeout = %v, want 3", got.Timeout)
	}
}

// TestSDKCreateFunctionZipRequiresRuntimeAndHandler covers AWS rejecting a .zip
// package CreateFunction that omits Runtime or Handler with
// InvalidParameterValueException. Before the fix cloudemu accepted it (201) and
// stored empty runtime/handler.
func TestSDKCreateFunctionZipRequiresRuntimeAndHandler(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	cases := []struct {
		name string
		in   *awslambda.CreateFunctionInput
	}{
		{
			name: "no-runtime",
			in: &awslambda.CreateFunctionInput{
				FunctionName: aws.String("noruntime"),
				Role:         aws.String("arn:aws:iam::000000000000:role/test"),
				Handler:      aws.String("index.handler"),
				Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
			},
		},
		{
			name: "no-handler",
			in: &awslambda.CreateFunctionInput{
				FunctionName: aws.String("nohandler"),
				Runtime:      lambdatypes.RuntimePython39,
				Role:         aws.String("arn:aws:iam::000000000000:role/test"),
				Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.CreateFunction(ctx, tc.in)
			if err == nil {
				t.Fatalf("CreateFunction with a .zip package and missing runtime/handler succeeded, want InvalidParameterValueException")
			}

			var apiErr smithy.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error %v is not an API error", err)
			}
			if apiErr.ErrorCode() != "InvalidParameterValueException" {
				t.Fatalf("ErrorCode = %q, want InvalidParameterValueException", apiErr.ErrorCode())
			}
		})
	}
}
