package container

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"

	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/internal/dockerx"
)

const (
	// taskNamePrefix namespaces every container this engine creates so they are
	// easy to spot (and clean up) on the host: `docker ps -a --filter
	// name=cloudemu-task-`.
	taskNamePrefix = "cloudemu-task-"
	// handleBytes is the number of random bytes behind a workload handle; hex
	// encoded it yields a collision-resistant, docker-name-safe id tail.
	handleBytes = 8
)

// Sentinel errors (err113): wrapped at the return site so callers can match and
// the linter stays satisfied without dynamic error creation.
var (
	errContainerRun   = errors.New("dockerengine: container run failed")
	errUnknownHandle  = errors.New("dockerengine: unknown workload handle")
	errUnknownName    = errors.New("dockerengine: unknown container name in workload")
	errHandleGen      = errors.New("dockerengine: could not generate workload handle")
	errNoContainers   = errors.New("dockerengine: run spec has no containers")
	errContainerImage = errors.New("dockerengine: container image must not be empty")
)

// containerRef ties a workload's neutral container name to the concrete docker
// container backing it, so Status/Logs/Exec can resolve a spec container name to
// the real container and Stop can tear down exactly what was created.
type containerRef struct {
	name string // spec container name (config.ContainerSpec.Name)
	ref  string // docker container id (every container is started detached)
}

// Containers is a config.ContainerEngine that runs each workload's containers as
// real Docker containers, all started detached. A RunToCompletion workload (a
// standalone ECS RunTask) blocks until the first container exits, so Status
// returns the real exit code and Logs returns the real captured output; a
// non-completion workload (a service) returns as soon as its containers are
// started. Safe for concurrent use within a process.
type Containers struct {
	mu        sync.Mutex
	runner    dockerx.Runner
	workloads map[string][]containerRef // handle -> backing containers
}

// New returns a Containers engine. Containers are created on Run and
// torn down on Stop/Close; the docker CLI must be on PATH (gate with
// dockerx.Available).
func New() *Containers {
	return &Containers{workloads: map[string][]containerRef{}}
}

// Run starts every container in the spec and returns an opaque handle the other
// methods use. All containers are started detached and concurrently, so a
// multi-container workload never serializes on one container's lifetime. When
// spec.RunToCompletion is set Run then blocks until the *first* container exits
// (a non-zero exit is not an error — its real exit code and output remain
// observable via Status/Logs) and returns, leaving any still-running siblings up
// for Status to observe and Stop to tear down — mirroring a real task that stops
// the moment its essential container exits. Without RunToCompletion Run returns
// as soon as the containers are started.
func (c *Containers) Run(ctx context.Context, spec config.ContainerRunSpec) (string, error) {
	if len(spec.Containers) == 0 {
		return "", errNoContainers
	}

	handle, err := newHandle()
	if err != nil {
		return "", err
	}

	refs := make([]containerRef, 0, len(spec.Containers))

	for i := range spec.Containers {
		cs := &spec.Containers[i]
		if strings.TrimSpace(cs.Image) == "" {
			c.teardown(refs)

			return "", fmt.Errorf("%q: %w", cs.Name, errContainerImage)
		}

		dockerName := taskNamePrefix + dockerx.SanitizeName(handle) + "-" + dockerx.SanitizeName(cs.Name)

		ref, runErr := runDetached(ctx, dockerName, cs)
		if runErr != nil {
			c.teardown(refs)

			return "", runErr
		}

		refs = append(refs, containerRef{name: cs.Name, ref: ref})
	}

	if spec.RunToCompletion {
		waitForFirstExit(ctx, refs)
	}

	c.mu.Lock()
	c.workloads[handle] = refs
	c.mu.Unlock()

	return handle, nil
}

// waitForFirstExit blocks until the first container in refs exits (or ctx is
// canceled). Each container is waited on in its own goroutine via `docker wait`;
// once the first returns, the remaining waits are canceled and joined so no
// goroutine outlives the call. The containers themselves are left running — they
// are torn down later by Stop/Close — so Status can still observe a sibling that
// had not exited when the first one did.
func waitForFirstExit(ctx context.Context, refs []containerRef) {
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	first := make(chan struct{}, 1)

	var wg sync.WaitGroup

	for i := range refs {
		wg.Add(1)

		go func(ref string) {
			defer wg.Done()

			//nolint:gosec // first-party argv, never a shell string
			_ = exec.CommandContext(waitCtx, dockerx.Binary, "wait", ref).Run()

			select {
			case first <- struct{}{}:
			default:
			}
		}(refs[i].ref)
	}

	select {
	case <-first:
	case <-ctx.Done():
	}

	cancel()
	wg.Wait()
}

// Status reports each container's docker lifecycle state (e.g. "running",
// "exited") and exit code, matched to the spec container name. The raw docker
// status is returned unchanged — the ECS wiring maps it onto the ECS lastStatus
// vocabulary.
func (c *Containers) Status(ctx context.Context, handle string) ([]config.ContainerStatus, error) {
	refs, err := c.refsFor(handle)
	if err != nil {
		return nil, err
	}

	out := make([]config.ContainerStatus, 0, len(refs))

	for _, r := range refs {
		state, code, ierr := c.runner.Inspect(ctx, r.ref)
		if ierr != nil {
			return nil, ierr
		}

		out = append(out, config.ContainerStatus{Name: r.name, State: state, ExitCode: code})
	}

	return out, nil
}

// Logs returns the accumulated stdout/stderr for one named container in the
// workload. A non-positive tailLines returns the full log.
func (c *Containers) Logs(ctx context.Context, handle, container string, tailLines int) (string, error) {
	ref, err := c.resolve(handle, container)
	if err != nil {
		return "", err
	}

	return c.runner.Logs(ctx, ref, tailLines)
}

// Exec runs a command inside one named container and returns its stdout, stderr,
// and exit code. A Go error is reserved for docker failing to run the command; a
// non-zero command exit is reported via ExecResult.ExitCode.
func (c *Containers) Exec(ctx context.Context, handle, container string, cmd []string) (config.ExecResult, error) {
	ref, err := c.resolve(handle, container)
	if err != nil {
		return config.ExecResult{}, err
	}

	stdout, stderr, code, err := c.runner.Exec(ctx, ref, cmd)
	if err != nil {
		return config.ExecResult{}, err
	}

	return config.ExecResult{Stdout: stdout, Stderr: stderr, ExitCode: code}, nil
}

// Stop force-removes every container backing the workload and forgets it. It is
// a no-op for an unknown handle.
func (c *Containers) Stop(_ context.Context, handle string) error {
	c.mu.Lock()
	refs, ok := c.workloads[handle]
	delete(c.workloads, handle)
	c.mu.Unlock()

	if !ok {
		return nil
	}

	c.teardown(refs)

	return nil
}

// Close force-removes every container this engine created across all workloads.
// Safe to call more than once.
func (c *Containers) Close() error {
	c.mu.Lock()
	all := c.workloads
	c.workloads = map[string][]containerRef{}
	c.mu.Unlock()

	for _, refs := range all {
		c.teardown(refs)
	}

	return nil
}

// runDetached runs `docker run -d --name <n> -e K=V <image> <cmd...>` and returns
// the container id. stdout (the id) and stderr (image-pull progress) are captured
// SEPARATELY so first-run pull progress can never corrupt the parsed id.
func runDetached(ctx context.Context, dockerName string, cs *config.ContainerSpec) (string, error) {
	var stdout, stderr bytes.Buffer

	//nolint:gosec // first-party argv, never a shell string
	cmd := exec.CommandContext(ctx, dockerx.Binary, containerRunArgs(dockerName, cs)...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s: %w", errContainerRun, strings.TrimSpace(stderr.String()), err)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// containerRunArgs assembles the argv for `docker run -d --name <n> -e K=V
// <image> <cmd...>`. Every workload container is started detached; RunToCompletion
// is realized by waiting on the container afterwards, not by a foreground run. env
// keys are sorted (via EnvArgs) for a deterministic argv; every argument is
// first-party, never a shell string.
func containerRunArgs(dockerName string, cs *config.ContainerSpec) []string {
	env := dockerx.EnvArgs(cs.Env)

	const fixed = 5 // "run" + "-d" + "--name" + name + image

	args := make([]string, 0, fixed+len(env)+len(cs.Command))
	args = append(args, "run", "-d", "--name", dockerName)
	args = append(args, env...)
	args = append(args, cs.Image)
	args = append(args, cs.Command...)

	return args
}

// refsFor returns the backing containers for a handle, erroring for an unknown
// handle.
func (c *Containers) refsFor(handle string) ([]containerRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	refs, ok := c.workloads[handle]
	if !ok {
		return nil, fmt.Errorf("%q: %w", handle, errUnknownHandle)
	}

	return refs, nil
}

// resolve maps a workload handle + spec container name to the concrete docker
// container ref.
func (c *Containers) resolve(handle, container string) (string, error) {
	refs, err := c.refsFor(handle)
	if err != nil {
		return "", err
	}

	for _, r := range refs {
		if r.name == container {
			return r.ref, nil
		}
	}

	return "", fmt.Errorf("%q in %q: %w", container, handle, errUnknownName)
}

// teardown force-removes every referenced container, ignoring errors so a
// partially-created or already-gone workload still cleans up what it can.
func (c *Containers) teardown(refs []containerRef) {
	for _, r := range refs {
		_ = c.runner.Rm(context.Background(), r.ref)
	}
}

// newHandle returns a random, docker-name-safe workload handle.
func newHandle() (string, error) {
	b := make([]byte, handleBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("%w: %w", errHandleGen, err)
	}

	return hex.EncodeToString(b), nil
}

// staticContainerCheck asserts Containers satisfies the config.ContainerEngine
// contract at compile time.
var _ config.ContainerEngine = (*Containers)(nil)
