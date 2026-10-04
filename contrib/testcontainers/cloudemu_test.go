package cloudemu_test

import (
	"context"
	"flag"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	cloudemu "github.com/stackshy/cloudemu/v2/contrib/testcontainers"
)

// testImage is built once from the repo Dockerfile so the test doesn't depend
// on a published image. Override with CLOUDEMU_TEST_IMAGE to use another tag.
const testImage = "cloudemu:tctest"

// dockerOK is set by TestMain when the container tests can run. Tests that need
// no Docker run either way.
var dockerOK bool //nolint:gochecknoglobals // set once in TestMain, read by tests

func requireDocker(t *testing.T) {
	t.Helper()
	if !dockerOK {
		t.Skip("needs Docker (skipped in -short, with CLOUDEMU_SKIP_DOCKER, or when docker is missing)")
	}
}

func TestMain(m *testing.M) {
	flag.Parse() // so testing.Short() is readable here
	if testing.Short() || os.Getenv("CLOUDEMU_SKIP_DOCKER") != "" {
		os.Exit(m.Run())
	}
	if _, err := exec.LookPath("docker"); err != nil {
		os.Exit(m.Run()) // no docker → only the Docker-free tests run
	}
	dockerOK = true
	if os.Getenv("CLOUDEMU_TEST_IMAGE") == "" {
		build := exec.Command("docker", "build", "-t", testImage, "../..")
		build.Stdout, build.Stderr = os.Stderr, os.Stderr
		if err := build.Run(); err != nil {
			os.Stderr.WriteString("docker build failed: " + err.Error() + "\n")
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

func image() string {
	if v := os.Getenv("CLOUDEMU_TEST_IMAGE"); v != "" {
		return v
	}
	return testImage
}

// TestRunResetSeed is the #248 acceptance: start the container, drive it over
// its mapped endpoint, and exercise the reset/seed control plane.
func TestRunResetSeed(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	ctr, err := cloudemu.Run(ctx, cloudemu.WithImage(image()))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = ctr.Terminate(ctx)
	})

	ep, err := ctr.AWSEndpoint(ctx)
	if err != nil {
		t.Fatalf("AWSEndpoint: %v", err)
	}

	// Drive the S3 wire against the container: create a bucket + object, read back.
	put(t, ep+"/tc-bucket", "")
	put(t, ep+"/tc-bucket/hello.txt", "hi from testcontainers")
	if body := get(t, ep+"/tc-bucket/hello.txt"); body != "hi from testcontainers" {
		t.Fatalf("object body = %q, want %q", body, "hi from testcontainers")
	}

	// Reset clears it.
	if err := ctr.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if code := status(t, ep+"/tc-bucket/hello.txt"); code == http.StatusOK {
		t.Fatal("object still present after Reset")
	}

	// Seed loads a fixture, readable back over the wire.
	fixture := map[string]any{
		"buckets": []map[string]any{
			{"name": "seeded", "objects": []map[string]any{{"key": "k.txt", "body": "from seed"}}},
		},
	}
	if err := ctr.Seed(ctx, fixture); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if body := get(t, ep+"/seeded/k.txt"); body != "from seed" {
		t.Fatalf("seeded object = %q, want %q", body, "from seed")
	}
}

// TestEnforceAuthAdminToken runs the container with --enforce-auth: Reset and
// Seed carry the admin token, an unauthenticated reset is refused, and health
// (the readiness probe) stays open.
func TestEnforceAuthAdminToken(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	ctr, err := cloudemu.Run(ctx, cloudemu.WithImage(image()), cloudemu.WithEnforceAuth("tc-admin-token"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = ctr.Terminate(ctx)
	})

	ep, err := ctr.AWSEndpoint(ctx)
	if err != nil {
		t.Fatalf("AWSEndpoint: %v", err)
	}

	if code := status(t, ep+"/_cloudemu/health"); code != http.StatusOK {
		t.Fatalf("health = %d, want 200", code)
	}
	if code := status(t, ep+"/_cloudemu/snapshot"); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated snapshot = %d, want 401", code)
	}

	if err := ctr.Reset(ctx); err != nil {
		t.Fatalf("Reset with the token: %v", err)
	}

	fixture := map[string]any{"iamUsers": []map[string]any{{
		"name":       "boot",
		"accessKeys": []map[string]any{{"accessKeyId": "AKIATESTCONTAINERS01", "secretAccessKey": "tc-secret"}},
	}}}
	if err := ctr.Seed(ctx, fixture); err != nil {
		t.Fatalf("Seed with the token: %v", err)
	}
}

func put(t *testing.T, url, body string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT %s = %d", url, resp.StatusCode)
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func status(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
