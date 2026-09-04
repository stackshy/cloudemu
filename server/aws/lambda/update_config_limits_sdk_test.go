package lambda_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
)

// TestSDKUpdateFunctionConfigurationMemoryLimits covers UpdateFunctionConfiguration
// enforcing the same MemorySize range (128-10240 MB) CreateFunction does: this
// gap let a real user silently persist an out-of-range value via update
// instead of getting InvalidParameterValueException.
func TestSDKUpdateFunctionConfigurationMemoryLimits(t *testing.T) {
	tests := []struct {
		name      string
		memory    int32
		expectErr bool
	}{
		{name: "minimum boundary accepted", memory: 128},
		{name: "maximum boundary accepted", memory: 10240},
		{name: "one below minimum rejected", memory: 127, expectErr: true},
		{name: "one above maximum rejected", memory: 10241, expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newSDKClient(t)
			ctx := context.Background()
			createBasicFunction(t, client, "mem-upd")

			_, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
				FunctionName: aws.String("mem-upd"),
				MemorySize:   aws.Int32(tc.memory),
			})

			if tc.expectErr {
				assertInvalidParameterValue(t, err, "UpdateFunctionConfiguration(MemorySize)")

				got, getErr := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
					FunctionName: aws.String("mem-upd"),
				})
				if getErr != nil {
					t.Fatalf("GetFunctionConfiguration: %v", getErr)
				}

				if aws.ToInt32(got.MemorySize) == tc.memory {
					t.Fatalf("MemorySize after rejected update = %d, want it unchanged", tc.memory)
				}

				return
			}

			if err != nil {
				t.Fatalf("UpdateFunctionConfiguration(MemorySize=%d): %v", tc.memory, err)
			}
		})
	}
}

// TestSDKUpdateFunctionConfigurationTimeoutLimits mirrors the MemorySize
// coverage above for the Timeout range (1-900 seconds).
func TestSDKUpdateFunctionConfigurationTimeoutLimits(t *testing.T) {
	tests := []struct {
		name      string
		timeout   int32
		expectErr bool
	}{
		{name: "minimum boundary accepted", timeout: 1},
		{name: "maximum boundary accepted", timeout: 900},
		{name: "one above maximum rejected", timeout: 901, expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newSDKClient(t)
			ctx := context.Background()
			createBasicFunction(t, client, "to-upd")

			_, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
				FunctionName: aws.String("to-upd"),
				Timeout:      aws.Int32(tc.timeout),
			})

			if tc.expectErr {
				assertInvalidParameterValue(t, err, "UpdateFunctionConfiguration(Timeout)")

				return
			}

			if err != nil {
				t.Fatalf("UpdateFunctionConfiguration(Timeout=%d): %v", tc.timeout, err)
			}
		})
	}
}

// TestSDKUpdateFunctionConfigurationOmittedFieldsSucceed covers the common
// Terraform/CLI case of an update that only touches an unrelated field
// (Description): omitting MemorySize/Timeout/Runtime must succeed and keep
// the function's prior values rather than being rejected.
func TestSDKUpdateFunctionConfigurationOmittedFieldsSucceed(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()
	createBasicFunction(t, client, "omit-upd")

	before, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("omit-upd"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration (before): %v", err)
	}

	if _, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("omit-upd"),
		Description:  aws.String("only this changed"),
	}); err != nil {
		t.Fatalf("UpdateFunctionConfiguration (Description only): %v", err)
	}

	after, err := client.GetFunctionConfiguration(ctx, &awslambda.GetFunctionConfigurationInput{
		FunctionName: aws.String("omit-upd"),
	})
	if err != nil {
		t.Fatalf("GetFunctionConfiguration (after): %v", err)
	}

	if aws.ToString(after.Description) != "only this changed" {
		t.Fatalf("Description = %q, want 'only this changed'", aws.ToString(after.Description))
	}

	if aws.ToInt32(after.MemorySize) != aws.ToInt32(before.MemorySize) {
		t.Fatalf("MemorySize = %d, want unchanged %d", aws.ToInt32(after.MemorySize), aws.ToInt32(before.MemorySize))
	}

	if aws.ToInt32(after.Timeout) != aws.ToInt32(before.Timeout) {
		t.Fatalf("Timeout = %d, want unchanged %d", aws.ToInt32(after.Timeout), aws.ToInt32(before.Timeout))
	}

	if after.Runtime != before.Runtime {
		t.Fatalf("Runtime = %s, want unchanged %s", after.Runtime, before.Runtime)
	}
}
