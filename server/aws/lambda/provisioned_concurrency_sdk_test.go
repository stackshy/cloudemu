package lambda_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// publishVersion publishes a new version of name via the SDK and returns its
// version number.
func publishVersion(t *testing.T, client *awslambda.Client, name string) string {
	t.Helper()

	out, err := client.PublishVersion(context.Background(), &awslambda.PublishVersionInput{
		FunctionName: aws.String(name),
	})
	if err != nil {
		t.Fatalf("PublishVersion(%s): %v", name, err)
	}

	return *out.Version
}

// TestSDKProvisionedConcurrencyLifecycle is the real-user end-to-end flow: a
// published version gets a provisioned-concurrency config via
// PutProvisionedConcurrencyConfig, GetProvisionedConcurrencyConfig reports it
// READY with the requested amount, ListProvisionedConcurrencyConfigs includes
// it, and DeleteProvisionedConcurrencyConfig removes it (a subsequent Get
// 404s with ProvisionedConcurrencyConfigNotFoundException).
func TestSDKProvisionedConcurrencyLifecycle(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	createBasicFunction(t, client, "pc")
	version := publishVersion(t, client, "pc")

	putOut, err := client.PutProvisionedConcurrencyConfig(ctx, &awslambda.PutProvisionedConcurrencyConfigInput{
		FunctionName:                    aws.String("pc"),
		Qualifier:                       aws.String(version),
		ProvisionedConcurrentExecutions: aws.Int32(3),
	})
	if err != nil {
		t.Fatalf("PutProvisionedConcurrencyConfig: %v", err)
	}

	if putOut.Status != lambdatypes.ProvisionedConcurrencyStatusEnumReady {
		t.Fatalf("Put Status = %q, want READY", putOut.Status)
	}

	if putOut.RequestedProvisionedConcurrentExecutions == nil || *putOut.RequestedProvisionedConcurrentExecutions != 3 {
		t.Fatalf("Put Requested = %v, want 3", putOut.RequestedProvisionedConcurrentExecutions)
	}

	getOut, err := client.GetProvisionedConcurrencyConfig(ctx, &awslambda.GetProvisionedConcurrencyConfigInput{
		FunctionName: aws.String("pc"),
		Qualifier:    aws.String(version),
	})
	if err != nil {
		t.Fatalf("GetProvisionedConcurrencyConfig: %v", err)
	}

	if getOut.Status != lambdatypes.ProvisionedConcurrencyStatusEnumReady {
		t.Fatalf("Get Status = %q, want READY", getOut.Status)
	}

	if getOut.AllocatedProvisionedConcurrentExecutions == nil || *getOut.AllocatedProvisionedConcurrentExecutions != 3 {
		t.Fatalf("Get Allocated = %v, want 3", getOut.AllocatedProvisionedConcurrentExecutions)
	}

	if getOut.AvailableProvisionedConcurrentExecutions == nil || *getOut.AvailableProvisionedConcurrentExecutions != 3 {
		t.Fatalf("Get Available = %v, want 3", getOut.AvailableProvisionedConcurrentExecutions)
	}

	listOut, err := client.ListProvisionedConcurrencyConfigs(ctx, &awslambda.ListProvisionedConcurrencyConfigsInput{
		FunctionName: aws.String("pc"),
	})
	if err != nil {
		t.Fatalf("ListProvisionedConcurrencyConfigs: %v", err)
	}

	if len(listOut.ProvisionedConcurrencyConfigs) != 1 {
		t.Fatalf("List = %d configs, want 1", len(listOut.ProvisionedConcurrencyConfigs))
	}

	if listOut.ProvisionedConcurrencyConfigs[0].FunctionArn == nil {
		t.Fatal("List item missing FunctionArn")
	}

	if _, err = client.DeleteProvisionedConcurrencyConfig(ctx, &awslambda.DeleteProvisionedConcurrencyConfigInput{
		FunctionName: aws.String("pc"),
		Qualifier:    aws.String(version),
	}); err != nil {
		t.Fatalf("DeleteProvisionedConcurrencyConfig: %v", err)
	}

	_, err = client.GetProvisionedConcurrencyConfig(ctx, &awslambda.GetProvisionedConcurrencyConfigInput{
		FunctionName: aws.String("pc"),
		Qualifier:    aws.String(version),
	})
	if err == nil {
		t.Fatal("Get after Delete: want error")
	}

	var notFound *lambdatypes.ProvisionedConcurrencyConfigNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("Get after Delete error = %T %v, want *ProvisionedConcurrencyConfigNotFoundException", err, err)
	}
}

// TestSDKProvisionedConcurrencyRejectsLatest guards the real Lambda constraint
// that provisioned concurrency cannot attach to the mutable $LATEST alias/
// unqualified function.
func TestSDKProvisionedConcurrencyRejectsLatest(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	createBasicFunction(t, client, "latest-pc")

	_, err := client.PutProvisionedConcurrencyConfig(ctx, &awslambda.PutProvisionedConcurrencyConfigInput{
		FunctionName:                    aws.String("latest-pc"),
		Qualifier:                       aws.String("$LATEST"),
		ProvisionedConcurrentExecutions: aws.Int32(1),
	})
	if err == nil {
		t.Fatal("Put on $LATEST: want error")
	}

	var invalid *lambdatypes.InvalidParameterValueException
	if !errors.As(err, &invalid) {
		t.Fatalf("Put on $LATEST error = %T %v, want *InvalidParameterValueException", err, err)
	}
}

// TestSDKProvisionedConcurrencyBoundByReserved guards the real Lambda
// constraint that a requested provisioned-concurrency amount cannot exceed the
// function's reserved concurrency once one is configured.
func TestSDKProvisionedConcurrencyBoundByReserved(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	createBasicFunction(t, client, "bounded-pc")
	version := publishVersion(t, client, "bounded-pc")

	if _, err := client.PutFunctionConcurrency(ctx, &awslambda.PutFunctionConcurrencyInput{
		FunctionName:                 aws.String("bounded-pc"),
		ReservedConcurrentExecutions: aws.Int32(2),
	}); err != nil {
		t.Fatalf("PutFunctionConcurrency: %v", err)
	}

	_, err := client.PutProvisionedConcurrencyConfig(ctx, &awslambda.PutProvisionedConcurrencyConfigInput{
		FunctionName:                    aws.String("bounded-pc"),
		Qualifier:                       aws.String(version),
		ProvisionedConcurrentExecutions: aws.Int32(3),
	})
	if err == nil {
		t.Fatal("Put exceeding reserved concurrency: want error")
	}

	var invalid *lambdatypes.InvalidParameterValueException
	if !errors.As(err, &invalid) {
		t.Fatalf("Put exceeding reserved error = %T %v, want *InvalidParameterValueException", err, err)
	}
}

// TestSDKGetProvisionedConcurrencyConfigNotFound guards that a function with no
// provisioned-concurrency config on its qualifier reports
// ProvisionedConcurrencyConfigNotFoundException rather than the generic
// ResourceNotFoundException a missing function reports.
func TestSDKGetProvisionedConcurrencyConfigNotFound(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	createBasicFunction(t, client, "no-pc")
	version := publishVersion(t, client, "no-pc")

	_, err := client.GetProvisionedConcurrencyConfig(ctx, &awslambda.GetProvisionedConcurrencyConfigInput{
		FunctionName: aws.String("no-pc"),
		Qualifier:    aws.String(version),
	})
	if err == nil {
		t.Fatal("Get with no config: want error")
	}

	var notFound *lambdatypes.ProvisionedConcurrencyConfigNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("Get with no config error = %T %v, want *ProvisionedConcurrencyConfigNotFoundException", err, err)
	}
}
