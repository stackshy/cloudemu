package lambda_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// TestSDKInvokeEventReturns202 covers an asynchronous (InvocationType=Event)
// invoke returning HTTP 202 with an empty body. Before the fix cloudemu always
// wrote 200 and echoed the request payload back regardless of invocation type.
func TestSDKInvokeEventReturns202(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("async"),
		Runtime:      lambdatypes.RuntimePython39,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("index.handler"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	resp, err := client.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName:   aws.String("async"),
		InvocationType: lambdatypes.InvocationTypeEvent,
		Payload:        []byte(`{"k":1}`),
	})
	if err != nil {
		t.Fatalf("Invoke(Event): %v", err)
	}

	if resp.StatusCode != 202 {
		t.Fatalf("StatusCode = %d, want 202 for async Event invocation", resp.StatusCode)
	}
	if len(resp.Payload) != 0 {
		t.Fatalf("Payload = %q, want empty body for async Event invocation", resp.Payload)
	}
	if resp.FunctionError != nil && *resp.FunctionError != "" {
		t.Fatalf("FunctionError = %q, want empty for async Event invocation", *resp.FunctionError)
	}
}

// TestSDKInvokeRequestResponseReturns200 guards that a synchronous invoke still
// returns 200 with the payload after the async 202 branch was added.
func TestSDKInvokeRequestResponseReturns200(t *testing.T) {
	client, cloud := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("sync"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	cloud.Lambda.RegisterHandler("sync", func(_ context.Context, payload []byte) ([]byte, error) {
		return payload, nil
	})

	resp, err := client.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName:   aws.String("sync"),
		InvocationType: lambdatypes.InvocationTypeRequestResponse,
		Payload:        []byte(`"ok"`),
	})
	if err != nil {
		t.Fatalf("Invoke(RequestResponse): %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode = %d, want 200 for RequestResponse", resp.StatusCode)
	}
	if string(resp.Payload) != `"ok"` {
		t.Fatalf("Payload = %q, want \"ok\"", resp.Payload)
	}
}

// TestSDKCreateFunctionLastUpdateStatus covers config responses carrying
// LastUpdateStatus=Successful (the field FunctionUpdatedV2 waiters poll). Before
// the fix it was empty on create/update, hanging every SAM/CDK/Terraform deploy
// that waits for the update to settle.
func TestSDKCreateFunctionLastUpdateStatus(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	created, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("status"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}
	if created.LastUpdateStatus != lambdatypes.LastUpdateStatusSuccessful {
		t.Fatalf("create LastUpdateStatus = %q, want Successful", created.LastUpdateStatus)
	}

	updated, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("status"),
		Description:  aws.String("changed"),
	})
	if err != nil {
		t.Fatalf("UpdateFunctionConfiguration: %v", err)
	}
	if updated.LastUpdateStatus != lambdatypes.LastUpdateStatusSuccessful {
		t.Fatalf("update LastUpdateStatus = %q, want Successful", updated.LastUpdateStatus)
	}

	got, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("status"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration: %v", err)
	}
	if got.LastUpdateStatus != lambdatypes.LastUpdateStatusSuccessful {
		t.Fatalf("get LastUpdateStatus = %q, want Successful", got.LastUpdateStatus)
	}
}
