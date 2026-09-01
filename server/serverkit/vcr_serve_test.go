package serverkit

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// s3ClientFor builds a path-style aws-sdk-go-v2 S3 client pointed at url. Real
// SDK, real SigV4 signing, real wire protocol — the emulator (and, in replay,
// the VCR) sees genuine SDK traffic.
func s3ClientFor(t *testing.T, url string) *s3.Client {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}

	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(url)
		o.UsePathStyle = true
	})
}

// TestVCRRecordReplayS3 is the issue #245 acceptance flow end-to-end: record a
// real aws-sdk-go-v2 S3 session (create bucket, put, get, list) through the wire
// server into a cassette, then replay it against a FRESH, EMPTY backend and prove
// the recorded responses come back verbatim — the real backend never serves them.
func TestVCRRecordReplayS3(t *testing.T) {
	ctx := context.Background()
	cassette := t.TempDir() + "/s3.cassette.json"

	const (
		bucket  = "vcr-bucket"
		key     = "greeting.txt"
		payload = "hello cloudemu"
	)

	// --- RECORD: a real backend serves; VCR captures every interaction. ---
	recApp := newTestApp(t, Config{
		Providers:   []string{"aws"},
		Host:        "127.0.0.1",
		Ports:       map[string]string{"aws": "0"},
		VCRMode:     "record",
		VCRCassette: cassette,
		Out:         io.Discard,
	})

	recTS := httptest.NewServer(recApp.handlerFor(recApp.backends["aws"], recApp.seedFor("aws")))
	defer recTS.Close()

	rec := s3ClientFor(t, recTS.URL)

	if _, err := rec.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("record CreateBucket: %v", err)
	}

	if _, err := rec.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader([]byte(payload)),
		ContentType: aws.String("text/plain"),
	}); err != nil {
		t.Fatalf("record PutObject: %v", err)
	}

	if got := getObject(t, rec, bucket, key); got != payload {
		t.Fatalf("record GetObject body = %q, want %q", got, payload)
	}

	if !listHasBucket(t, rec, bucket) {
		t.Fatalf("record ListBuckets missing %q", bucket)
	}

	// Flush the cassette exactly as shutdown does.
	if err := recApp.vcr.Flush(); err != nil {
		t.Fatalf("flush cassette: %v", err)
	}

	// --- REPLAY: a FRESH, EMPTY backend; VCR must answer from the cassette. ---
	playApp := newTestApp(t, Config{
		Providers:   []string{"aws"},
		Host:        "127.0.0.1",
		Ports:       map[string]string{"aws": "0"},
		VCRMode:     "replay",
		VCRCassette: cassette,
		VCRStrict:   true,
		Out:         io.Discard,
	})

	// Prove the replay backend really is empty: a recorded ListBuckets returning
	// the bucket can then only have come from the cassette.
	if b, err := playApp.targets["aws"].Storage.ListBuckets(ctx); err != nil || len(b) != 0 {
		t.Fatalf("replay backend precondition: buckets=%d err=%v, want 0/nil (empty)", len(b), err)
	}

	playTS := httptest.NewServer(playApp.handlerFor(playApp.backends["aws"], playApp.seedFor("aws")))
	defer playTS.Close()

	play := s3ClientFor(t, playTS.URL)

	// The recorded GET replays verbatim despite the empty backend (a live GET here
	// would be NoSuchBucket).
	if got := getObject(t, play, bucket, key); got != payload {
		t.Fatalf("replay GetObject body = %q, want recorded %q", got, payload)
	}

	// The recorded ListBuckets replays the bucket, though the backend has none.
	if !listHasBucket(t, play, bucket) {
		t.Fatalf("replay ListBuckets missing recorded bucket %q", bucket)
	}

	// A request that was never recorded must NOT be served by the (empty) backend:
	// strict replay fails it, proving replay short-circuits the real handlers.
	if _, err := play.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String("never-recorded")}); err == nil {
		t.Fatal("replay of an unrecorded request should fail under strict mode, got success")
	}
}

func getObject(t *testing.T, c *s3.Client, bucket, key string) string {
	t.Helper()

	out, err := c.GetObject(context.Background(), &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer out.Body.Close()

	b, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read GetObject body: %v", err)
	}

	return string(b)
}

func listHasBucket(t *testing.T, c *s3.Client, name string) bool {
	t.Helper()

	out, err := c.ListBuckets(context.Background(), &s3.ListBucketsInput{})
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}

	for _, b := range out.Buckets {
		if aws.ToString(b.Name) == name {
			return true
		}
	}

	return false
}
