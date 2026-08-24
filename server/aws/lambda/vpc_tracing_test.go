package lambda_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// TestSDKLambdaVpcDeadLetterTracing verifies that VpcConfig, DeadLetterConfig
// and an explicit TracingConfig supplied at CreateFunction are stored and echoed
// back by GetFunctionConfiguration.
func TestSDKLambdaVpcDeadLetterTracing(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("netfn"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
		VpcConfig: &lambdatypes.VpcConfig{
			SubnetIds:        []string{"subnet-1", "subnet-2"},
			SecurityGroupIds: []string{"sg-1"},
		},
		DeadLetterConfig: &lambdatypes.DeadLetterConfig{
			TargetArn: aws.String("arn:aws:sqs:us-east-1:000000000000:dlq"),
		},
		TracingConfig: &lambdatypes.TracingConfig{Mode: lambdatypes.TracingModeActive},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	out, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("netfn"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration: %v", err)
	}

	if out.VpcConfig == nil {
		t.Fatal("VpcConfig nil, want echoed config")
	}

	if len(out.VpcConfig.SubnetIds) != 2 || out.VpcConfig.SubnetIds[0] != "subnet-1" {
		t.Fatalf("SubnetIds = %v, want [subnet-1 subnet-2]", out.VpcConfig.SubnetIds)
	}

	if len(out.VpcConfig.SecurityGroupIds) != 1 || out.VpcConfig.SecurityGroupIds[0] != "sg-1" {
		t.Fatalf("SecurityGroupIds = %v, want [sg-1]", out.VpcConfig.SecurityGroupIds)
	}

	if out.DeadLetterConfig == nil ||
		aws.ToString(out.DeadLetterConfig.TargetArn) != "arn:aws:sqs:us-east-1:000000000000:dlq" {
		t.Fatalf("DeadLetterConfig = %+v, want dlq target", out.DeadLetterConfig)
	}

	if out.TracingConfig == nil || out.TracingConfig.Mode != lambdatypes.TracingModeActive {
		t.Fatalf("TracingConfig = %+v, want Active", out.TracingConfig)
	}
}

// TestSDKLambdaTracingDefaultsPassThrough verifies that a function created with
// no TracingConfig reports the AWS default {Mode: PassThrough}.
func TestSDKLambdaTracingDefaultsPassThrough(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("tracefn"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	out, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("tracefn"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration: %v", err)
	}

	if out.TracingConfig == nil || out.TracingConfig.Mode != lambdatypes.TracingModePassThrough {
		t.Fatalf("TracingConfig = %+v, want default PassThrough", out.TracingConfig)
	}

	if out.VpcConfig != nil {
		t.Fatalf("VpcConfig = %+v, want nil when unset", out.VpcConfig)
	}
}

// TestSDKLambdaUpdateVpcTracing verifies UpdateFunctionConfiguration stores and
// echoes an updated VpcConfig/TracingConfig.
func TestSDKLambdaUpdateVpcTracing(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	createBasicFunction(t, client, "updnet")

	if _, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName:  aws.String("updnet"),
		TracingConfig: &lambdatypes.TracingConfig{Mode: lambdatypes.TracingModeActive},
		VpcConfig: &lambdatypes.VpcConfig{
			SubnetIds:        []string{"subnet-x"},
			SecurityGroupIds: []string{"sg-x"},
		},
	}); err != nil {
		t.Fatalf("UpdateFunctionConfiguration: %v", err)
	}

	out, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("updnet"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration: %v", err)
	}

	if out.TracingConfig == nil || out.TracingConfig.Mode != lambdatypes.TracingModeActive {
		t.Fatalf("TracingConfig = %+v, want Active", out.TracingConfig)
	}

	if out.VpcConfig == nil || len(out.VpcConfig.SubnetIds) != 1 || out.VpcConfig.SubnetIds[0] != "subnet-x" {
		t.Fatalf("VpcConfig = %+v, want subnet-x", out.VpcConfig)
	}
}

// TestSDKLambdaUpdateTracingKeepsVpc verifies the AWS partial-update contract for
// the optional config: an UpdateFunctionConfiguration that changes only
// TracingConfig leaves a previously configured VpcConfig unchanged (omitted
// fields are not cleared).
func TestSDKLambdaUpdateTracingKeepsVpc(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("keepvpc"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
		VpcConfig: &lambdatypes.VpcConfig{
			SubnetIds:        []string{"subnet-keep"},
			SecurityGroupIds: []string{"sg-keep"},
		},
	}); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if _, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName:  aws.String("keepvpc"),
		TracingConfig: &lambdatypes.TracingConfig{Mode: lambdatypes.TracingModeActive},
	}); err != nil {
		t.Fatalf("UpdateFunctionConfiguration: %v", err)
	}

	out, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("keepvpc"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration: %v", err)
	}

	if out.TracingConfig == nil || out.TracingConfig.Mode != lambdatypes.TracingModeActive {
		t.Fatalf("TracingConfig = %+v, want Active", out.TracingConfig)
	}

	if out.VpcConfig == nil || len(out.VpcConfig.SubnetIds) != 1 || out.VpcConfig.SubnetIds[0] != "subnet-keep" {
		t.Fatalf("VpcConfig = %+v, want subnet-keep preserved across a tracing-only update", out.VpcConfig)
	}
}

// TestSDKLambdaListLayerVersionsDescending verifies ListLayerVersions returns
// versions newest-first.
func TestSDKLambdaListLayerVersionsDescending(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := client.PublishLayerVersion(ctx, &awslambda.PublishLayerVersionInput{
			LayerName: aws.String("multi"),
			Content:   &lambdatypes.LayerVersionContentInput{ZipFile: []byte{byte(i)}},
		}); err != nil {
			t.Fatalf("PublishLayerVersion #%d: %v", i, err)
		}
	}

	lv, err := client.ListLayerVersions(ctx, &awslambda.ListLayerVersionsInput{
		LayerName: aws.String("multi"),
	})
	if err != nil {
		t.Fatalf("ListLayerVersions: %v", err)
	}

	if len(lv.LayerVersions) != 3 {
		t.Fatalf("got %d versions, want 3", len(lv.LayerVersions))
	}

	want := []int64{3, 2, 1}
	for i, v := range lv.LayerVersions {
		if v.Version != want[i] {
			t.Fatalf("LayerVersions[%d].Version = %d, want %d (descending)", i, v.Version, want[i])
		}
	}
}
