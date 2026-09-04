package lambda_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/smithy-go"
)

// wantUnreservedFloorMessage is the real Lambda PutFunctionConcurrency error
// text for a reservation that would push the account's shared unreserved
// pool below its 100-execution minimum.
const wantUnreservedFloorMessage = "Specified ReservedConcurrentExecutions for function decreases account's " +
	"UnreservedConcurrentExecution below its minimum value of [100]."

// TestSDKPutFunctionConcurrencyUnreservedFloor covers the account-wide
// unreserved-concurrency floor: reserving concurrency across two functions
// that leaves exactly the 100-execution minimum unreserved (out of the
// 1000-execution account limit) succeeds, but tipping one more execution over
// that boundary is rejected with InvalidParameterValueException and real
// AWS's exact message text.
func TestSDKPutFunctionConcurrencyUnreservedFloor(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	createBasicFunction(t, client, "floor-a")
	createBasicFunction(t, client, "floor-b")

	if _, err := client.PutFunctionConcurrency(ctx, &awslambda.PutFunctionConcurrencyInput{
		FunctionName:                 aws.String("floor-a"),
		ReservedConcurrentExecutions: aws.Int32(100),
	}); err != nil {
		t.Fatalf("PutFunctionConcurrency(floor-a, 100): %v", err)
	}

	// 100 (floor-a) + 800 (floor-b) leaves exactly 100 unreserved out of the
	// 1000-execution account limit — right at the boundary, must succeed.
	if _, err := client.PutFunctionConcurrency(ctx, &awslambda.PutFunctionConcurrencyInput{
		FunctionName:                 aws.String("floor-b"),
		ReservedConcurrentExecutions: aws.Int32(800),
	}); err != nil {
		t.Fatalf("PutFunctionConcurrency(floor-b, 800): %v", err)
	}

	// One more execution (801) would leave only 99 unreserved — rejected.
	_, err := client.PutFunctionConcurrency(ctx, &awslambda.PutFunctionConcurrencyInput{
		FunctionName:                 aws.String("floor-b"),
		ReservedConcurrentExecutions: aws.Int32(801),
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("PutFunctionConcurrency(floor-b, 801): err = %v, want InvalidParameterValueException", err)
	}

	if !strings.Contains(apiErr.ErrorMessage(), wantUnreservedFloorMessage) {
		t.Fatalf("error message = %q, want it to contain %q", apiErr.ErrorMessage(), wantUnreservedFloorMessage)
	}

	// The rejected Put must not have changed floor-b's prior reservation.
	got, err := client.GetFunctionConcurrency(ctx, &awslambda.GetFunctionConcurrencyInput{
		FunctionName: aws.String("floor-b"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConcurrency(floor-b): %v", err)
	}

	if aws.ToInt32(got.ReservedConcurrentExecutions) != 800 {
		t.Fatalf("floor-b ReservedConcurrentExecutions = %d, want unchanged 800", aws.ToInt32(got.ReservedConcurrentExecutions))
	}
}

// TestSDKPutFunctionConcurrencySingleFunctionOverAccountLimit covers a single
// function trying to reserve far more than the entire account's concurrency
// budget in one call.
func TestSDKPutFunctionConcurrencySingleFunctionOverAccountLimit(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "floor-huge")

	_, err := client.PutFunctionConcurrency(ctx, &awslambda.PutFunctionConcurrencyInput{
		FunctionName:                 aws.String("floor-huge"),
		ReservedConcurrentExecutions: aws.Int32(999999),
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("PutFunctionConcurrency(999999): err = %v, want InvalidParameterValueException", err)
	}
}
