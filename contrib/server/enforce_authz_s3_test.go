package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const s3ReaderDoc = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject","s3:ListBucket"],` +
	`"Resource":["arn:aws:s3:::data","arn:aws:s3:::data/*"]}]}`

// s3Client returns a path-style S3 client, or a virtual-hosted one that sends
// Host "<bucket>.localhost:<port>" and dials the server whatever the name.
func s3Client(t *testing.T, endpoint string, c aws.Credentials, virtualHosted bool) *s3.Client {
	t.Helper()

	cfg := credsConfig(t, c)

	if !virtualHosted {
		return s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		})
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}

	dialer := &net.Dialer{}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, u.Host)
	}}

	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("http://localhost:" + u.Port())
		o.HTTPClient = &http.Client{Transport: vhostOnly{t: t, next: transport}}
	})
}

// vhostOnly fails the test if the SDK falls back to a path-style request, so
// the virtual-hosted case really sends the bucket in the Host.
type vhostOnly struct {
	t    *testing.T
	next http.RoundTripper
}

func (v vhostOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if !strings.Contains(r.URL.Host, ".localhost:") {
		v.t.Errorf("request to %s is not virtual-hosted", r.URL)
	}

	return v.next.RoundTrip(r)
}

// testEnforceAuthS3 drives the S3 SDK as a reader allowed s3:GetObject and
// s3:ListBucket on the data bucket only, path-style and virtual-hosted, plus
// presigned URLs.
func testEnforceAuthS3(t *testing.T, endpoint string, boot awsClients) {
	ctx := context.Background()
	admin := s3Client(t, endpoint, boot.cred, false)

	for _, b := range []string{"data", "other"} {
		_, err := admin.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(b)})
		wantOK(t, "CreateBucket "+b, err)
	}

	for _, o := range [][2]string{{"data", "report.csv"}, {"other", "secret"}} {
		_, err := admin.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(o[0]), Key: aws.String(o[1]), Body: strings.NewReader("x")})
		wantOK(t, "PutObject "+o[1], err)
	}

	readerCreds := boot.newUser(t, "s3reader", s3ReaderDoc)

	for _, style := range []struct {
		name    string
		virtual bool
	}{{"path-style", false}, {"virtual-hosted", true}} {
		t.Run(style.name, func(t *testing.T) {
			reader := s3Client(t, endpoint, readerCreds, style.virtual)

			_, err := reader.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("data"), Key: aws.String("report.csv")})
			wantOK(t, "GetObject", err)
			_, err = reader.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String("data")})
			wantOK(t, "ListObjectsV2", err)

			_, err = reader.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("data"), Key: aws.String("new"), Body: strings.NewReader("y")})
			wantCode(t, "PutObject", err, "AccessDenied")
			_, err = reader.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("reader-bucket")})
			wantCode(t, "CreateBucket", err, "AccessDenied")
			_, err = reader.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("other"), Key: aws.String("secret")})
			wantCode(t, "GetObject on another bucket", err, "AccessDenied")
			_, err = reader.CopyObject(ctx, &s3.CopyObjectInput{
				Bucket: aws.String("data"), Key: aws.String("copied"), CopySource: aws.String("other/secret"),
			})
			wantCode(t, "CopyObject from another bucket", err, "AccessDenied")
		})
	}

	for _, key := range []string{"new", "copied"} {
		if _, err := admin.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("data"), Key: aws.String(key)}); err == nil {
			t.Fatalf("a denied request wrote data/%s", key)
		}
	}

	presign := s3.NewPresignClient(s3Client(t, endpoint, readerCreds, false))

	for _, tc := range []struct {
		bucket, key string
		want        int
	}{{"data", "report.csv", http.StatusOK}, {"other", "secret", http.StatusForbidden}} {
		req, err := presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(tc.bucket), Key: aws.String(tc.key)})
		wantOK(t, "PresignGetObject", err)

		if status := httpGet(t, req.URL); status != tc.want {
			t.Fatalf("presigned GET %s/%s = %d, want %d", tc.bucket, tc.key, status, tc.want)
		}
	}
}

func httpGet(t *testing.T, rawURL string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode
}
