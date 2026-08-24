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

// TestSDKCreateFunctionTimeoutOutOfRange covers the AWS Timeout ceiling: a value
// above 900 seconds is rejected with InvalidParameterValueException (HTTP 400),
// not accepted and echoed back with HTTP 201.
func TestSDKCreateFunctionTimeoutOutOfRange(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("too-slow"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Timeout:      aws.Int32(5000),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("CreateFunction(Timeout=5000) err = %v, want InvalidParameterValueException", err)
	}

	// The rejected function must not have been created.
	if _, err := client.GetFunction(ctx, &awslambda.GetFunctionInput{FunctionName: aws.String("too-slow")}); err == nil {
		t.Fatal("GetFunction after rejected create returned nil error, want NotFound")
	}
}

// TestSDKCreateFunctionMemoryBelowMinimum covers the AWS MemorySize floor: a
// value below 128 MB is rejected with InvalidParameterValueException (HTTP 400).
func TestSDKCreateFunctionMemoryBelowMinimum(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("too-small"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		MemorySize:   aws.Int32(64),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("CreateFunction(MemorySize=64) err = %v, want InvalidParameterValueException", err)
	}
}

// TestSDKCreateFunctionMemoryAboveMaximum covers the AWS MemorySize ceiling: a
// value above the enforced 10240 MB limit is rejected with
// InvalidParameterValueException.
func TestSDKCreateFunctionMemoryAboveMaximum(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("too-big"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		MemorySize:   aws.Int32(40000),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("CreateFunction(MemorySize=40000) err = %v, want InvalidParameterValueException", err)
	}
}

// TestSDKCreateFunctionBoundaryValuesAccepted confirms the happy path is not
// regressed: Timeout=900 and MemorySize=10240 (the enforced ceiling) are valid.
func TestSDKCreateFunctionBoundaryValuesAccepted(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("edge"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		Timeout:      aws.Int32(900),
		MemorySize:   aws.Int32(10240),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	}); err != nil {
		t.Fatalf("CreateFunction(Timeout=900, MemorySize=10240): %v", err)
	}
}

// TestSDKCreateFunctionMemoryJustAboveMaximum covers the tight boundary: 10241 MB
// (one above the enforced 10240 ceiling) is rejected with
// InvalidParameterValueException.
func TestSDKCreateFunctionMemoryJustAboveMaximum(t *testing.T) {
	client, _ := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("one-over"),
		Runtime:      lambdatypes.RuntimeGo1x,
		Role:         aws.String("arn:aws:iam::000000000000:role/test"),
		Handler:      aws.String("main"),
		MemorySize:   aws.Int32(10241),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("z")},
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValueException" {
		t.Fatalf("CreateFunction(MemorySize=10241) err = %v, want InvalidParameterValueException", err)
	}
}
