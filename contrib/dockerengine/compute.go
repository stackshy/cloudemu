package dockerengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
)

const (
	// defaultComputeImage is the small, shell-capable base image a provisioned VM
	// runs in. A ComputeProvisionRequest.ImageID is an EC2 AMI id (e.g. "ami-123")
	// which has no real registry mapping, so every instance maps to this one base
	// image regardless of the requested AMI. Pin the major.minor so behavior does
	// not drift under the caller.
	defaultComputeImage = "alpine:3.20"
	// vmNamePrefix namespaces the containers this engine creates so they are easy
	// to spot (and clean up) on the host.
	vmNamePrefix = "cloudemu-vm-"
	// bootShell runs the boot script; a shebang line in the script is harmless (it
	// becomes a comment) since the whole script is handed to `sh -c` as one arg.
	bootShell = "/bin/sh"
	// envFlagWidth is the argv width of one `-e K=V` env pair ("-e" + "K=V").
	envFlagWidth = 2
)

// Sentinel errors (err113): wrapped at the return site so callers can match and
// the linter stays satisfied without dynamic error creation.
var (
	errComputeRun     = errors.New("dockerengine: compute container run failed")
	errComputeExec    = errors.New("dockerengine: compute boot script exec failed")
	errUnknownCompute = errors.New("dockerengine: no console output for unknown instance")
)

// vm records the backing container and captured boot output for one instance so
// ConsoleOutput can replay it and Deprovision can tear down exactly what was made.
type vm struct {
	containerID string
	console     []byte
}

// Compute is a config.ComputeEngine that backs each VM instance with a real
// Docker container. Provision starts a stable container (sleep infinity) and runs
// the instance's boot script inside it via `docker exec`, capturing the combined
// stdout/stderr as the console-output (cloud-init-output.log) analog. Safe for
// concurrent use within a process.
type Compute struct {
	image string

	mu       sync.Mutex
	runner   Runner
	backings map[string]vm // instanceID -> backing container + captured console
}

// Option configures a Compute engine.
type Option func(*Compute)

// WithBaseImage overrides the base image every instance runs in (default
// "alpine:3.20"). The image must be shell-capable so the boot script can run.
func WithBaseImage(image string) Option {
	return func(c *Compute) {
		if strings.TrimSpace(image) != "" {
			c.image = image
		}
	}
}

// NewCompute returns a Compute engine. By default instances run in a small
// shell-capable base image; override it with WithBaseImage.
func NewCompute(opts ...Option) *Compute {
	c := &Compute{image: defaultComputeImage, backings: map[string]vm{}}
	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Provision starts a backing container for the instance and runs its boot script
// once, capturing the combined stdout/stderr as the console output.
func (c *Compute) Provision(ctx context.Context, req config.ComputeProvisionRequest) (config.ComputeProvisionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id, err := c.runContainer(ctx, req.InstanceID)
	if err != nil {
		return config.ComputeProvisionResult{}, err
	}

	console, err := runBoot(ctx, id, req.BootScript, req.Env)
	if err != nil {
		_ = c.runner.Rm(context.Background(), id)

		return config.ComputeProvisionResult{}, err
	}

	c.backings[req.InstanceID] = vm{containerID: id, console: console}

	return config.ComputeProvisionResult{IP: containerIP(ctx, id)}, nil
}

// ConsoleOutput returns the boot script's captured combined output for the
// instance. It errors for an instance that was never provisioned.
func (c *Compute) ConsoleOutput(_ context.Context, instanceID string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	b, ok := c.backings[instanceID]
	if !ok {
		return nil, fmt.Errorf("%q: %w", instanceID, errUnknownCompute)
	}

	return b.console, nil
}

// Deprovision force-removes the backing container. No-op if the instance is
// unknown.
func (c *Compute) Deprovision(ctx context.Context, instanceID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	b, ok := c.backings[instanceID]
	if !ok {
		return nil
	}

	delete(c.backings, instanceID)

	return c.runner.Rm(ctx, b.containerID)
}

// Close force-removes every backing container. Safe to call more than once.
func (c *Compute) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, b := range c.backings {
		_ = c.runner.Rm(context.Background(), b.containerID)
		delete(c.backings, id)
	}

	return nil
}

// runContainer starts a detached, named container that stays up (sleep infinity)
// so the boot script can be exec'd into it. Every argument is first-party (never
// a shell string).
func (c *Compute) runContainer(ctx context.Context, instanceID string) (string, error) {
	name := vmNamePrefix + sanitizeName(instanceID)

	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, dockerBinary, "run", "-d", "--name", name, c.image, "sleep", "infinity")
	// Keep stdout (the container ID) clean of image-pull progress, which docker
	// writes to stderr; merging them would corrupt the ID on a first-run pull.
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s: %w", errComputeRun, strings.TrimSpace(stderr.String()), err)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// runBoot execs the boot script inside the container, injecting env, and returns
// the combined stdout/stderr. An empty script skips the exec (empty console). A
// non-zero script exit is NOT an error — its output is still valid console output,
// just as a real cloud-init-output.log records a failed boot; only the docker exec
// itself failing to run is surfaced as an error.
func runBoot(ctx context.Context, containerID string, script []byte, env map[string]string) ([]byte, error) {
	if len(bytes.TrimSpace(script)) == 0 {
		return nil, nil
	}

	const fixedArgs = 5 // "exec" + containerID + shell + "-c" + script

	args := make([]string, 0, fixedArgs+len(env)*envFlagWidth)
	args = append(args, "exec")
	args = append(args, envArgs(env)...)
	args = append(args, containerID, bootShell, "-c", string(script))

	cmd := exec.CommandContext(ctx, dockerBinary, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return out, nil
		}

		return nil, fmt.Errorf("%w: %s: %w", errComputeExec, strings.TrimSpace(string(out)), err)
	}

	return out, nil
}

// containerIP returns the container's IP from docker inspect, or "" when none is
// available — an empty IP is acceptable per the ComputeEngine contract, so any
// inspect failure degrades to "".
func containerIP(ctx context.Context, containerID string) string {
	const tmpl = "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}"

	out, err := exec.CommandContext(ctx, dockerBinary, "inspect", "-f", tmpl, containerID).Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}

// envArgs renders sorted `-e K=V` flags for a deterministic argv.
func envArgs(env map[string]string) []string {
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

// sanitizeName maps an instance id to a valid docker container-name tail
// ([a-zA-Z0-9_.-]), replacing every other rune with '-'.
func sanitizeName(s string) string {
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

// staticComputeCheck asserts Compute satisfies the config.ComputeEngine contract
// at compile time.
var _ config.ComputeEngine = (*Compute)(nil)
