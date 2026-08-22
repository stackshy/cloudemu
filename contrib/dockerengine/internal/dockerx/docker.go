package dockerx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Binary is the CLI the runner shells out to.
const Binary = "docker"

// inspectFieldCount is the number of space-separated fields the inspect template
// emits ("<status> <exitcode>").
const inspectFieldCount = 2

// envFlagWidth is the argv width of one `-e K=V` env pair ("-e" + "K=V").
const envFlagWidth = 2

// Sentinel errors wrapped by the runner so callers can match on them and the
// err113 linter stays satisfied (no dynamic error creation at the return site).
var (
	// ErrUnavailable reports that the docker CLI is not on PATH.
	ErrUnavailable = errors.New("dockerengine: docker CLI not found on PATH")

	errRun     = errors.New("dockerengine: docker run failed")
	errInspect = errors.New("dockerengine: docker inspect failed")
	errLogs    = errors.New("dockerengine: docker logs failed")
	errExec    = errors.New("dockerengine: docker exec failed")
	errRm      = errors.New("dockerengine: docker rm failed")
	errImage   = errors.New("dockerengine: image must not be empty")
)

// Available reports whether the docker CLI is present on PATH. Engine backings
// built on the Runner should gate on this and fall back to the in-memory default
// when Docker is absent.
func Available() bool {
	_, err := exec.LookPath(Binary)

	return err == nil
}

// Runner is the low-level plumbing shared by every Docker-backed engine backing
// added in later waves. It shells out to the docker CLI via os/exec — all
// arguments are first-party, so they are passed as an argv slice (never a shell
// string), sidestepping command injection. It spawns no goroutines.
type Runner struct{}

// Run starts a container from image with the given command and environment. When
// detached is set the container runs in the background and its ID is returned;
// otherwise Run blocks until the container exits and returns its ID. env keys
// are sorted so the emitted argv is deterministic.
func (Runner) Run(ctx context.Context, image string, cmd []string, env map[string]string, detached bool) (string, error) {
	if strings.TrimSpace(image) == "" {
		return "", errImage
	}

	var out bytes.Buffer

	//nolint:gosec // first-party argv, never a shell string
	c := exec.CommandContext(ctx, Binary, buildRunArgs(image, cmd, env, detached)...)
	c.Stdout = &out
	c.Stderr = &out

	if err := c.Run(); err != nil {
		return "", fmt.Errorf("%w: %s: %w", errRun, strings.TrimSpace(out.String()), err)
	}

	return strings.TrimSpace(out.String()), nil
}

// Inspect returns the container's lifecycle state (e.g. "running", "exited") and
// its exit code.
func (Runner) Inspect(ctx context.Context, id string) (state string, exitCode int, err error) {
	args := []string{"inspect", "-f", "{{.State.Status}} {{.State.ExitCode}}", id}

	out, err := exec.CommandContext(ctx, Binary, args...).Output()
	if err != nil {
		return "", 0, fmt.Errorf("%w: %w", errInspect, err)
	}

	fields := strings.Fields(string(out))
	if len(fields) != inspectFieldCount {
		return "", 0, fmt.Errorf("%w: unexpected output %q", errInspect, string(out))
	}

	code, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, fmt.Errorf("%w: bad exit code %q", errInspect, fields[1])
	}

	return fields[0], code, nil
}

// Logs returns the container's accumulated stdout/stderr. A positive tail limits
// output to the last tail lines; a non-positive tail returns everything.
func (Runner) Logs(ctx context.Context, id string, tail int) (string, error) {
	args := []string{"logs"}
	if tail > 0 {
		args = append(args, "--tail", strconv.Itoa(tail))
	}

	args = append(args, id)

	var stdout, stderr bytes.Buffer

	c := exec.CommandContext(ctx, Binary, args...)
	c.Stdout = &stdout
	c.Stderr = &stderr

	if err := c.Run(); err != nil {
		return "", fmt.Errorf("%w: %w", errLogs, err)
	}

	// docker streams container stderr to the command's stderr; callers want both.
	return stdout.String() + stderr.String(), nil
}

// Exec runs cmd inside a running container and returns its stdout, stderr, and
// exit code. A non-zero exit is reported via exitCode (not err); err is reserved
// for the docker exec itself failing to run.
func (Runner) Exec(ctx context.Context, id string, cmd []string) (stdout, stderr string, exitCode int, err error) {
	args := append([]string{"exec", id}, cmd...)

	var outBuf, errBuf bytes.Buffer

	c := exec.CommandContext(ctx, Binary, args...)
	c.Stdout = &outBuf
	c.Stderr = &errBuf
	err = c.Run()

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
	}

	if err != nil {
		return "", "", 0, fmt.Errorf("%w: %w", errExec, err)
	}

	return outBuf.String(), errBuf.String(), 0, nil
}

// Rm force-removes the container, ignoring whether it is still running.
func (Runner) Rm(ctx context.Context, id string) error {
	if err := exec.CommandContext(ctx, Binary, "rm", "-f", id).Run(); err != nil {
		return fmt.Errorf("%w: %w", errRm, err)
	}

	return nil
}

// buildRunArgs assembles the argv for `docker run`. It is separated from Run so
// it can be unit-tested without Docker present. env keys are sorted for a stable
// ordering.
func buildRunArgs(image string, cmd []string, env map[string]string, detached bool) []string {
	args := []string{"run"}
	if detached {
		args = append(args, "-d")
	}

	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}

	args = append(args, image)
	args = append(args, cmd...)

	return args
}

// EnvArgs renders sorted `-e K=V` flags for a deterministic argv.
func EnvArgs(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	args := make([]string, 0, len(keys)*envFlagWidth)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}

	return args
}

// SanitizeName maps an id to a valid docker container-name tail
// ([a-zA-Z0-9_.-]), replacing every other rune with '-'.
func SanitizeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '_' || r == '.' || r == '-':
			return r
		default:
			return '-'
		}
	}, s)
}
