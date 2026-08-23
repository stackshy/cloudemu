package ec2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"
)

// TestCreateTagsUnknownInstanceErrorCode pins that CreateTags on a non-existent
// instance id returns InvalidInstanceID.NotFound (the resource-specific code
// real EC2 uses), not the generic InvalidID.NotFound.
func TestCreateTagsUnknownInstanceErrorCode(t *testing.T) {
	ctx := context.Background()
	c := newEC2Client(t)

	_, err := c.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{"i-deadbeefdeadbeef0"},
		Tags:      []ec2types.Tag{{Key: aws.String("k"), Value: aws.String("v")}},
	})
	if err == nil {
		t.Fatal("CreateTags on unknown instance id should fail")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidInstanceID.NotFound" {
		t.Fatalf("want InvalidInstanceID.NotFound, got %v", err)
	}
}
