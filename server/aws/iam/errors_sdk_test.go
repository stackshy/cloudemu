package iam_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

// newClient is a shared helper for error-path tests. Lives here (rather than
// sharing newSDKClient) so the SDK-driver wiring used to assert error codes
// is colocated with those tests.
func newClient(t *testing.T) *awsiam.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		IAM: cloud.IAM,
		EC2: cloud.EC2,
	})

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

	return awsiam.NewFromConfig(cfg, func(o *awsiam.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

// TestSDKIAMNoSuchEntityIsTyped locks in the wire shape of the not-found
// error: the SDK must unmarshal it as *types.NoSuchEntityException, not as
// a generic awserror. Regressions on the XML error envelope or the
// NoSuchEntity code would break this.
func TestSDKIAMNoSuchEntityIsTyped(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	_, err := client.GetUser(ctx, &awsiam.GetUserInput{
		UserName: aws.String("nobody"),
	})
	if err == nil {
		t.Fatalf("GetUser on missing user: expected error, got nil")
	}

	var nse *iamtypes.NoSuchEntityException
	if !errors.As(err, &nse) {
		t.Fatalf("expected *NoSuchEntityException, got %T: %v", err, err)
	}
}

// TestSDKIAMEntityAlreadyExistsIsTyped is the dual: duplicate-create must
// surface as *types.EntityAlreadyExistsException, not generic.
func TestSDKIAMEntityAlreadyExistsIsTyped(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	if _, err := client.CreateUser(ctx, &awsiam.CreateUserInput{
		UserName: aws.String("dupe"),
	}); err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}

	_, err := client.CreateUser(ctx, &awsiam.CreateUserInput{
		UserName: aws.String("dupe"),
	})
	if err == nil {
		t.Fatalf("second CreateUser: expected error, got nil")
	}

	var eae *iamtypes.EntityAlreadyExistsException
	if !errors.As(err, &eae) {
		t.Fatalf("expected *EntityAlreadyExistsException, got %T: %v", err, err)
	}
}

// TestSDKIAMClaimsActionsBeforeEC2 verifies the dispatch precedence
// documented in handler.go: with both IAM and EC2 wired, an IAM request
// must be claimed by the IAM handler before EC2 sees it. We assert this by
// observing that CreateUser succeeds end-to-end — if EC2 claimed the request
// first, it would return InvalidAction (CreateUser is not an EC2 action) or
// otherwise mangle the response. The test stays meaningful even if other
// query-protocol handlers are added later, as long as IAM stays before EC2.
func TestSDKIAMClaimsActionsBeforeEC2(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()

	out, err := client.CreateUser(ctx, &awsiam.CreateUserInput{
		UserName: aws.String("precedence"),
	})
	if err != nil {
		t.Fatalf("CreateUser with EC2 wired: %v "+
			"(EC2 may be claiming IAM requests first)", err)
	}

	if aws.ToString(out.User.UserName) != "precedence" {
		t.Fatalf("got user %q, want precedence", aws.ToString(out.User.UserName))
	}
}
