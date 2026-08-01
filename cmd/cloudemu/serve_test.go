package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"google.golang.org/api/option"
)

// TestServeOutOfProcess builds the binary, runs it as a separate process, and
// drives real cloud SDK clients against the listening sockets — the way an
// actual user runs `cloudemu serve` and points an app at it. It exercises the
// HTTP path (AWS) and the self-signed HTTPS path (Azure) end to end.
func TestServeOutOfProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and execs a binary; skipped in -short")
	}

	awsPort := freePort(t)
	azurePort := freePort(t)
	gcpPort := freePort(t)

	bin := filepath.Join(t.TempDir(), "cloudemu")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "serve",
		"--host", "127.0.0.1",
		"--aws-port", awsPort,
		"--azure-port", azurePort,
		"--gcp-port", gcpPort,
		"--k8s-port", "", // Kubernetes data-plane not needed for this test
		"--quiet",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	})

	awsEndpoint := "http://127.0.0.1:" + awsPort
	if !waitReady(awsEndpoint+"/", 15*time.Second) {
		t.Fatalf("AWS endpoint %s never became ready", awsEndpoint)
	}

	t.Run("aws-s3-over-http", func(t *testing.T) {
		ctx := context.Background()
		cfg, err := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion("us-east-1"),
			awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider("test", "test", "")))
		if err != nil {
			t.Fatal(err)
		}
		client := s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(awsEndpoint)
			o.UsePathStyle = true
		})

		if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("demo")}); err != nil {
			t.Fatalf("CreateBucket over the socket: %v", err)
		}
		out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
		if err != nil {
			t.Fatalf("ListBuckets: %v", err)
		}
		if len(out.Buckets) != 1 || aws.ToString(out.Buckets[0].Name) != "demo" {
			t.Fatalf("ListBuckets = %+v, want [demo]", out.Buckets)
		}
	})

	t.Run("azure-arm-over-self-signed-https", func(t *testing.T) {
		ctx := context.Background()
		// A transport that trusts the emulator's self-signed cert. A real user
		// either does this for local dev or installs the printed cert.
		httpClient := &http.Client{Transport: &http.Transport{
			// #nosec G402 -- local test trusts the emulator's self-signed cert.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}}
		azureEndpoint := "https://127.0.0.1:" + azurePort

		cloudCfg := cloud.Configuration{
			ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
			Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {Endpoint: azureEndpoint, Audience: "https://management.azure.com"},
			},
		}
		opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
			Cloud:     cloudCfg,
			Transport: httpClient,
			Retry:     policy.RetryOptions{MaxRetries: -1},
		}}

		cf, err := armservicebus.NewClientFactory("000000000000", fakeCred{}, opts)
		if err != nil {
			t.Fatal(err)
		}
		client := cf.NewNamespacesClient()

		poller, err := client.BeginCreateOrUpdate(ctx, "rg-1", "ns-demo", armservicebus.SBNamespace{
			Location: to.Ptr("eastus"),
		}, nil)
		if err != nil {
			t.Fatalf("BeginCreateOrUpdate over HTTPS: %v", err)
		}
		if _, err := poller.PollUntilDone(ctx, nil); err != nil {
			t.Fatalf("poll: %v", err)
		}
		got, err := client.Get(ctx, "rg-1", "ns-demo", nil)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Name == nil || *got.Name != "ns-demo" {
			t.Fatalf("Get namespace = %+v, want ns-demo", got.SBNamespace)
		}
	})

	// #244 acceptance: seed resources (via the real SDK), reset, confirm empty.
	t.Run("admin-reset-clears-state", func(t *testing.T) {
		ctx := context.Background()
		cfg, err := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion("us-east-1"),
			awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider("test", "test", "")))
		if err != nil {
			t.Fatal(err)
		}
		client := s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(awsEndpoint)
			o.UsePathStyle = true
		})

		if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("reset-me")}); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}
		before, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
		if err != nil {
			t.Fatal(err)
		}
		if len(before.Buckets) == 0 {
			t.Fatal("expected at least the bucket we just created before reset")
		}

		// A second provider, to prove reset is global: a POST to the AWS port
		// must also clear GCP state.
		gcs, err := storage.NewClient(ctx,
			option.WithEndpoint("http://127.0.0.1:"+gcpPort+"/storage/v1/"),
			option.WithoutAuthentication())
		if err != nil {
			t.Fatal(err)
		}
		if err := gcs.Bucket("gcp-reset-me").Create(ctx, "cloudemu-local", nil); err != nil {
			t.Fatalf("GCS Bucket.Create: %v", err)
		}
		if _, err := gcs.Bucket("gcp-reset-me").Attrs(ctx); err != nil {
			t.Fatalf("GCS bucket should exist before reset: %v", err)
		}

		resp, err := http.Post(awsEndpoint+"/_cloudemu/reset", "application/json", nil)
		if err != nil {
			t.Fatalf("POST /_cloudemu/reset: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("reset status = %d, want 200", resp.StatusCode)
		}

		after, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
		if err != nil {
			t.Fatalf("ListBuckets after reset: %v", err)
		}
		if len(after.Buckets) != 0 {
			t.Fatalf("after reset the backend still has %d buckets, want 0", len(after.Buckets))
		}
		// GCP was reset too, even though we POSTed to the AWS port.
		if _, err := gcs.Bucket("gcp-reset-me").Attrs(ctx); err == nil {
			t.Fatal("GCP bucket survived a reset issued on the AWS port — reset is not global")
		}
	})

	// Concurrent resets must serialise and stay consistent, not deadlock or
	// corrupt the swap — this exercises the rebuild mutex.
	t.Run("admin-reset-concurrent", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make(chan error, 6)
		for range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := http.Post(awsEndpoint+"/_cloudemu/reset", "application/json", nil)
				if err != nil {
					errs <- err
					return
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					errs <- fmt.Errorf("status %d", resp.StatusCode)
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("concurrent reset failed: %v", err)
		}

		// The server is still usable after the storm of resets.
		ctx := context.Background()
		cfg, _ := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion("us-east-1"),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("t", "t", "")))
		client := s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(awsEndpoint)
			o.UsePathStyle = true
		})
		if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("post-storm")}); err != nil {
			t.Fatalf("server unusable after concurrent resets: %v", err)
		}
	})

	// #244 seed: POST a fixture and read the seeded resource back via the SDK.
	t.Run("admin-seed-loads-fixtures", func(t *testing.T) {
		ctx := context.Background()
		http.Post(awsEndpoint+"/_cloudemu/reset", "application/json", nil) //nolint:errcheck // clean slate

		fixture := `{"buckets":[{"name":"seeded-bucket","objects":[{"key":"hello.txt","body":"hi from seed"}]}]}`
		resp, err := http.Post(awsEndpoint+"/_cloudemu/seed", "application/json", strings.NewReader(fixture))
		if err != nil {
			t.Fatalf("POST /_cloudemu/seed: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("seed status = %d, want 200", resp.StatusCode)
		}

		cfg, _ := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion("us-east-1"),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("t", "t", "")))
		client := s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(awsEndpoint)
			o.UsePathStyle = true
		})
		obj, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("seeded-bucket"), Key: aws.String("hello.txt")})
		if err != nil {
			t.Fatalf("seeded object not readable via SDK: %v", err)
		}
		body, _ := io.ReadAll(obj.Body)
		if string(body) != "hi from seed" {
			t.Fatalf("seeded object body = %q, want %q", body, "hi from seed")
		}
	})
}

// fakeCred is a static-token Azure credential; cloudemu does not validate it.
type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// waitReady polls url until it answers or the deadline passes.
func waitReady(url string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

// freePort asks the OS for an unused TCP port and returns it as a string. The
// listener is closed immediately; the small reuse window is acceptable in a
// test.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}
