package compat

import (
	"context"
	"net/http/httptest"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const providerAWS = "aws"

// AWSSession is a Session backed by CloudEmu's AWS wire server plus a real
// aws-sdk-go-v2 config pointed at it.
type AWSSession struct {
	*Session

	cfg aws.Config
}

// BootAWS starts CloudEmu's AWS wire server in-process for the given drivers
// and returns a session wired with a real aws-sdk-go-v2 config (dummy creds,
// us-east-1). Recorded results flush on test cleanup.
//
//nolint:gocritic // by-value Drivers mirrors awsserver.New's ergonomic API
func BootAWS(tb TB, d awsserver.Drivers) *AWSSession {
	tb.Helper()

	srv := awsserver.New(d)
	ts := httptest.NewServer(srv)
	tb.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		tb.Fatalf("compat: load aws config: %v", err)
	}

	s := &Session{tb: tb, provider: providerAWS, endpoint: ts.URL}
	tb.Cleanup(s.flush)

	return &AWSSession{Session: s, cfg: cfg}
}

// S3Client returns a real S3 client pointed at the emulator, with path-style
// addressing (required against a single-host emulator).
func (a *AWSSession) S3Client() *s3.Client {
	return s3.NewFromConfig(a.cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(a.endpoint)
		o.UsePathStyle = true
	})
}

// DynamoDBClient returns a real DynamoDB client pointed at the emulator.
func (a *AWSSession) DynamoDBClient() *dynamodb.Client {
	return dynamodb.NewFromConfig(a.cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(a.endpoint)
	})
}

// SQSClient returns a real SQS client pointed at the emulator.
func (a *AWSSession) SQSClient() *sqs.Client {
	return sqs.NewFromConfig(a.cfg, func(o *sqs.Options) {
		o.BaseEndpoint = aws.String(a.endpoint)
	})
}
