package redshift_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsredshift "github.com/aws/aws-sdk-go-v2/service/redshift"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	"github.com/stackshy/cloudemu/v2/server/aws/redshift"
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

// TestSDKDescribeEventsAnsweredByRedshift checks DescribeEvents is served by
// Redshift, not RDS, on the full server.
func TestSDKDescribeEventsAnsweredByRedshift(t *testing.T) {
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

	client := awsredshift.NewFromConfig(cfg, func(o *awsredshift.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	out, err := client.DescribeEvents(context.Background(), &awsredshift.DescribeEventsInput{})
	if err != nil {
		t.Fatalf("DescribeEvents: %v", err)
	}

	if len(out.Events) != 0 {
		t.Fatalf("Events = %d, want 0", len(out.Events))
	}

	if !strings.Contains(rec.last, `xmlns="`+redshift.Namespace+`"`) {
		t.Fatalf("response not in the Redshift namespace: %s", rec.last)
	}
}

// TestMatchesDescribeEvents checks Redshift claims its own DescribeEvents and
// passes on ones meant for RDS or ElastiCache.
func TestMatchesDescribeEvents(t *testing.T) {
	h := redshift.New(nil)

	cases := []struct {
		name    string
		service string // "" means unsigned
		version string // "" means no Version field
		want    bool
	}{
		{"redshift-signed", "redshift", "2012-12-01", true},
		{"rds-signed", "rds", "2014-10-31", false},
		{"elasticache-signed", "elasticache", "2015-02-02", false},
		{"unsigned no version", "", "", true},
		{"unsigned redshift version", "", "2012-12-01", true},
		{"unsigned elasticache version", "", "2015-02-02", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "Action=DescribeEvents"
			if tc.version != "" {
				body += "&Version=" + tc.version
			}

			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			if tc.service != "" {
				r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKID/20260101/us-east-1/"+
					tc.service+"/aws4_request, Signature=x")
			}

			if got := h.Matches(r); got != tc.want {
				t.Fatalf("Matches(DescribeEvents, scope=%q, version=%q) = %v, want %v",
					tc.service, tc.version, got, tc.want)
			}
		})
	}
}
