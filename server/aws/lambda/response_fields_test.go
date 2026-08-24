package lambda_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// TestSDKInvokeExecutedVersion covers the X-Amz-Executed-Version response header
// a synchronous invoke always returns; the SDK reads it into
// InvokeOutput.ExecutedVersion. An unqualified invoke reports $LATEST.
func TestSDKInvokeExecutedVersion(t *testing.T) {
	client, cloud := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("ver-echo"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	cloud.Lambda.RegisterHandler("ver-echo", func(_ context.Context, payload []byte) ([]byte, error) {
		return payload, nil
	})

	resp, err := client.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName: aws.String("ver-echo"),
		Payload:      []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if aws.ToString(resp.ExecutedVersion) != "$LATEST" {
		t.Fatalf("ExecutedVersion = %q, want $LATEST", aws.ToString(resp.ExecutedVersion))
	}
}

// TestSDKGetFunctionConcurrency covers GetFunction surfacing the top-level
// Concurrency object once reserved concurrency has been set: before the fix the
// GetFunction response never carried Concurrency even after PutFunctionConcurrency.
func TestSDKGetFunctionConcurrency(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "reserved")

	// Before PutFunctionConcurrency, GetFunction omits the Concurrency object.
	pre, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("reserved")})
	if err != nil {
		t.Fatalf("GetFunction (pre): %v", err)
	}
	if pre.Concurrency != nil {
		t.Fatalf("Concurrency = %+v before Put, want nil", pre.Concurrency)
	}

	if _, err := client.PutFunctionConcurrency(ctx, &awslambda.PutFunctionConcurrencyInput{
		FunctionName:                 aws.String("reserved"),
		ReservedConcurrentExecutions: aws.Int32(7),
	}); err != nil {
		t.Fatalf("PutFunctionConcurrency: %v", err)
	}

	got, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("reserved")})
	if err != nil {
		t.Fatalf("GetFunction (post): %v", err)
	}
	if got.Concurrency == nil || got.Concurrency.ReservedConcurrentExecutions == nil {
		t.Fatalf("Concurrency = %+v, want ReservedConcurrentExecutions set", got.Concurrency)
	}
	if *got.Concurrency.ReservedConcurrentExecutions != 7 {
		t.Fatalf("ReservedConcurrentExecutions = %d, want 7", *got.Concurrency.ReservedConcurrentExecutions)
	}
}
