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

// assertInvalidParameterValue asserts err is exactly the InvalidParameterValueException
// (HTTP 400) code real AWS Lambda returns for a bad Runtime/MemorySize/Timeout.
func assertInvalidParameterValue(t *testing.T, err error, context string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("%s: err = %v, want InvalidParameterValueException", context, err)
	}
}

// TestSDKCreateFunctionRuntimeValidation covers CreateFunction's Runtime enum
// check: a current AWS runtime (including nodejs24.x, released after the
// initial validRuntimes snapshot was written — a regression guard against
// over-rejection) is accepted, and a garbage value is rejected with
// InvalidParameterValueException.
func TestSDKCreateFunctionRuntimeValidation(t *testing.T) {
	tests := []struct {
		name      string
		runtime   lambdatypes.Runtime
		expectErr bool
	}{
		{name: "nodejs22.x accepted", runtime: lambdatypes.RuntimeNodejs22x},
		{name: "nodejs24.x accepted", runtime: lambdatypes.RuntimeNodejs24x},
		{name: "python3.13 accepted", runtime: lambdatypes.RuntimePython313},
		{name: "unlisted well-formed nodejs99.x accepted", runtime: "nodejs99.x"},
		{name: "garbage rejected", runtime: "not-a-runtime", expectErr: true},
		{name: "single char garbage rejected", runtime: "x", expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newSDKClient(t)
			ctx := context.Background()
			fnName := "rt-" + string(tc.runtime)

			_, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
				FunctionName: aws.String(fnName),
				Runtime:      tc.runtime,
				Role:         aws.String("arn:aws:iam::000000000000:role/test"),
				Handler:      aws.String("main"),
				Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
			})

			if tc.expectErr {
				assertInvalidParameterValue(t, err, "CreateFunction(Runtime="+string(tc.runtime)+")")

				if _, getErr := client.GetFunction(ctx, &awslambda.GetFunctionInput{
					FunctionName: aws.String(fnName),
				}); getErr == nil {
					t.Fatal("GetFunction after rejected create returned nil error, want NotFound")
				}

				return
			}

			if err != nil {
				t.Fatalf("CreateFunction(Runtime=%s): %v", tc.runtime, err)
			}
		})
	}
}

// TestSDKUpdateFunctionConfigurationRuntimeValidation mirrors the
// CreateFunction coverage above for UpdateFunctionConfiguration, and also
// covers omitting Runtime on an update: it must succeed and keep the
// function's existing Runtime rather than being treated as an invalid value.
func TestSDKUpdateFunctionConfigurationRuntimeValidation(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "rt-update")

	t.Run("current runtime accepted", func(t *testing.T) {
		if _, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
			FunctionName: aws.String("rt-update"),
			Runtime:      lambdatypes.RuntimeNodejs24x,
		}); err != nil {
			t.Fatalf("UpdateFunctionConfiguration(Runtime=nodejs24.x): %v", err)
		}

		got, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
			FunctionName: aws.String("rt-update"),
		})
		if err != nil {
			t.Fatalf("GetFunctionConfiguration: %v", err)
		}

		if got.Runtime != lambdatypes.RuntimeNodejs24x {
			t.Fatalf("Runtime = %s, want nodejs24.x", got.Runtime)
		}
	})

	t.Run("garbage runtime rejected, prior value kept", func(t *testing.T) {
		_, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
			FunctionName: aws.String("rt-update"),
			Runtime:      "not-a-runtime",
		})
		assertInvalidParameterValue(t, err, "UpdateFunctionConfiguration(Runtime=not-a-runtime)")

		got, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
			FunctionName: aws.String("rt-update"),
		})
		if err != nil {
			t.Fatalf("GetFunctionConfiguration: %v", err)
		}

		if got.Runtime != lambdatypes.RuntimeNodejs24x {
			t.Fatalf("Runtime after rejected update = %s, want it unchanged (nodejs24.x)", got.Runtime)
		}
	})

	t.Run("update omitting runtime succeeds and keeps it", func(t *testing.T) {
		if _, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
			FunctionName: aws.String("rt-update"),
			Description:  aws.String("unrelated change"),
		}); err != nil {
			t.Fatalf("UpdateFunctionConfiguration(no Runtime): %v", err)
		}

		got, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
			FunctionName: aws.String("rt-update"),
		})
		if err != nil {
			t.Fatalf("GetFunctionConfiguration: %v", err)
		}

		if got.Runtime != lambdatypes.RuntimeNodejs24x {
			t.Fatalf("Runtime after omitted-field update = %s, want it unchanged (nodejs24.x)", got.Runtime)
		}
	})
}
