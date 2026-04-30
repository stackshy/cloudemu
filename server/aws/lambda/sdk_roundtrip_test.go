package lambda_test

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/stackshy/cloudemu"
	awsprovider "github.com/stackshy/cloudemu/providers/aws"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

func newSDKClient(t *testing.T) (*awslambda.Client, *awsprovider.Provider) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Lambda: cloud.Lambda})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	return client, cloud
}

func TestSDKLambdaCreateGetDelete(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("hello"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		MemorySize:   aws.Int32(128),
		Timeout:      aws.Int32(30),
		Code: &lambdatypes.FunctionCode{
			ZipFile: []byte("fake-zip"),
		},
	})
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	got, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{
		FunctionName: aws.String("hello"),
	})
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}

	if got.Configuration == nil || aws.ToString(got.Configuration.FunctionName) != "hello" {
		t.Fatalf("got %+v, want FunctionName=hello", got.Configuration)
	}

	if aws.ToString(got.Configuration.Handler) != "main" {
		t.Fatalf("Handler = %q, want main", aws.ToString(got.Configuration.Handler))
	}

	if got.Configuration.MemorySize == nil || *got.Configuration.MemorySize != 128 {
		t.Fatalf("MemorySize = %v, want 128", got.Configuration.MemorySize)
	}

	list, err := client.ListFunctions(ctx, &awslambda.ListFunctionsInput{})
	if err != nil {
		t.Fatalf("ListFunctions: %v", err)
	}

	if len(list.Functions) != 1 || aws.ToString(list.Functions[0].FunctionName) != "hello" {
		t.Fatalf("Functions = %+v, want one named hello", list.Functions)
	}

	if _, err := client.DeleteFunction(ctx, &awslambda.DeleteFunctionInput{
		FunctionName: aws.String("hello"),
	}); err != nil {
		t.Fatalf("DeleteFunction: %v", err)
	}

	if _, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{
		FunctionName: aws.String("hello"),
	}); err == nil {
		t.Fatal("GetFunction after delete returned nil error, want NotFound")
	}
}

func TestSDKLambdaInvoke(t *testing.T) {
	client, cloud := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("echo"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	cloud.Lambda.RegisterHandler("echo", func(_ context.Context, payload []byte) ([]byte, error) {
		out := append([]byte(`{"echoed":`), payload...)
		out = append(out, '}')

		return out, nil
	})

	resp, err := client.Invoke(ctx, &awslambda.InvokeInput{
		FunctionName: aws.String("echo"),
		Payload:      []byte(`"hi"`),
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	if got := string(resp.Payload); got != `{"echoed":"hi"}` {
		t.Fatalf("Payload = %q, want {\"echoed\":\"hi\"}", got)
	}

	if resp.FunctionError != nil && *resp.FunctionError != "" {
		t.Fatalf("FunctionError = %q, want empty", *resp.FunctionError)
	}

	// Read+exhaust to simulate a real client clean-up — guards against any
	// surprises if Invoke ever returns a streaming body.
	_, _ = io.ReadAll(bytes.NewReader(resp.Payload))
}

func TestSDKLambdaInvokeOnMissingFunction(t *testing.T) {
	client, _ := newSDKClient(t)

	_, err := client.Invoke(context.Background(), &awslambda.InvokeInput{
		FunctionName: aws.String("nope"),
		Payload:      []byte(`{}`),
	})
	if err == nil {
		t.Fatal("Invoke on missing function returned nil error, want NotFound")
	}
}
