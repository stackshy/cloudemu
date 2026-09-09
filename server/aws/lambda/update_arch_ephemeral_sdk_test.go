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

// TestSDKUpdateFunctionConfigurationEphemeralStorage covers EphemeralStorage
// updates travelling through UpdateFunctionConfiguration (the API that carries
// it) and persisting on a subsequent GetFunctionConfiguration. Before the fix
// the field was dropped from the request, so GetFunction kept reporting the
// create-time size and Terraform saw perpetual drift on ephemeral_storage.
func TestSDKUpdateFunctionConfigurationEphemeralStorage(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "esupd")

	if _, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName:     aws.String("esupd"),
		EphemeralStorage: &lambdatypes.EphemeralStorage{Size: aws.Int32(3072)},
	}); err != nil {
		t.Fatalf("UpdateFunctionConfiguration(EphemeralStorage): %v", err)
	}

	got, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("esupd"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration: %v", err)
	}
	if es := got.EphemeralStorage; es == nil || aws.ToInt32(es.Size) != 3072 {
		t.Fatalf("EphemeralStorage = %+v, want Size 3072", got.EphemeralStorage)
	}

	// An out-of-range size on update is rejected, like create.
	_, err = client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName:     aws.String("esupd"),
		EphemeralStorage: &lambdatypes.EphemeralStorage{Size: aws.Int32(400)},
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("UpdateFunctionConfiguration(size 400) err = %v, want InvalidParameterValueException", err)
	}
}

// TestSDKUpdateFunctionCodeArchitectures covers Architectures updates travelling
// through UpdateFunctionCode (the API that carries them — the code must match the
// target instruction set) and persisting on a subsequent GetFunctionConfiguration.
// Before the fix the field was dropped, so GetFunction kept reporting the
// create-time architecture and Terraform saw perpetual drift on architectures.
func TestSDKUpdateFunctionCodeArchitectures(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "archupd") // defaults to x86_64

	if _, err := client.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{
		FunctionName:  aws.String("archupd"),
		ZipFile:       []byte("new-code"),
		Architectures: []lambdatypes.Architecture{lambdatypes.ArchitectureArm64},
	}); err != nil {
		t.Fatalf("UpdateFunctionCode(Architectures): %v", err)
	}

	got, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("archupd"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration: %v", err)
	}
	if archs := got.Architectures; len(archs) != 1 || archs[0] != lambdatypes.ArchitectureArm64 {
		t.Fatalf("Architectures = %v, want [arm64]", got.Architectures)
	}

	// An unknown architecture on update is rejected.
	_, err = client.UpdateFunctionCode(ctx, &awslambda.UpdateFunctionCodeInput{
		FunctionName:  aws.String("archupd"),
		ZipFile:       []byte("z"),
		Architectures: []lambdatypes.Architecture{"sparc"},
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("UpdateFunctionCode(arch sparc) err = %v, want InvalidParameterValueException", err)
	}
}
