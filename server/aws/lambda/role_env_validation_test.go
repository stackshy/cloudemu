package lambda_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/smithy-go"
)

const validRoleARN = "arn:aws:iam::000000000000:role/test"

func createInput(name, role string, env map[string]string) *awslambda.CreateFunctionInput {
	in := &awslambda.CreateFunctionInput{
		FunctionName: aws.String(name),
		Runtime:      lambdatypes.RuntimePython312,
		Role:         aws.String(role),
		Handler:      aws.String("index.handler"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	}

	if env != nil {
		in.Environment = &lambdatypes.Environment{Variables: env}
	}

	return in
}

func requireLambdaErrCode(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != want {
		t.Fatalf("err = %v, want %s", err, want)
	}
}

func TestSDKCreateFunctionRoleAndEnvironmentValidation(t *testing.T) {
	tests := []struct {
		name     string
		role     string
		env      map[string]string
		wantCode string
	}{
		{name: "malformed role", role: "not-an-arn", wantCode: "ValidationException"},
		{name: "role without account", role: "arn:aws:iam::role/test", wantCode: "ValidationException"},
		{name: "key with hyphen", role: validRoleARN, env: map[string]string{"MY-KEY": "v"}, wantCode: "ValidationException"},
		{name: "single character key", role: validRoleARN, env: map[string]string{"A": "v"}, wantCode: "ValidationException"},
		{name: "reserved AWS_REGION", role: validRoleARN, env: map[string]string{"AWS_REGION": "us-west-2"},
			wantCode: "InvalidParameterValueException"},
		{name: "over 4 KB", role: validRoleARN, env: map[string]string{"BIG": strings.Repeat("a", 5000)},
			wantCode: "InvalidParameterValueException"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newSDKClient(t)
			ctx := context.Background()

			_, err := client.CreateFunction(ctx, createInput("fn", tc.role, tc.env))
			requireLambdaErrCode(t, err, tc.wantCode)

			if _, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("fn")}); err == nil {
				t.Fatal("rejected function was created")
			}
		})
	}
}

func TestSDKCreateFunctionValidRoleAndEnvironment(t *testing.T) {
	client, _ := newSDKClient(t)

	out, err := client.CreateFunction(context.Background(),
		createInput("ok", "arn:aws-us-gov:iam::123456789012:role/service-role/my.role", map[string]string{"STAGE": "dev", "LOG_LEVEL": "debug"}))
	if err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	if got := out.Environment.Variables["STAGE"]; got != "dev" {
		t.Fatalf("STAGE = %q, want dev", got)
	}
}

func TestSDKUpdateFunctionConfigurationRoleAndEnvironmentValidation(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, createInput("upd", validRoleARN, nil)); err != nil {
		t.Fatalf("CreateFunction: %v", err)
	}

	_, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("upd"), Role: aws.String("bad-role"),
	})
	requireLambdaErrCode(t, err, "ValidationException")

	_, err = client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("upd"),
		Environment:  &lambdatypes.Environment{Variables: map[string]string{"AWS_SECRET_ACCESS_KEY": "x"}},
	})
	requireLambdaErrCode(t, err, "InvalidParameterValueException")

	// An update with no Role still works.
	if _, err := client.UpdateFunctionConfiguration(ctx, &awslambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("upd"), Description: aws.String("d"),
	}); err != nil {
		t.Fatalf("UpdateFunctionConfiguration without role: %v", err)
	}
}
