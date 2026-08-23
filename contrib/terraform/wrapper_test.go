package terraform_test

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	cloudemu "github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestCloudemuTfWrapperIsIdempotent drives the cloudemu-tf wrapper (not raw
// terraform) through init → apply → plan → destroy against an in-process
// CloudEmu. The fixture carries only an empty `provider "aws" {}` block; the
// wrapper injects the endpoints, credentials and flags. A post-apply
// `plan -detailed-exitcode` of 0 proves the generated provider config both
// reaches CloudEmu and round-trips.
func TestCloudemuTfWrapperIsIdempotent(t *testing.T) {
	bin := tfBinary(t) // skips when neither tofu nor terraform is present
	wrapper := wrapperPath(t)

	srv := httptest.NewServer(awsserver.NewFromProvider(cloudemu.NewAWS()))
	defer srv.Close()

	work := copyFixture(t, "wrapper")

	env := append(os.Environ(),
		"CLOUDEMU_ENDPOINT="+srv.URL,
		"CLOUDEMU_TF_BIN="+bin,
	)

	run := func(args ...string) (int, string) {
		full := append([]string{wrapper, "-chdir=" + work}, args...)
		cmd := exec.Command("sh", full...) //nolint:gosec // test-controlled args
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return cmd.ProcessState.ExitCode(), string(out) + errString(err)
	}

	code, out := run("init")
	require.Equal(t, 0, code, "init failed:\n%s", out)

	code, out = run("apply", "-auto-approve")
	require.Equal(t, 0, code, "apply failed:\n%s", out)
	t.Cleanup(func() { _, _ = run("destroy", "-auto-approve") })

	// -detailed-exitcode: 0 = no changes, 2 = a diff (perpetual diff), 1 = error.
	code, out = run("plan", "-detailed-exitcode")
	require.Equal(t, 0, code, "plan after apply is not empty (exit %d):\n%s", code, out)

	code, out = run("destroy", "-auto-approve")
	require.Equal(t, 0, code, "destroy failed:\n%s", out)
}

func wrapperPath(t *testing.T) string {
	t.Helper()

	p, err := filepath.Abs("cloudemu-tf")
	require.NoError(t, err)

	if _, statErr := os.Stat(p); statErr != nil {
		t.Fatalf("wrapper script not found at %s: %v", p, statErr)
	}

	return p
}

func errString(err error) string {
	if err == nil {
		return ""
	}

	return "\n" + err.Error()
}
