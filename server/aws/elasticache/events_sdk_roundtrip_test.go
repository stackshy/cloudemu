package elasticache_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	"github.com/stackshy/cloudemu/v2/server/aws/elasticache"
)

// bodyRecorder keeps the last response body so a test can check which
// service answered.
type bodyRecorder struct {
	last string
}

func (b *bodyRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()

	if err != nil {
		return nil, err
	}

	b.last = string(raw)
	resp.Body = io.NopCloser(bytes.NewReader(raw))

	return resp, nil
}

// newFullServerClient points the SDK at the full AWS server, so RDS and
// Redshift are registered ahead of ElastiCache as in production.
func newFullServerClient(t *testing.T) (*awselasticache.Client, *bodyRecorder) {
	t.Helper()

	ts := httptest.NewServer(awsserver.NewFromProvider(cloudemu.NewAWS()))
	t.Cleanup(ts.Close)

	rec := &bodyRecorder{}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
		awsconfig.WithHTTPClient(&http.Client{Transport: rec}),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := awselasticache.NewFromConfig(cfg, func(o *awselasticache.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	return client, rec
}

// TestSDKDescribeEventsAnsweredByElastiCache checks DescribeEvents is served
// by ElastiCache, not RDS, and that bad inputs are rejected.
func TestSDKDescribeEventsAnsweredByElastiCache(t *testing.T) {
	client, rec := newFullServerClient(t)
	ctx := context.Background()

	out, err := client.DescribeEvents(ctx, &awselasticache.DescribeEventsInput{
		SourceType: ectypes.SourceTypeCacheCluster,
		MaxRecords: aws.Int32(50),
		Duration:   aws.Int32(60),
	})
	if err != nil {
		t.Fatalf("DescribeEvents: %v", err)
	}

	if len(out.Events) != 0 {
		t.Fatalf("Events = %d, want 0", len(out.Events))
	}

	if !strings.Contains(rec.last, `xmlns="`+elasticache.Namespace+`"`) {
		t.Fatalf("response not in the ElastiCache namespace: %s", rec.last)
	}

	bad := []struct {
		name string
		in   *awselasticache.DescribeEventsInput
	}{
		{"bad source type", &awselasticache.DescribeEventsInput{SourceType: "db-instance"}},
		{"max records too low", &awselasticache.DescribeEventsInput{MaxRecords: aws.Int32(5)}},
		{"max records too high", &awselasticache.DescribeEventsInput{MaxRecords: aws.Int32(101)}},
		{"duration too long", &awselasticache.DescribeEventsInput{Duration: aws.Int32(20161)}},
	}

	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.DescribeEvents(ctx, tc.in)

			var apiErr smithy.APIError
			if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValue" {
				t.Fatalf("err = %v, want InvalidParameterValue", err)
			}
		})
	}
}
