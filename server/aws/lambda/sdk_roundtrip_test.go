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
	"github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
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

func TestSDKLambdaConcurrencyRoundtrip(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("busy"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	put, err := client.PutFunctionConcurrency(ctx, &awslambda.PutFunctionConcurrencyInput{
		FunctionName:                 aws.String("busy"),
		ReservedConcurrentExecutions: aws.Int32(5),
	})
	if err != nil {
		t.Fatalf("PutFunctionConcurrency: %v", err)
	}

	if put.ReservedConcurrentExecutions == nil || *put.ReservedConcurrentExecutions != 5 {
		t.Fatalf("Put ReservedConcurrentExecutions = %v, want 5", put.ReservedConcurrentExecutions)
	}

	got, err := client.GetFunctionConcurrency(ctx, &awslambda.GetFunctionConcurrencyInput{
		FunctionName: aws.String("busy"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConcurrency: %v", err)
	}

	if got.ReservedConcurrentExecutions == nil || *got.ReservedConcurrentExecutions != 5 {
		t.Fatalf("Get ReservedConcurrentExecutions = %v, want 5", got.ReservedConcurrentExecutions)
	}

	if _, err := client.DeleteFunctionConcurrency(ctx, &awslambda.DeleteFunctionConcurrencyInput{
		FunctionName: aws.String("busy"),
	}); err != nil {
		t.Fatalf("DeleteFunctionConcurrency: %v", err)
	}

	if _, err := client.GetFunctionConcurrency(ctx, &awslambda.GetFunctionConcurrencyInput{
		FunctionName: aws.String("busy"),
	}); err == nil {
		t.Fatal("GetFunctionConcurrency after delete returned nil error, want NotFound")
	}
}

func TestSDKLambdaLayersRoundtrip(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	pub, err := client.PublishLayerVersion(ctx, &awslambda.PublishLayerVersionInput{
		LayerName:          aws.String("deps"),
		Description:        aws.String("shared deps"),
		CompatibleRuntimes: []lambdatypes.Runtime{lambdatypes.RuntimePython39},
		Content: &lambdatypes.LayerVersionContentInput{
			ZipFile: []byte("layer-zip"),
		},
	})
	if err != nil {
		t.Fatalf("PublishLayerVersion: %v", err)
	}

	if pub.Version != 1 {
		t.Fatalf("Version = %d, want 1", pub.Version)
	}

	if aws.ToString(pub.LayerVersionArn) == "" {
		t.Fatal("LayerVersionArn empty")
	}

	if pub.Content == nil || pub.Content.CodeSize == 0 {
		t.Fatalf("Content = %+v, want non-zero CodeSize", pub.Content)
	}

	got, err := client.GetLayerVersion(ctx, &awslambda.GetLayerVersionInput{
		LayerName:     aws.String("deps"),
		VersionNumber: aws.Int64(1),
	})
	if err != nil {
		t.Fatalf("GetLayerVersion: %v", err)
	}

	if got.Version != 1 || aws.ToString(got.Description) != "shared deps" {
		t.Fatalf("GetLayerVersion = %+v, want version 1 / shared deps", got)
	}

	lv, err := client.ListLayerVersions(ctx, &awslambda.ListLayerVersionsInput{
		LayerName: aws.String("deps"),
	})
	if err != nil {
		t.Fatalf("ListLayerVersions: %v", err)
	}

	if len(lv.LayerVersions) != 1 || lv.LayerVersions[0].Version != 1 {
		t.Fatalf("LayerVersions = %+v, want one version 1", lv.LayerVersions)
	}

	ll, err := client.ListLayers(ctx, &awslambda.ListLayersInput{})
	if err != nil {
		t.Fatalf("ListLayers: %v", err)
	}

	if len(ll.Layers) != 1 || aws.ToString(ll.Layers[0].LayerName) != "deps" {
		t.Fatalf("Layers = %+v, want one named deps", ll.Layers)
	}

	if ll.Layers[0].LatestMatchingVersion == nil || ll.Layers[0].LatestMatchingVersion.Version != 1 {
		t.Fatalf("LatestMatchingVersion = %+v, want version 1", ll.Layers[0].LatestMatchingVersion)
	}

	if _, err := client.DeleteLayerVersion(ctx, &awslambda.DeleteLayerVersionInput{
		LayerName:     aws.String("deps"),
		VersionNumber: aws.Int64(1),
	}); err != nil {
		t.Fatalf("DeleteLayerVersion: %v", err)
	}

	if _, err := client.GetLayerVersion(ctx, &awslambda.GetLayerVersionInput{
		LayerName:     aws.String("deps"),
		VersionNumber: aws.Int64(1),
	}); err == nil {
		t.Fatal("GetLayerVersion after delete returned nil error, want NotFound")
	}
}
