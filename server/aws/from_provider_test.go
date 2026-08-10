package aws_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestNewFromProvider verifies the convenience wiring: a fully-constructed
// provider turned into a running server via NewFromProvider serves real
// aws-sdk-go-v2 traffic (CreateBucket then HeadBucket).
func TestNewFromProvider(t *testing.T) {
	p := cloudemu.NewAWS()

	srv := awsserver.NewFromProvider(p)
	if srv == nil {
		t.Fatal("NewFromProvider returned nil")
	}

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

	client := awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.UsePathStyle = true
	})

	ctx := context.Background()
	const bucket = "from-provider-bucket"

	if _, err := client.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	out, err := client.ListBuckets(ctx, &awss3.ListBucketsInput{})
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}

	found := false
	for _, b := range out.Buckets {
		if aws.ToString(b.Name) == bucket {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("created bucket %q not returned by ListBuckets: %+v", bucket, out.Buckets)
	}
}
