package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"google.golang.org/api/option"
)

// buildServeBinary compiles the cloudemu binary once for a durability test.
func buildServeBinary(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "cloudemu")
	build := exec.Command("go", "build", "-o", bin, ".")

	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	return bin
}

// durabilityPorts is one run's endpoint ports plus its persistent state file.
type durabilityPorts struct {
	aws, gcp, azure, state string
}

// startServe launches `cloudemu serve` with the given persistence settings and
// waits for the AWS endpoint to answer. The caller must terminate the process.
func startServe(t *testing.T, bin string, p durabilityPorts, strategy, interval string) *exec.Cmd {
	t.Helper()

	cmd := exec.Command(bin, "serve",
		"--host", "127.0.0.1",
		"--aws-port", p.aws,
		"--gcp-port", p.gcp,
		"--azure-port", p.azure,
		"--k8s-port", "",
		"--quiet",
		"--persist",
		"--state-file", p.state,
		"--persist-strategy", strategy,
		"--persist-interval", interval,
	)
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}

	if !waitReady("http://127.0.0.1:"+p.aws+"/", 15*time.Second) {
		_ = cmd.Process.Kill()
		t.Fatal("AWS endpoint never became ready")
	}

	return cmd
}

// killHard sends SIGKILL — a hard crash with no graceful shutdown, so only saves
// that already reached disk survive. This is the scenario today's --persist fails.
func killHard(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

// waitFileContains polls the state file until it holds substr, proving a
// background save landed before we crash the process.
func waitFileContains(t *testing.T, path, substr string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && bytes.Contains(b, []byte(substr)) {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("state file %s never contained %q within %s", path, substr, timeout)
}

func s3Client(t *testing.T, port string) *s3.Client {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatal(err)
	}

	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("http://127.0.0.1:" + port)
		o.UsePathStyle = true
	})
}

func iamClient(t *testing.T, port string) *iam.Client {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatal(err)
	}

	return iam.NewFromConfig(cfg, func(o *iam.Options) {
		o.BaseEndpoint = aws.String("http://127.0.0.1:" + port)
	})
}

func gcsClient(t *testing.T, port string) *storage.Client {
	t.Helper()

	c, err := storage.NewClient(context.Background(),
		option.WithEndpoint("http://127.0.0.1:"+port+"/storage/v1/"),
		option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}

	return c
}

const durableBucketPolicy = `{"Version":"2012-10-17","Statement":[{"Sid":"AllowGet","Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::durable/*"}]}`

const durableRolePolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`

// createDurableResources drives real SDK mutations across AWS/GCP/Azure,
// including the S3 and IAM Get-then-mutate sub-resource writes the request seam
// must catch (they never call a memstore mutator).
func createDurableResources(t *testing.T, p durabilityPorts) {
	t.Helper()

	ctx := context.Background()
	s3c := s3Client(t, p.aws)

	if _, err := s3c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("durable")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	if _, err := s3c.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{
		Bucket:  aws.String("durable"),
		Tagging: &types.Tagging{TagSet: []types.Tag{{Key: aws.String("env"), Value: aws.String("prod")}}},
	}); err != nil {
		t.Fatalf("PutBucketTagging: %v", err)
	}

	if _, err := s3c.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: aws.String("durable"),
		Policy: aws.String(durableBucketPolicy),
	}); err != nil {
		t.Fatalf("PutBucketPolicy: %v", err)
	}

	if _, err := s3c.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{
		Bucket: aws.String("durable"),
		ServerSideEncryptionConfiguration: &types.ServerSideEncryptionConfiguration{
			Rules: []types.ServerSideEncryptionRule{{
				ApplyServerSideEncryptionByDefault: &types.ServerSideEncryptionByDefault{SSEAlgorithm: types.ServerSideEncryptionAes256},
			}},
		},
	}); err != nil {
		t.Fatalf("PutBucketEncryption: %v", err)
	}

	iamc := iamClient(t, p.aws)
	if _, err := iamc.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String("durable-role"),
		AssumeRolePolicyDocument: aws.String(durableRolePolicy),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	if _, err := iamc.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       aws.String("durable-role"),
		PolicyName:     aws.String("inline-durable"),
		PolicyDocument: aws.String(durableRolePolicy),
	}); err != nil {
		t.Fatalf("PutRolePolicy: %v", err)
	}

	gcs := gcsClient(t, p.gcp)
	defer gcs.Close()

	if err := gcs.Bucket("gcp-durable").Create(ctx, "cloudemu-local", nil); err != nil {
		t.Fatalf("GCS Bucket.Create: %v", err)
	}

	createAzureContainer(t, p.azure)
}

// assertDurableResourcesSurvive re-reads every resource after a crash+restart and
// fails if any is missing — the crux of #447.
func assertDurableResourcesSurvive(t *testing.T, p durabilityPorts) {
	t.Helper()

	ctx := context.Background()
	s3c := s3Client(t, p.aws)

	if _, err := s3c.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: aws.String("durable")}); err != nil {
		t.Fatalf("bucket tagging did not survive the crash: %v", err)
	}

	if _, err := s3c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String("durable")}); err != nil {
		t.Fatalf("bucket policy did not survive the crash: %v", err)
	}

	if _, err := s3c.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: aws.String("durable")}); err != nil {
		t.Fatalf("bucket encryption did not survive the crash: %v", err)
	}

	iamc := iamClient(t, p.aws)
	if _, err := iamc.GetRolePolicy(ctx, &iam.GetRolePolicyInput{
		RoleName: aws.String("durable-role"), PolicyName: aws.String("inline-durable"),
	}); err != nil {
		t.Fatalf("IAM inline role policy did not survive the crash: %v", err)
	}

	gcs := gcsClient(t, p.gcp)
	defer gcs.Close()

	if _, err := gcs.Bucket("gcp-durable").Attrs(ctx); err != nil {
		t.Fatalf("GCP bucket did not survive the crash: %v", err)
	}

	assertAzureContainerExists(t, p.azure)
}

// azblobClient builds an azblob service client for the emulator's self-signed
// HTTPS endpoint, using anonymous credentials (the emulator does not verify SAS
// or SharedKey signatures).
func azblobClient(t *testing.T, port string) *azblob.Client {
	t.Helper()

	httpClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed emulator cert
	}}

	c, err := azblob.NewClientWithNoCredential("https://127.0.0.1:"+port+"/", &azblob.ClientOptions{
		ClientOptions: policy.ClientOptions{Transport: httpClient, Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		t.Fatalf("azblob client: %v", err)
	}

	return c
}

func createAzureContainer(t *testing.T, port string) {
	t.Helper()

	if _, err := azblobClient(t, port).CreateContainer(context.Background(), "durable-container", nil); err != nil {
		t.Fatalf("Azure CreateContainer: %v", err)
	}
}

func assertAzureContainerExists(t *testing.T, port string) {
	t.Helper()

	cc := azblobClient(t, port).ServiceClient().NewContainerClient("durable-container")
	if _, err := cc.GetProperties(context.Background(), nil); err != nil {
		t.Fatalf("Azure blob container did not survive the crash: %v", err)
	}
}

// TestPersistCrashDurability is the #447 acceptance test: with an always-on
// strategy, resources created via real SDKs survive a SIGKILL (no graceful
// shutdown) and a restart — which today's shutdown-only --persist loses entirely.
// It runs for both scheduled and on-request, and asserts the S3/IAM
// Get-then-mutate writes (caught only by the request-boundary seam) survive too.
func TestPersistCrashDurability(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and execs a binary; skipped in -short")
	}

	bin := buildServeBinary(t)

	for _, strategy := range []string{"scheduled", "on-request"} {
		t.Run(strategy, func(t *testing.T) {
			p := durabilityPorts{
				aws:   freePort(t),
				gcp:   freePort(t),
				azure: freePort(t),
				state: filepath.Join(t.TempDir(), "state.json"),
			}

			cmd := startServe(t, bin, p, strategy, "150ms")
			createDurableResources(t, p)

			// Wait for a background save to land BEFORE the hard kill — this is the
			// durability window under test. Both the bucket and the IAM inline
			// policy must be on disk.
			// Wait for a marker from every provider's last-created resource, so the
			// save that landed captured them all before the hard kill.
			waitFileContains(t, p.state, "inline-durable", 5*time.Second)    // AWS (S3 + IAM)
			waitFileContains(t, p.state, "gcp-durable", 5*time.Second)       // GCP
			waitFileContains(t, p.state, "durable-container", 5*time.Second) // Azure

			killHard(t, cmd)

			cmd2 := startServe(t, bin, p, strategy, "150ms")
			defer killHard(t, cmd2)

			assertDurableResourcesSurvive(t, p)
		})
	}
}

// TestPersistManualDoesNotAutoSave locks the manual contract end to end: nothing
// is saved automatically, and — the regression guard for the replaced
// unconditional shutdown save — nothing is saved even on a graceful SIGTERM
// shutdown.
func TestPersistManualDoesNotAutoSave(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and execs a binary; skipped in -short")
	}

	bin := buildServeBinary(t)
	p := durabilityPorts{
		aws:   freePort(t),
		gcp:   freePort(t),
		azure: freePort(t),
		state: filepath.Join(t.TempDir(), "state.json"),
	}

	cmd := startServe(t, bin, p, "manual", "50ms")

	s3c := s3Client(t, p.aws)
	if _, err := s3c.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String("manual-bucket")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	// Give any (wrongly-present) periodic saver time to fire.
	time.Sleep(300 * time.Millisecond)

	// Graceful shutdown: manual must still NOT save.
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()

	if b, err := os.ReadFile(p.state); err == nil && strings.Contains(string(b), "manual-bucket") {
		t.Fatalf("manual strategy saved state to disk (bucket present); it must never auto-save")
	}
}

// TestPersistAdminRestoreSurvivesCrash proves the explicit dirty-set in
// App.restore: a POST /_cloudemu/snapshot restore, with NO subsequent provider
// request, is persisted on the next tick and survives a SIGKILL — the
// admin-surface analogue of the Get-then-mutate hole.
func TestPersistAdminRestoreSurvivesCrash(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and execs a binary; skipped in -short")
	}

	bin := buildServeBinary(t)
	p := durabilityPorts{
		aws:   freePort(t),
		gcp:   freePort(t),
		azure: freePort(t),
		state: filepath.Join(t.TempDir(), "state.json"),
	}

	cmd := startServe(t, bin, p, "scheduled", "150ms")
	awsBase := "http://127.0.0.1:" + p.aws

	// Seed a bucket, then capture the whole-emulator snapshot JSON.
	s3c := s3Client(t, p.aws)
	if _, err := s3c.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String("restore-src")}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	snapshot := httpGet(t, awsBase+"/_cloudemu/snapshot")

	// Wipe, then restore from the captured snapshot. The restore is the LAST
	// state-mutating call — no provider request follows it.
	httpPost(t, awsBase+"/_cloudemu/reset", nil)
	httpPost(t, awsBase+"/_cloudemu/snapshot", snapshot)

	// The restore marked state dirty, so the scheduled tick persists it.
	waitFileContains(t, p.state, "restore-src", 5*time.Second)

	killHard(t, cmd)

	cmd2 := startServe(t, bin, p, "scheduled", "150ms")
	defer killHard(t, cmd2)

	s3c2 := s3Client(t, p.aws)
	out, err := s3c2.ListBuckets(context.Background(), &s3.ListBucketsInput{})
	if err != nil {
		t.Fatalf("ListBuckets after restart: %v", err)
	}

	var found bool

	for _, b := range out.Buckets {
		if aws.ToString(b.Name) == "restore-src" {
			found = true
		}
	}

	if !found {
		t.Fatal("restored bucket did not survive the crash; App.restore did not mark state dirty")
	}
}

func httpGet(t *testing.T, url string) []byte {
	t.Helper()

	resp, err := http.Get(url) //nolint:noctx // short-lived in-process test call
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", url, resp.StatusCode, b)
	}

	return b
}

func httpPost(t *testing.T, url string, body []byte) {
	t.Helper()

	resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:noctx // short-lived in-process test call
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s = %d: %s", url, resp.StatusCode, b)
	}
}
