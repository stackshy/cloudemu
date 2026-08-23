// Package terraform_test drives a real Terraform/OpenTofu binary through
// init → apply → plan → destroy against an in-process CloudEmu AWS wire server,
// proving CloudEmu is IaC-compatible. The load-bearing assertion is the
// post-apply plan: it must report NO changes (exit 0). A change means a
// resource read did not round-trip what apply wrote — a "perpetual diff" — the
// class of wire-fidelity bug this suite exists to catch.
package terraform_test

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cloudemu "github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestTerraformApplyIsIdempotent runs each fixture through the full
// init → apply → plan → destroy loop against a fresh in-process AWS server and
// asserts the post-apply plan is empty. Each fixture is one scenario (a family
// of resources); add a directory under fixtures/ and a name here to cover more.
func TestTerraformApplyIsIdempotent(t *testing.T) {
	bin := tfBinary(t)

	for _, fixture := range []string{"basic", "networking"} {
		t.Run(fixture, func(t *testing.T) {
			applyFixture(t, fixture, bin)
		})
	}
}

func applyFixture(t *testing.T, fixture, bin string) {
	t.Helper()

	// In-process AWS wire server — no subprocess, no Docker.
	srv := httptest.NewServer(awsserver.NewFromProvider(cloudemu.NewAWS()))
	defer srv.Close()

	tf := newTerraform(t, fixture, bin)
	ctx := context.Background()
	endpoint := tfexec.Var("endpoint=" + srv.URL)

	require.NoError(t, tf.Init(ctx), "terraform init")

	require.NoError(t, tf.Apply(ctx, endpoint), "terraform apply")
	t.Cleanup(func() { _ = tf.Destroy(context.Background(), endpoint) })

	// The idempotency bar: a re-plan after apply must show no changes.
	var planOut bytes.Buffer
	tf.SetStdout(&planOut)
	changed, err := tf.Plan(ctx, endpoint)
	tf.SetStdout(io.Discard)
	require.NoError(t, err, "terraform plan")

	if changed {
		t.Logf("non-empty plan after apply (perpetual diff):\n%s", planOut.String())
	}

	assert.False(t, changed, "plan after apply reports changes → a resource read did not round-trip")

	// Destroy must succeed (the config still declares the resources, so a plan
	// after destroy naturally wants to re-create them — success is the signal).
	require.NoError(t, tf.Destroy(ctx, endpoint), "terraform destroy")
}

// newTerraform copies fixtures/<name> into a temp workdir and returns a tfexec
// handle. A plugin cache (set on the inherited environment) keeps init from
// re-downloading the provider every run.
func newTerraform(t *testing.T, fixture, bin string) *tfexec.Terraform {
	t.Helper()

	t.Setenv("TF_PLUGIN_CACHE_DIR", pluginCacheDir(t))

	work := copyFixture(t, fixture)

	tf, err := tfexec.NewTerraform(work, bin)
	require.NoError(t, err)

	return tf
}

// tfBinary prefers OpenTofu (the licensing-safe default), falling back to
// Terraform; it skips the test when neither is installed.
func tfBinary(t *testing.T) string {
	t.Helper()

	for _, name := range []string{"tofu", "terraform"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}

	t.Skip("no tofu/terraform binary on PATH")

	return ""
}

func copyFixture(t *testing.T, name string) string {
	t.Helper()

	src := filepath.Join("fixtures", name)
	dst := t.TempDir()

	entries, err := os.ReadDir(src)
	require.NoError(t, err)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}

		b, rerr := os.ReadFile(filepath.Join(src, e.Name()))
		require.NoError(t, rerr)
		require.NoError(t, os.WriteFile(filepath.Join(dst, e.Name()), b, 0o600))
	}

	return dst
}

// pluginCacheDir is a stable per-user dir so repeated runs reuse the downloaded
// provider instead of fetching it each time.
func pluginCacheDir(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(os.TempDir(), "cloudemu-tf-plugin-cache")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	return dir
}
