package aws

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithy "github.com/aws/smithy-go"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// iamClientAt builds a real aws-sdk-go-v2 IAM client that signs with the given
// static credentials and points at the emulator endpoint.
func iamClientAt(t *testing.T, endpoint, accessKeyID, secret, token string) *iam.Client {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKeyID, secret, token),
		),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}

	return iam.NewFromConfig(cfg, func(o *iam.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
}

// registerUserWithKey creates an IAM user and one access key directly on the
// driver, returning the AKIA id and its secret so a real SDK client can sign
// with a key the emulator will recognize.
func registerUserWithKey(t *testing.T, m iamdriver.IAM, userName string) (accessKeyID, secret string) {
	t.Helper()

	ctx := context.Background()
	if _, err := m.CreateUser(ctx, iamdriver.UserConfig{Name: userName}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	ak, err := m.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{UserName: userName})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	return ak.AccessKeyID, ak.SecretAccessKey
}

func apiErrorCode(t *testing.T, err error) string {
	t.Helper()

	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not a smithy.APIError: %v", err)
	}

	return apiErr.ErrorCode()
}

// TestCompatAWSSigV4AuthDisabled confirms the default (auth off) path is
// unchanged: a call signed with the harness's dummy creds still succeeds.
func TestCompatAWSSigV4AuthDisabled(t *testing.T) {
	cloud := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{IAM: cloud.IAM})

	client := iam.NewFromConfig(sess.Config(), func(o *iam.Options) {
		o.BaseEndpoint = aws.String(sess.Endpoint())
	})

	if _, err := client.ListUsers(context.Background(), &iam.ListUsersInput{}); err != nil {
		t.Fatalf("ListUsers with auth disabled should succeed, got: %v", err)
	}
}

// TestCompatAWSSigV4AuthEnabled exercises the enforced path with a real SDK
// signer: a registered key verifies, a wrong secret and an unknown key are
// rejected with the correct AWS error codes, and an STS-style temporary (ASIA)
// credential is accepted (its signature is a documented follow-up).
func TestCompatAWSSigV4AuthEnabled(t *testing.T) {
	cloud := cloudemu.NewAWS()

	akid, secret := registerUserWithKey(t, cloud.IAM, "alice")

	sess := compat.BootAWS(t, awsserver.Drivers{
		IAM:         cloud.IAM,
		EC2:         cloud.EC2, // shares the query protocol; exercises dispatch after the gate
		S3:          cloud.S3,  // REST/XML with a signed object-key path (single-encode branch)
		EnforceAuth: true,
	})
	ctx := context.Background()

	t.Run("RegisteredKeySucceeds", func(t *testing.T) {
		client := iamClientAt(t, sess.Endpoint(), akid, secret, "")
		out, err := client.GetUser(ctx, &iam.GetUserInput{UserName: aws.String("alice")})
		if err != nil {
			t.Fatalf("signed request with a registered key should succeed, got: %v", err)
		}
		if out.User == nil || aws.ToString(out.User.UserName) != "alice" {
			t.Fatalf("unexpected GetUser response: %+v", out)
		}
	})

	t.Run("WrongSecretRejected", func(t *testing.T) {
		client := iamClientAt(t, sess.Endpoint(), akid, "secret-wrong-value", "")
		_, err := client.ListUsers(ctx, &iam.ListUsersInput{})
		if code := apiErrorCode(t, err); code != "SignatureDoesNotMatch" {
			t.Fatalf("wrong secret: want SignatureDoesNotMatch, got %q", code)
		}
	})

	t.Run("UnknownKeyRejected", func(t *testing.T) {
		client := iamClientAt(t, sess.Endpoint(), "AKIAUNKNOWNKEY000000", "secret-does-not-matter", "")
		_, err := client.ListUsers(ctx, &iam.ListUsersInput{})
		if code := apiErrorCode(t, err); code != "InvalidClientTokenId" {
			t.Fatalf("unknown key: want InvalidClientTokenId, got %q", code)
		}
	})

	t.Run("TempCredentialAccepted", func(t *testing.T) {
		client := iamClientAt(t, sess.Endpoint(), "ASIATEMPCREDENTIAL00", "any-synthetic-secret", "session-token")
		if _, err := client.ListUsers(ctx, &iam.ListUsersInput{}); err != nil {
			t.Fatalf("STS-style temporary (ASIA) credential should pass through, got: %v", err)
		}
	})

	// A real S3 client disables SigV4 path escaping, so its signed object-key
	// path exercises the verifier's S3 single-encode branch end-to-end.
	t.Run("S3SignedObjectKey", func(t *testing.T) {
		cfg, err := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion("us-east-1"),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(akid, secret, "")),
		)
		if err != nil {
			t.Fatalf("load aws config: %v", err)
		}

		s3c := s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(sess.Endpoint())
			o.UsePathStyle = true
		})

		const bucket = "auth-bucket"
		if _, err := s3c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}

		key := "my dir/hello world.txt" // spaces exercise path canonicalization
		if _, err := s3c.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body:   strings.NewReader("hi"),
		}); err != nil {
			t.Fatalf("PutObject with a spaced key should verify, got: %v", err)
		}
	})
}
