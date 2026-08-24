package iam_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// TestSDKAccessKeyLimit asserts a user is capped at two access keys and the
// third create fails with LimitExceeded (HTTP 409).
func TestSDKAccessKeyLimit(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &iam.CreateUserInput{UserName: aws.String("keyed")}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	for i := range 2 {
		if _, err := client.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{
			UserName: aws.String("keyed"),
		}); err != nil {
			t.Fatalf("CreateAccessKey #%d: %v", i+1, err)
		}
	}

	_, err := client.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{UserName: aws.String("keyed")})

	var limit *iamtypes.LimitExceededException
	if !errors.As(err, &limit) {
		t.Fatalf("third CreateAccessKey: want LimitExceededException, got %v", err)
	}
}
