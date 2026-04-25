package chaos_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	cloudemuConfig "github.com/stackshy/cloudemu/config"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

// TestSDKObservesChaos proves the central architectural promise: a real
// aws-sdk-go-v2 client hitting an HTTP server backed by a chaos-wrapped
// driver experiences the chaos exactly the same as a Go-API caller would.
func TestSDKObservesChaos(t *testing.T) {
	provider := cloudemu.NewAWS()
	engine := chaos.New(cloudemuConfig.RealClock{})
	defer engine.Stop()

	// Wrap the S3 driver with chaos before handing it to the HTTP server.
	wrappedS3 := chaos.WrapBucket(provider.S3, engine)

	srv := awsserver.New(awsserver.Drivers{S3: wrappedS3})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("t", "t", "")))
	if err != nil {
		t.Fatal(err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.UsePathStyle = true
		// Disable SDK retries so we test the raw chaos behaviour, not how
		// the retryer hides it. Real apps would re-enable retries.
		o.RetryMaxAttempts = 1
	})
	ctx := context.Background()

	// Baseline: ops succeed.
	if _, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("baseline"),
	}); err != nil {
		t.Fatalf("baseline CreateBucket: %v", err)
	}

	// Apply S3 outage for a short window.
	engine.Apply(chaos.ServiceOutage("storage", 200*time.Millisecond))

	// Calls during the outage fail.
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("baseline"), Key: aws.String("k"),
		Body: bytes.NewReader([]byte("x")),
	})
	if err == nil {
		t.Fatal("expected failure during chaos outage, got nil")
	}

	// Wait past the outage window — ops succeed again (the SDK observes recovery).
	time.Sleep(300 * time.Millisecond)

	if _, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("baseline"), Key: aws.String("k"),
		Body: bytes.NewReader([]byte("after-recovery")),
	}); err != nil {
		t.Fatalf("expected recovery, still failing: %v", err)
	}

	// Engine recorded the chaos events.
	rec := engine.Recorded()
	if len(rec) == 0 {
		t.Error("expected chaos engine to record events during outage")
	}
}

// TestSDKObservesLatencySpike confirms latency injected at the driver layer
// is reflected in observed call duration on the SDK side.
func TestSDKObservesLatencySpike(t *testing.T) {
	provider := cloudemu.NewAWS()
	engine := chaos.New(cloudemuConfig.RealClock{})
	defer engine.Stop()

	wrappedS3 := chaos.WrapBucket(provider.S3, engine)
	srv := awsserver.New(awsserver.Drivers{S3: wrappedS3})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	cfg, _ := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("t", "t", "")))
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.UsePathStyle = true
	})
	ctx := context.Background()

	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("lat"),
	}); err != nil {
		t.Fatal(err)
	}

	engine.Apply(chaos.LatencySpike("storage", 100*time.Millisecond, time.Hour))

	start := time.Now()

	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("lat"), Key: aws.String("k"),
		Body: bytes.NewReader([]byte("x")),
	})
	if err != nil {
		t.Fatalf("PutObject under latency: %v", err)
	}

	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("expected ≥100ms latency, observed %v", elapsed)
	}
}
