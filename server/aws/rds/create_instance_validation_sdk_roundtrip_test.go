package rds_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	smithy "github.com/aws/smithy-go"
)

// TestSDKRDSCreateInstanceInvalidEngine: CreateDBInstance with an engine outside
// the accepted enum is rejected with InvalidParameterValue, matching real RDS —
// so a typo'd engine fails against cloudemu exactly as it would in production.
func TestSDKRDSCreateInstanceInvalidEngine(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("bad-engine"),
		Engine:               aws.String("not-a-real-engine"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		MasterUsername:       aws.String("admin"),
		MasterUserPassword:   aws.String("supersecret"),
		AllocatedStorage:     aws.Int32(20),
	})
	if err == nil {
		t.Fatal("CreateDBInstance with an invalid engine should fail")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not a smithy APIError: %v", err)
	}

	if apiErr.ErrorCode() != "InvalidParameterValue" {
		t.Fatalf("ErrorCode = %q, want InvalidParameterValue", apiErr.ErrorCode())
	}
}

// TestSDKRDSCreateInstanceInvalidClass: CreateDBInstance with an unknown
// DBInstanceClass is rejected with InvalidParameterValue.
func TestSDKRDSCreateInstanceInvalidClass(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("bad-class"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.bogus.xlarge"),
		MasterUsername:       aws.String("admin"),
		MasterUserPassword:   aws.String("supersecret"),
		AllocatedStorage:     aws.Int32(20),
	})
	if err == nil {
		t.Fatal("CreateDBInstance with an invalid instance class should fail")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not a smithy APIError: %v", err)
	}

	if apiErr.ErrorCode() != "InvalidParameterValue" {
		t.Fatalf("ErrorCode = %q, want InvalidParameterValue", apiErr.ErrorCode())
	}
}

// TestSDKRDSCreateInstanceValidEngineAndClass: a valid engine + class still
// succeeds, guarding against an over-strict validator.
func TestSDKRDSCreateInstanceValidEngineAndClass(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("good-db"),
		Engine:               aws.String("postgres"),
		DBInstanceClass:      aws.String("db.r5.large"),
		MasterUsername:       aws.String("admin"),
		MasterUserPassword:   aws.String("supersecret"),
		AllocatedStorage:     aws.Int32(20),
	})
	if err != nil {
		t.Fatalf("CreateDBInstance with a valid engine/class should succeed: %v", err)
	}
}
